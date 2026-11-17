package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/webpush"
)

// A device is dropped once pushMaxFailures deliveries in a row, spread over
// at least pushFailureGrace, failed with an answer that blames the device.
// Gone answers (404, 410) drop it at once. The grace period keeps a burst of
// failures (an alert and its retries) from removing a device. Failures are
// always counted (the device list shows them), but a device is only dropped
// while its push service delivers to other devices, so a mistake in this
// instance's VAPID setup removes nothing.
const (
	pushMaxFailures  = 5
	pushFailureGrace = 72 * time.Hour
)

// pushConcurrency bounds parallel sends of one alert, so a slow push
// service does not hold up the evaluator loop for every device in turn.
const pushConcurrency = 8

// pushFiringTimeout bounds all sends of one alert, however many devices it
// has. Devices not reached in time are retried with the firing.
const pushFiringTimeout = 45 * time.Second

// pushTTL is how long push services hold an alert for a device that is offline.
const pushTTL = 24 * time.Hour

const vapidSecretKey = "vapid_private_key"

// pushRetryDevicesKey in an alert's details lists the devices a retry still
// has to reach. A partly delivered alert is retried for those only, so the
// devices that got it are not notified twice.
const pushRetryDevicesKey = "push_retry_device_ids"

// pushPartlyDeliveredKey in an alert's details records that an earlier
// attempt reached some devices, so a retry that reaches none of the rest is
// still a partial delivery, not a failed alert.
const pushPartlyDeliveredKey = "push_partly_delivered"

// PushNotification is the JSON the service worker receives.
type PushNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// URL is a path on this instance, opened when the notification is clicked.
	URL string `json:"url"`
	// Tag makes a newer notification replace an older one with the same tag.
	// Alerts have none: each one is about different issues or monitors, so
	// none may hide another. Only the device test uses one.
	Tag string `json:"tag,omitempty"`
}

// LoadPushSender sets up Web Push with the instance's VAPID keys.
// VAPID_PRIVATE_KEY overrides the key generated and stored on first start;
// VAPID_SUBJECT overrides the contact derived from PUBLIC_URL;
// PUSH_EXTRA_HOSTS adds push service hosts to webpush.DefaultHosts.
// The sender gets its HTTP client from the evaluator in SetPushSender.
func LoadPushSender(ctx context.Context, pool *pgxpool.Pool, publicURL string) (*webpush.Sender, error) {
	private := strings.TrimSpace(os.Getenv("VAPID_PRIVATE_KEY"))
	if private == "" {
		var err error
		private, err = storage.GetOrCreateInstanceSecret(ctx, pool, vapidSecretKey, func() (string, error) {
			k, err := webpush.GenerateKeys()
			if err != nil {
				return "", err
			}
			return k.Private, nil
		})
		if err != nil {
			return nil, fmt.Errorf("vapid key: %w", err)
		}
	}
	keys, err := webpush.ParseKeys(private)
	if err != nil {
		return nil, err
	}
	subject, err := vapidSubject(os.Getenv("VAPID_SUBJECT"), publicURL)
	if err != nil {
		return nil, err
	}
	if subject == fallbackVapidSubject {
		slog.Warn("VAPID_SUBJECT is not set and PUBLIC_URL is not https; push services may reject notifications (Safari does). Set VAPID_SUBJECT=mailto:you@your-domain")
	}
	return &webpush.Sender{
		Keys:    keys,
		Subject: subject,
		Hosts:   pushHosts(os.Getenv("PUSH_EXTRA_HOSTS")),
	}, nil
}

