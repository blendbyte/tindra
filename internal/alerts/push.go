package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/webpush"
)

// pushMaxFailures is how many deliveries in a row may fail before a device
// is dropped. Gone answers (404, 410) drop it at once.
const pushMaxFailures = 5

// pushTTL is how long push services hold an alert for a device that is offline.
const pushTTL = 24 * time.Hour

const vapidSecretKey = "vapid_private_key"

// PushNotification is the JSON the service worker receives.
type PushNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// URL is a path on this instance, opened when the notification is clicked.
	URL string `json:"url"`
	// Tag groups notifications of one rule so a newer one replaces the older.
	Tag string `json:"tag"`
}

// LoadPushSender sets up Web Push with the instance's VAPID keys.
// VAPID_PRIVATE_KEY overrides the key generated and stored on first start;
// VAPID_SUBJECT overrides the contact derived from PUBLIC_URL.
func LoadPushSender(ctx context.Context, pool *pgxpool.Pool, publicURL string, allowPrivateIPs bool) (*webpush.Sender, error) {
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
	return &webpush.Sender{
		Keys:    keys,
		Subject: vapidSubject(os.Getenv("VAPID_SUBJECT"), publicURL),
		Client:  NewWebhookClient(allowPrivateIPs),
	}, nil
}

// vapidSubject picks the contact push services can reach the operator at.
// They require a mailto: or https: URI.
func vapidSubject(explicit, publicURL string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if u, err := url.Parse(publicURL); err == nil && u.Scheme == "https" && u.Host != "" {
		return "https://" + u.Host
	}
	return "mailto:webpush@localhost"
}

// SetPushSender enables the push channel. Without it push rules fail with a
// clear error instead of silently doing nothing.
func (e *Evaluator) SetPushSender(s *webpush.Sender) { e.push = s }

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
	body, err := json.Marshal(PushNotification{
		Title: "Tindra test notification",
		Body:  "Push notifications work on this device.",
		URL:   "/settings/profile",
		Tag:   "tindra-test",
	})
	if err != nil {
		return err
	}
	return e.sendPush(ctx, sub, webpush.Message{Payload: body, TTL: time.Minute, Urgency: "high"})
}

// firePush notifies every device of every recipient. It only fails when no
// device got the alert: a retry would otherwise notify the ones that did twice.
func (e *Evaluator) firePush(ctx context.Context, rule *storage.AlertRule, payload AlertPayload) error {
	if e.push == nil {
		return errors.New("web push not configured")
	}
	subs, err := storage.ListPushSubscriptionsForRule(ctx, e.pool, rule.ID)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return errors.New("no recipient has a device registered for push notifications")
	}
	body, err := json.Marshal(buildPushNotification(payload))
	if err != nil {
		return err
	}
	msg := webpush.Message{Payload: body, TTL: pushTTL, Urgency: "high", Topic: webpush.TopicFor(rule.ID)}

	var errs []error
	delivered := 0
	for _, sub := range subs {
		if err := e.sendPush(ctx, sub, msg); err != nil {
			errs = append(errs, err)
			continue
		}
		delivered++
	}
	if delivered == 0 {
		return fmt.Errorf("push failed on all %d devices: %w", len(subs), errors.Join(errs...))
	}
	if len(errs) > 0 {
		slog.Warn("push delivery failed on some devices", "rule", rule.ID, "delivered", delivered, "failed", len(errs), "err", errors.Join(errs...))
	}
	return nil
}

// sendPush delivers to one device and keeps its bookkeeping: success resets
// the failure count, Gone deletes it, other failures count toward removal.
func (e *Evaluator) sendPush(ctx context.Context, sub *storage.PushSubscription, msg webpush.Message) error {
	res, err := e.push.Send(ctx, webpush.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, msg)
	// Bookkeeping must not be skipped because the request's context ended.
	bookkeeping := context.WithoutCancel(ctx)
	if err == nil {
		if err := storage.RecordPushSuccess(bookkeeping, e.pool, sub.ID); err != nil {
			slog.Error("record push success", "subscription", sub.ID, "err", err)
		}
		return nil
	}
	if res.Gone {
		if _, derr := storage.ExpirePushSubscription(bookkeeping, e.pool, sub.ID); derr != nil {
			slog.Error("expire push subscription", "subscription", sub.ID, "err", derr)
		}
		return fmt.Errorf("device %q unsubscribed and was removed", sub.Label)
	}
	if _, ferr := storage.RecordPushFailure(bookkeeping, e.pool, sub.ID, pushMaxFailures); ferr != nil {
		slog.Error("record push failure", "subscription", sub.ID, "err", ferr)
	}
	return fmt.Errorf("device %q: %w", sub.Label, err)
}

// buildPushNotification turns an alert into a short notification. Links are
// paths so the service worker opens them on the instance's own origin.
func buildPushNotification(p AlertPayload) PushNotification {
	n := PushNotification{
		Title: truncateRunes(strings.TrimPrefix(buildAlertSubject(p), "[Tindra] "), 200),
		URL:   pushTargetPath(p),
		Tag:   "rule-" + p.RuleID,
	}
	if test, _ := p.Details["test"].(bool); test {
		n.Title = "Test: " + truncateRunes(p.RuleName, 200)
		n.Body = "This rule delivers to this device."
		return n
	}

	var lines []string
	switch {
	case len(p.Issues) > 0:
		for _, iss := range p.Issues {
			lines = append(lines, iss.Title)
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
	// A single issue or monitor is already named in the title.
	if len(lines) == 1 && strings.Contains(n.Title, lines[0]) {
		lines = nil
	}
	const maxLines = 3
	if len(lines) > maxLines {
		extra := len(lines) - maxLines
		lines = append(lines[:maxLines], fmt.Sprintf("and %d more", extra))
	}
	for i, l := range lines {
		lines[i] = truncateRunes(l, 120)
	}
	lines = append(lines, "Rule: "+truncateRunes(p.RuleName, 200))
	n.Body = strings.Join(lines, "\n")
	return n
}

func pushTargetPath(p AlertPayload) string {
	switch p.Trigger {
	case "uptime_down", "uptime_recovered":
		return monitorsViewURL("", "uptime", uptimeProjectIDs(p.UptimeMonitors))
	case "cron_missed", "cron_error":
		return monitorsViewURL("", "cron", cronProjectIDs(p.Monitors))
	case "log_count":
		return logsViewURL("", p)
	}
	if test, _ := p.Details["test"].(bool); test {
		return "/alerts"
	}
	if len(p.Issues) == 1 {
		return "/issues/" + p.Issues[0].ID
	}
	return "/issues"
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}