// pushHosts is webpush.DefaultHosts plus a comma separated list of extra
// hosts, for browsers that use a self-hosted push service. Endpoints are
// matched by host name alone, so a scheme, port or path typed into an entry
// is dropped (with a warning) rather than making it match nothing.
func pushHosts(extra string) []string {
	hosts := slices.Clone(webpush.DefaultHosts)
	for raw := range strings.SplitSeq(extra, ",") {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		h := entry
		if i := strings.Index(h, "://"); i >= 0 {
			h = h[i+3:]
		}
		h, _, _ = strings.Cut(h, "/")
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		}
		if h != entry {
			slog.Warn("PUSH_EXTRA_HOSTS entries are host names; using the host only", "entry", entry, "host", h)
		}
		if h != "" && h != "*." && !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// fallbackVapidSubject is used when nothing better is known. It is a valid
// URI, but some push services reject contacts on localhost.
const fallbackVapidSubject = "mailto:webpush@localhost"

// vapidSubject picks the contact push services can reach the operator at.
// They require a mailto: or https: URI and reject every notification
// otherwise, so a malformed VAPID_SUBJECT is an error at startup.
func vapidSubject(explicit, publicURL string) (string, error) {
	if s := strings.TrimSpace(explicit); s != "" {
		u, err := url.Parse(s)
		switch {
		case err == nil && u.Scheme == "mailto" && strings.Contains(u.Opaque, "@"):
		case err == nil && u.Scheme == "https" && u.Host != "":
		default:
			return "", fmt.Errorf("VAPID_SUBJECT must be a mailto: address or an https: URL, got %q", s)
		}
		return s, nil
	}
	if u, err := url.Parse(publicURL); err == nil && u.Scheme == "https" && u.Host != "" {
		return "https://" + u.Host, nil
	}
	return fallbackVapidSubject, nil
}

// SetPushSender enables the push channel. Without it push rules fail with a
// clear error instead of silently doing nothing. A sender without a client
// uses the evaluator's, which blocks private addresses like webhooks do.
func (e *Evaluator) SetPushSender(s *webpush.Sender) {
	if s != nil && s.Client == nil {
		s.Client = e.client
	}
	e.push = s
}

// PushEndpointAllowed reports whether a browser's endpoint is a push service
// this instance delivers to.
func (e *Evaluator) PushEndpointAllowed(endpoint string) bool {
	return e != nil && e.push != nil && e.push.AllowsEndpoint(endpoint)
}

// PushPublicKey is the VAPID key browsers subscribe with, or "" when push is off.
func (e *Evaluator) PushPublicKey() string {
	if e == nil || e.push == nil {
		return ""
	}
	return e.push.Keys.Public
}

// SendTestPush sends a test notification to one device.
func (e *Evaluator) SendTestPush(ctx context.Context, sub *storage.PushSubscription) error {
	if e.push == nil {
		return errors.New("web push not configured")
	}
	body, err := encodePushNotification(PushNotification{
		Title: "Tindra test notification",
		Body:  "Push notifications work on this device.",
		URL:   "/settings/profile",
		Tag:   "tindra-test",
	})
	if err != nil {
		return err
	}
	// A test press never counts against the device: the user is looking at
	// the error, and pressing it repeatedly must not remove the device.
	return e.sendPush(ctx, sub, webpush.Message{Payload: body, TTL: time.Minute, Urgency: "high"}).err
}

// firePush notifies every device of every recipient. It fails when no device
// got the alert, and when some devices missed it for a reason a retry can
// fix (an outage, a timeout); the retry then goes to those devices only (see
// pushRetryDevicesKey), so nobody is notified twice.
func (e *Evaluator) firePush(ctx context.Context, rule *storage.AlertRule, payload AlertPayload) error {
	if e.push == nil {
		return errors.New("web push not configured")
	}
	owed := detailStrings(payload.Details, pushRetryDevicesKey)
	subs, err := storage.ListPushSubscriptionsForRule(ctx, e.pool, rule.ID, owed)
	if err != nil {
		return err
	}
	if len(owed) > 0 {
		if len(subs) == 0 {
			// The devices still owed this alert were removed meanwhile.
			if earlier, _ := payload.Details[pushPartlyDeliveredKey].(bool); earlier {
				return nil // it reached everyone it still can
			}
			return notRetryable(errors.New("the devices this alert was owed to were removed"))
		}
	}
	if len(subs) == 0 {
		// Nobody to send to; a retry would find the same.
		return notRetryable(errors.New("no recipient has a device registered for push notifications"))
	}
	body, err := encodePushNotification(buildPushNotification(payload))
	if err != nil {
		return err
	}
	// No Topic either: the push service would drop an alert still queued for
	// an offline device when a later one (or a rule test) arrives.
	msg := webpush.Message{Payload: body, TTL: pushTTL, Urgency: "high"}

	sendCtx, cancel := context.WithTimeout(ctx, pushFiringTimeout)
	defer cancel()
	results := make([]pushOutcome, len(subs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, pushConcurrency)
	for i, sub := range subs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if sendCtx.Err() != nil {
				results[i] = pushOutcome{err: fmt.Errorf("device %q: %w", sub.Label, sendCtx.Err()), reason: "timeout"}
				return
			}
			results[i] = e.sendPush(sendCtx, sub, msg)
		})
	}
	wg.Wait()
	// A rule test, like a device test, never counts against a device.
	if test, _ := payload.Details["test"].(bool); !test {
		e.recordPushFaults(context.WithoutCancel(ctx), subs, results)
	}

	var failed int
	var reasons []string
	var retry []string
	for i, r := range results {
		if r.err == nil {
			continue
		}
		failed++
		reasons = append(reasons, r.reason)
		// The device labels stay in the server log: the firing log, which
		// API tokens can read, says what happened, not to whose devices.
		slog.Warn("push delivery failed", "rule", rule.ID, "subscription", subs[i].ID, "err", r.err)
		// Retrying helps only after an outage or a timeout. A device that is
		// gone or that its push service rejected fails the same way again;
		// its failures show in the device list.
		if !r.gone && !r.fault {
			retry = append(retry, subs[i].ID)
		}
	}
	if failed == 0 {
		return nil
	}
	delivered := len(subs) - failed
	earlier, _ := payload.Details[pushPartlyDeliveredKey].(bool)
	reached := delivered > 0 || earlier
	summary := fmt.Errorf("push failed on %d of %d devices (%s)", failed, len(subs), countReasons(reasons))
	if len(retry) == 0 {
		if !reached {
			// Every device is gone or was rejected: trying again cannot help.
			return notRetryable(summary)
		}
		return nil
	}
	// Retry only the devices a retry can help. Details is shared with the
	// caller, which stores the payload for the retry; every caller sets it.
	// Should one not, the error still makes the whole alert retry.
	if payload.Details != nil {
		payload.Details[pushRetryDevicesKey] = retry
		if delivered > 0 {
			payload.Details[pushPartlyDeliveredKey] = true
		}
	}
	if reached {
		return partlyDelivered(summary)
	}
	return summary
}

// countReasons turns ["403", "403", "timeout"] into "403 on 2, timeout on 1".
func countReasons(reasons []string) string {
	counts := map[string]int{}
	var order []string
	for _, r := range reasons {
		if counts[r] == 0 {
			order = append(order, r)
		}
		counts[r]++
	}
	parts := make([]string, len(order))
	for i, r := range order {
		parts[i] = fmt.Sprintf("%s on %d", r, counts[r])
	}
	return strings.Join(parts, ", ")
}

// pushOutcome is how one delivery went. fault is set when the push service
// blamed the device (see deviceFault); gone when the device was removed.
// reason says briefly what went wrong, without naming the device.
type pushOutcome struct {
	err    error
	fault  bool
	gone   bool
	reason string
}

// sendPush delivers to one device and keeps the bookkeeping that needs no
// judgement: success resets the failure count and Gone deletes the device.
// Whether a fault counts toward removal is up to the caller.
func (e *Evaluator) sendPush(ctx context.Context, sub *storage.PushSubscription, msg webpush.Message) pushOutcome {
	res, err := e.push.Send(ctx, webpush.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, msg)
	// Bookkeeping must not be skipped because the request's context ended.
	bookkeeping := context.WithoutCancel(ctx)
	if err == nil {
		if err := storage.RecordPushSuccess(bookkeeping, e.pool, sub); err != nil {
			slog.Error("record push success", "subscription", sub.ID, "err", err)
		}
		return pushOutcome{}
	}
	if res.Gone || errors.Is(err, webpush.ErrHostNotAllowed) {
		// Gone, or on a push service this instance no longer delivers to
		// (its host was taken out of PUSH_EXTRA_HOSTS): it can never work.
		if _, derr := storage.ExpirePushSubscription(bookkeeping, e.pool, sub); derr != nil {
			slog.Error("expire push subscription", "subscription", sub.ID, "err", derr)
		}
		if !res.Gone {
			return pushOutcome{err: fmt.Errorf("device %q is not on an allowed push service and was removed", sub.Label), gone: true, reason: "not an allowed push service"}
		}
		return pushOutcome{err: fmt.Errorf("device %q unsubscribed and was removed", sub.Label), gone: true, reason: "unsubscribed"}
	}
	if res.Detail != "" {
		slog.Warn("push service rejected delivery", "subscription", sub.ID, "status", res.StatusCode, "detail", res.Detail)
	}
	reason := "unreachable"
	switch {
	case res.StatusCode != 0:
		reason = fmt.Sprintf("push service returned %d", res.StatusCode)
	case errors.Is(err, context.DeadlineExceeded):
		reason = "timeout"
	}
	return pushOutcome{err: fmt.Errorf("device %q: %w", sub.Label, err), fault: deviceFault(res.StatusCode), reason: reason}
}

// recordPushFaults counts faults against their devices. A device may only
// be dropped while its push service delivered to some device within the
// grace period: when a service rejects every device, the instance is the
// likely cause (a changed VAPID key, a contact the service does not accept).
func (e *Evaluator) recordPushFaults(ctx context.Context, subs []*storage.PushSubscription, results []pushOutcome) {
	healthy := map[string]bool{}
	for i, r := range results {
		if !r.fault {
			continue
		}
		service := e.push.ServiceFor(subs[i].Endpoint)
		ok, known := healthy[service]
		if !known && service != "" {
			var err error
			if ok, err = storage.PushDeliveredToService(ctx, e.pool, service, pushFailureGrace); err != nil {
				slog.Error("check push service health", "service", service, "err", err)
			}
			healthy[service] = ok
		}
		if _, err := storage.RecordPushFailure(ctx, e.pool, subs[i], pushMaxFailures, pushFailureGrace, ok); err != nil {
			slog.Error("record push failure", "subscription", subs[i].ID, "err", err)
		}
	}
}

// deviceFault reports whether a push service answer means the subscription
// itself is broken (bad keys, a VAPID key it was not made with). No answer
// (status 0), 5xx, timeouts, rate limits and an oversized payload do not.
func deviceFault(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusRequestEntityTooLarge, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return status >= 400 && status < 500
}

// encodePushNotification marshals n so it fits in one push message. When it
// does not, it drops the link's query, then the body lines above the rule
// name, then the body, then shortens the title.
func encodePushNotification(n PushNotification) ([]byte, error) {
	shrink := []func(*PushNotification){
		func(n *PushNotification) { n.URL, _, _ = strings.Cut(n.URL, "?") },
		func(n *PushNotification) { n.Body = n.Body[strings.LastIndex(n.Body, "\n")+1:] },
		func(n *PushNotification) { n.Body = "" },
		func(n *PushNotification) { n.Title = truncateRunes(n.Title, 60) },
	}
	for i := 0; ; i++ {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		// The service worker reads this as JSON, never as HTML, so "<" and
		// friends need not grow into six-byte escapes.
		enc.SetEscapeHTML(false)
		if err := enc.Encode(n); err != nil {
			return nil, err
		}
		b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		if len(b) <= webpush.MaxPayload {
			return b, nil
		}
		if i == len(shrink) {
			return nil, fmt.Errorf("notification is %d bytes, limit is %d", len(b), webpush.MaxPayload)
		}
		shrink[i](&n)
	}
}

// buildPushNotification turns an alert into a short notification. Links are
// paths so the service worker opens them on the instance's own origin.
func buildPushNotification(p AlertPayload) PushNotification {
	subject := oneLine(strings.TrimPrefix(buildAlertSubject(p), "[Tindra] "))
	n := PushNotification{
		Title: truncateRunes(subject, 200),
		URL:   pushTargetPath(p),
	}
	if test, _ := p.Details["test"].(bool); test {
		n.Title = "Test: " + truncateRunes(oneLine(p.RuleName), 200)
		n.Body = "This rule delivers to this device."
		return n
	}

	var lines []string
	switch {
	case len(p.Issues) > 0:
		for _, iss := range p.Issues {
			if iss.ShortID != "" {
				lines = append(lines, iss.ShortID+" "+iss.Title)
			} else {
				lines = append(lines, iss.Title)
			}
		}
	case len(p.UptimeMonitors) > 0:
		for _, m := range p.UptimeMonitors {
			lines = append(lines, m.Name)
		}
	case len(p.Monitors) > 0:
		for _, m := range p.Monitors {
			lines = append(lines, m.Name)
		}
	}
	// A single issue or monitor may already be named in the subject, in the
	// form the subject uses (singleIssueTitle cuts long issue titles).
	if len(lines) == 1 && strings.Contains(subject, oneLine(subjectName(p))) {
		lines = nil
	}
	const maxLines = 3
	if len(lines) > maxLines {
		extra := len(lines) - maxLines
		lines = append(lines[:maxLines], fmt.Sprintf("and %d more", extra))
	}
	// Names may hold line breaks (multi-line exception messages); one name
	// is one line, so the line cap and the shrinking below hold.
	for i, l := range lines {
		lines[i] = truncateRunes(oneLine(l), 120)
	}
	lines = append(lines, "Rule: "+truncateRunes(oneLine(p.RuleName), 200))
	n.Body = strings.Join(lines, "\n")
	return n
}

func pushTargetPath(p AlertPayload) string {
	if test, _ := p.Details["test"].(bool); test {
		return "/alerts"
	}
	switch p.Trigger {
	case "uptime_down", "uptime_recovered":
		return monitorsViewURL("", "uptime", uptimeProjectIDs(p.UptimeMonitors))
	case "cron_missed", "cron_error":
		return monitorsViewURL("", "cron", cronProjectIDs(p.Monitors))
	case "log_count":
		return logsViewURL("", p)
	}
	if len(p.Issues) == 1 {
		return "/issues/" + p.Issues[0].ID
	}
	return "/issues"
}

// oneLine replaces line breaks and tabs with spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// subjectName is the form in which buildAlertSubject would name the
// payload's first issue or monitor.
func subjectName(p AlertPayload) string {
	switch {
	case len(p.Issues) > 0:
		return singleIssueTitle(p)
	case len(p.UptimeMonitors) > 0:
		return p.UptimeMonitors[0].Name
	case len(p.Monitors) > 0:
		return p.Monitors[0].Name
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}
