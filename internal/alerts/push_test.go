package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/webpush"
	"github.com/blendbyte/tindra/internal/webpush/webpushtest"
)

// pushService records what was posted per path and answers with the status
// configured for that path (201 by default).
type pushService struct {
	*httptest.Server
	mu       sync.Mutex
	bodies   map[string][]byte
	statuses map[string]int
	hits     map[string]int
}

func newPushService(t *testing.T) *pushService {
	ps := &pushService{bodies: map[string][]byte{}, statuses: map[string]int{}, hits: map[string]int{}}
	ps.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		defer ps.mu.Unlock()
		ps.bodies[r.URL.Path] = body
		ps.hits[r.URL.Path]++
		status := ps.statuses[r.URL.Path]
		if status == 0 {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pushService) body(path string) []byte {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.bodies[path]
}

func pushEvaluator(t *testing.T, ps *pushService) *Evaluator {
	t.Helper()
	keys, err := webpush.GenerateKeys()
	require.NoError(t, err)
	e := testEvaluator(nil)
	e.SetPushSender(&webpush.Sender{Keys: keys, Subject: "mailto:ops@example.com", Client: ps.Client()})
	return e
}

type pushDevice struct {
	sub  *webpushtest.Subscriber
	row  *storage.PushSubscription
	path string
}

func addPushDevice(t *testing.T, ps *pushService, userID, path string) pushDevice {
	t.Helper()
	s := webpushtest.NewSubscriber()
	res, err := storage.UpsertPushSubscription(context.Background(), testPool, &storage.PushSubscription{
		UserID: userID, Endpoint: ps.URL + path, P256dh: s.P256dh(), Auth: s.Auth(), Label: "device " + path,
	}, storage.PushUpsertOptions{})
	require.NoError(t, err)
	return pushDevice{sub: s, row: res.Subscription, path: path}
}

func pushUser(t *testing.T, email string) *storage.User {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `DELETE FROM users WHERE email = $1`, email)
	require.NoError(t, err)
	u, err := storage.CreateUser(ctx, testPool, email, "password123456")
	require.NoError(t, err)
	return u
}

func pushRule(t *testing.T, recipients ...string) *storage.AlertRule {
	t.Helper()
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "push rule", Enabled: true,
		Trigger: "new_issue", Channel: "push", RecipientIDs: recipients, CooldownMins: 60,
	})
	require.NoError(t, err)
	return rule
}

func decodeNotification(t *testing.T, d pushDevice, body []byte) PushNotification {
	t.Helper()
	plain, err := d.sub.Decrypt(body)
	require.NoError(t, err)
	var n PushNotification
	require.NoError(t, json.Unmarshal(plain, &n))
	return n
}

func TestFirePush_deliversToAllRecipientDevices(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	alice := pushUser(t, "push-alice@example.com")
	bob := pushUser(t, "push-bob@example.com")
	outsider := pushUser(t, "push-outsider@example.com")
	a1 := addPushDevice(t, ps, alice.ID, "/alice-phone")
	a2 := addPushDevice(t, ps, alice.ID, "/alice-laptop")
	b1 := addPushDevice(t, ps, bob.ID, "/bob-phone")
	addPushDevice(t, ps, outsider.ID, "/outsider")
	rule := pushRule(t, alice.ID, bob.ID)

	payload := AlertPayload{
		RuleID: rule.ID, RuleName: rule.Name, Trigger: "new_issue", ProjectName: "Shop",
		Details: map[string]any{"new_issue_count": 1},
		Issues:  []*storage.Issue{{ID: "iss-1", Title: "TypeError: x is undefined"}},
	}
	status, err := e.deliver(context.Background(), rule, payload)
	require.NoError(t, err)
	require.Nil(t, status)

	for _, d := range []pushDevice{a1, a2, b1} {
		n := decodeNotification(t, d, ps.body(d.path))
		require.Equal(t, "Shop - New issue: TypeError: x is undefined", n.Title)
		require.Equal(t, "Rule: push rule", n.Body, "single issue is not repeated in the body")
		require.Equal(t, "/issues/iss-1", n.URL)
		require.Empty(t, n.Tag, "no alert may replace another on the device")

		got, err := storage.GetPushSubscription(context.Background(), testPool, d.row.ID, d.row.UserID)
		require.NoError(t, err)
		require.NotNil(t, got.LastSuccessAt)
	}
	require.Nil(t, ps.body("/outsider"), "non-recipients get nothing")
}

func TestFirePush_partialFailureRetriesOnlyMissedDevices(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-partial@example.com")
	ok := addPushDevice(t, ps, u.ID, "/ok")
	gone := addPushDevice(t, ps, u.ID, "/gone")
	flaky := addPushDevice(t, ps, u.ID, "/flaky")
	broken := addPushDevice(t, ps, u.ID, "/broken")
	ps.statuses["/gone"] = http.StatusGone
	ps.statuses["/flaky"] = http.StatusServiceUnavailable
	ps.statuses["/broken"] = http.StatusBadRequest
	rule := pushRule(t, u.ID)

	payload := AlertPayload{RuleID: rule.ID, RuleName: rule.Name, Trigger: "new_issue", Details: map[string]any{}}
	_, err := e.deliver(context.Background(), rule, payload)
	require.ErrorContains(t, err, "push failed on 3 of 4 devices (unsubscribed on 1, push service returned 503 on 1, push service returned 400 on 1)")
	require.ErrorIs(t, err, errPartlyDelivered, "a retry is scheduled for the rest; the alert did get through")
	require.NotContains(t, err.Error(), "device /", "the firing log names no device")
	require.NotNil(t, ps.body(ok.path))
	// The payload stored for the retry names only the devices that missed it
	// and can still get it.
	require.ElementsMatch(t, []string{flaky.row.ID}, payload.Details[pushRetryDevicesKey],
		"only a retry can help the device behind an outage; the rejected one fails again")

	// The retry (after a JSON round trip, as the evaluator stores it) goes
	// to those only, so nobody gets the alert twice.
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var stored AlertPayload
	require.NoError(t, json.Unmarshal(raw, &stored))
	ps.statuses["/flaky"] = http.StatusCreated
	ps.statuses["/broken"] = http.StatusCreated
	_, err = e.deliver(context.Background(), rule, stored)
	require.NoError(t, err)
	require.Equal(t, 1, ps.hits[ok.path])
	require.Equal(t, 2, ps.hits[flaky.path])
	require.Equal(t, 1, ps.hits[broken.path])
	require.Equal(t, 1, ps.hits[gone.path])

	ctx := context.Background()
	got, err := storage.GetPushSubscription(ctx, testPool, gone.row.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, got, "410 removes the device")
}

func TestFirePush_retryWithNothingLeftIsDelivered(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-nothing-left@example.com")
	addPushDevice(t, ps, u.ID, "/ok")
	rule := pushRule(t, u.ID)
	// The device the retry was for was removed meanwhile.
	gone := []any{"00000000-0000-0000-0000-000000000000"}
	payload := AlertPayload{RuleID: rule.ID, Trigger: "new_issue",
		Details: map[string]any{pushRetryDevicesKey: gone, pushPartlyDeliveredKey: true}}
	_, err := e.deliver(context.Background(), rule, payload)
	require.NoError(t, err, "it reached others earlier")
	require.Zero(t, ps.hits["/ok"], "the devices that got it are not notified again")

	// Had it reached nobody, the alert failed.
	_, err = e.deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue",
		Details: map[string]any{pushRetryDevicesKey: gone}})
	require.ErrorIs(t, err, errNotRetryable)
}

func TestDeliver_retryDevicesNeverReachOtherChannels(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
	}))
	t.Cleanup(srv.Close)
	// The rule was a push rule when the retry was stored, a webhook now.
	rule := webhookRule(testProject.ID, srv.URL)
	payload := AlertPayload{RuleID: "r", Trigger: "new_issue",
		Details: map[string]any{"new_issue_count": 1, pushRetryDevicesKey: []any{"3f1c2a52-8a7e-4c7b-9a51-2b1f9d0e7c11"}}}
	_, err := testEvaluator(nil).deliver(context.Background(), rule, payload)
	require.NoError(t, err)
	require.NotContains(t, string(body), pushRetryDevicesKey)
	require.NotContains(t, string(body), "3f1c2a52")
}

func TestFireTest_pushDoesNotCountFailures(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-ruletest@example.com")
	addPushDevice(t, ps, u.ID, "/ok")
	d := addPushDevice(t, ps, u.ID, "/broken")
	ps.statuses["/broken"] = http.StatusBadRequest
	rule := pushRule(t, u.ID)
	for range 3 {
		_ = e.FireTest(ctx, rule)
	}
	got, err := storage.GetPushSubscription(ctx, testPool, d.row.ID, u.ID)
	require.NoError(t, err)
	require.Zero(t, got.FailureCount)
}

func TestFirePush_onlyGoneFailuresNeedNoRetry(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-onlygone@example.com")
	addPushDevice(t, ps, u.ID, "/ok")
	addPushDevice(t, ps, u.ID, "/gone")
	ps.statuses["/gone"] = http.StatusGone
	rule := pushRule(t, u.ID)
	_, err := e.deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
	require.NoError(t, err)
}

func TestFirePush_failureCounting(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-counting@example.com")
	addPushDevice(t, ps, u.ID, "/ok")
	flaky := addPushDevice(t, ps, u.ID, "/flaky")
	broken := addPushDevice(t, ps, u.ID, "/broken")
	ps.statuses["/flaky"] = http.StatusServiceUnavailable
	ps.statuses["/broken"] = http.StatusBadRequest
	rule := pushRule(t, u.ID)
	_, err := e.deliver(ctx, rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
	require.Error(t, err, "the outage is retried")

	got, err := storage.GetPushSubscription(ctx, testPool, flaky.row.ID, u.ID)
	require.NoError(t, err)
	require.Zero(t, got.FailureCount, "a push service outage says nothing about the device")
	got, err = storage.GetPushSubscription(ctx, testPool, broken.row.ID, u.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.FailureCount, "a rejected subscription counts against the device")
}

func TestFirePush_allFailedReturnsError(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-allfail@example.com")
	down := addPushDevice(t, ps, u.ID, "/down")
	limited := addPushDevice(t, ps, u.ID, "/limited")
	ps.statuses["/down"] = http.StatusInternalServerError
	ps.statuses["/limited"] = http.StatusTooManyRequests
	rule := pushRule(t, u.ID)

	// An outage across several alerts and their retries keeps every device.
	for range 3 * pushMaxFailures {
		_, err := e.deliver(ctx, rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
		require.ErrorContains(t, err, "push failed on 2 of 2 devices")
		require.NotErrorIs(t, err, errPartlyDelivered)
	}
	for _, d := range []pushDevice{down, limited} {
		got, err := storage.GetPushSubscription(ctx, testPool, d.row.ID, u.ID)
		require.NoError(t, err)
		require.NotNil(t, got, d.path)
		require.Zero(t, got.FailureCount, d.path)
	}
}

func TestFirePush_dropsRejectedDeviceAfterGracePeriod(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-rejected@example.com")
	d := addPushDevice(t, ps, u.ID, "/rejected")
	addPushDevice(t, ps, u.ID, "/fine") // the push service itself works
	ps.statuses["/rejected"] = http.StatusForbidden
	rule := pushRule(t, u.ID)
	fire := func() {
		_, err := e.deliver(ctx, rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
		require.NoError(t, err, "a rejected device is not retried; its failures show in the device list")
	}

	// A burst of failures (an alert and its retries) does not remove it.
	for range pushMaxFailures + 2 {
		fire()
	}
	got, err := storage.GetPushSubscription(ctx, testPool, d.row.ID, u.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, pushMaxFailures+2, got.FailureCount)

	// Failing for longer than the grace period does.
	_, err = testPool.Exec(ctx, `UPDATE push_subscriptions SET failing_since = NOW() - interval '4 days' WHERE id = $1`, d.row.ID)
	require.NoError(t, err)
	fire()
	got, err = storage.GetPushSubscription(ctx, testPool, d.row.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestFirePush_instanceProblemRemovesNoDevices(t *testing.T) {
	// Every device of a push service is rejected and none got anything
	// lately: a bad VAPID setup on this side, not broken devices.
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-badvapid@example.com")
	a := addPushDevice(t, ps, u.ID, "/a")
	b := addPushDevice(t, ps, u.ID, "/b")
	ps.statuses["/a"] = http.StatusForbidden
	ps.statuses["/b"] = http.StatusUnauthorized
	_, err := testPool.Exec(ctx, `UPDATE push_subscriptions SET failing_since = NOW() - interval '30 days', failure_count = 50 WHERE id = ANY($1::uuid[])`, []string{a.row.ID, b.row.ID})
	require.NoError(t, err)
	rule := pushRule(t, u.ID)

	for range pushMaxFailures {
		_, err := e.deliver(ctx, rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
		require.ErrorContains(t, err, "push failed on 2 of 2 devices")
	}
	for _, d := range []pushDevice{a, b} {
		got, err := storage.GetPushSubscription(ctx, testPool, d.row.ID, u.ID)
		require.NoError(t, err)
		require.NotNil(t, got, d.path)
		require.Equal(t, 50+pushMaxFailures, got.FailureCount, "counted, so the device list shows it, but never removed")
	}
}

func TestSendTestPush_failuresDoNotCount(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-testbutton@example.com")
	d := addPushDevice(t, ps, u.ID, "/d")
	ok := addPushDevice(t, ps, u.ID, "/ok")
	require.NoError(t, e.SendTestPush(ctx, ok.row)) // the push service is healthy
	ps.statuses["/d"] = http.StatusBadRequest
	for range 2 * pushMaxFailures {
		require.Error(t, e.SendTestPush(ctx, d.row))
	}
	got, err := storage.GetPushSubscription(ctx, testPool, d.row.ID, u.ID)
	require.NoError(t, err)
	require.Zero(t, got.FailureCount)
}

func TestFirePush_rejectedEverywhereIsNotRetried(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-allrejected@example.com")
	addPushDevice(t, ps, u.ID, "/broken")
	ps.statuses["/broken"] = http.StatusBadRequest
	rule := pushRule(t, u.ID)
	_, err := e.deliver(ctx, rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
	require.ErrorIs(t, err, errNotRetryable, "the same answer would come again")

	// An outage on one of them makes it worth a retry, for that device only.
	down := addPushDevice(t, ps, u.ID, "/down")
	ps.statuses["/down"] = http.StatusServiceUnavailable
	payload := AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}}
	_, err = e.deliver(ctx, rule, payload)
	require.Error(t, err)
	require.NotErrorIs(t, err, errNotRetryable)
	require.Equal(t, []string{down.row.ID}, payload.Details[pushRetryDevicesKey])
}

func TestNotRetryable_keepsTheMessage(t *testing.T) {
	err := notRetryable(fmt.Errorf("push failed on all 1 devices: %w", errors.New("boom")))
	require.ErrorIs(t, err, errNotRetryable)
	require.Equal(t, "push failed on all 1 devices: boom", err.Error())
	require.NotContains(t, fmt.Errorf("wrapped: %w", err).Error(), "not retryable")
}

func TestFirePush_retryReachingNoneOfTheRestIsStillPartial(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-partial-retry@example.com")
	addPushDevice(t, ps, u.ID, "/ok")
	late := addPushDevice(t, ps, u.ID, "/late")
	ps.statuses["/late"] = http.StatusServiceUnavailable
	rule := pushRule(t, u.ID)
	payload := AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}}
	_, err := e.deliver(ctx, rule, payload)
	require.Error(t, err)
	require.Equal(t, true, payload.Details[pushPartlyDeliveredKey])

	// The retry finds the late device rejecting the subscription.
	ps.statuses["/late"] = http.StatusBadRequest
	_, err = e.deliver(ctx, rule, payload)
	require.NoError(t, err, "the alert reached a device on the first attempt")
	require.Equal(t, 2, ps.hits[late.path])
	require.Equal(t, 1, ps.hits["/ok"])
}

func TestLogFiring_partlyDeliveredEndsAsSuccess(t *testing.T) {
	ctx := context.Background()
	u := pushUser(t, "push-logpartial@example.com")
	rule := pushRule(t, u.ID)
	e := testEvaluator(nil)
	e.logFiring(ctx, rule.ID, "push", AlertPayload{RuleID: rule.ID, Trigger: "new_issue"}, maxDeliveryAttempts, nil,
		partlyDelivered(errors.New("push failed on 1 of 2 devices (timeout on 1)")))
	var status, msg string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT status, error FROM alert_firings WHERE rule_id = $1 ORDER BY fired_at DESC LIMIT 1`, rule.ID).Scan(&status, &msg))
	require.Equal(t, "success", status, "the alert got through; the rest is a note")
	require.Contains(t, msg, "1 of 2 devices")
	require.Equal(t, "failed", finalStatus(errors.New("x")))
}

func TestPushTargetPath_testsOpenTheAlertsPage(t *testing.T) {
	for _, trigger := range []string{"uptime_down", "cron_missed", "log_count", "new_issue"} {
		require.Equal(t, "/alerts", pushTargetPath(AlertPayload{Trigger: trigger, Details: map[string]any{"test": true}}), trigger)
	}
}

func TestLogFiring_notRetryableFailsAtOnce(t *testing.T) {
	ctx := context.Background()
	u := pushUser(t, "push-logfiring@example.com")
	rule := pushRule(t, u.ID)
	e := testEvaluator(nil)
	e.logFiring(ctx, rule.ID, "push", AlertPayload{RuleID: rule.ID, Trigger: "new_issue"}, 1, nil, fmt.Errorf("x: %w", errNotRetryable))
	var status string
	var next *time.Time
	require.NoError(t, testPool.QueryRow(ctx, `SELECT status, next_retry_at FROM alert_firings WHERE rule_id = $1 ORDER BY fired_at DESC LIMIT 1`, rule.ID).Scan(&status, &next))
	require.Equal(t, "failed", status)
	require.Nil(t, next)
}

func TestFirePush_removesDevicesOffTheAllowlist(t *testing.T) {
	ctx := context.Background()
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-offlist@example.com")
	d := addPushDevice(t, ps, u.ID, "/d")
	// The operator took the device's push service out of PUSH_EXTRA_HOSTS.
	e.push.Hosts = webpush.DefaultHosts
	rule := pushRule(t, u.ID)
	_, err := e.deliver(ctx, rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
	require.ErrorContains(t, err, "not an allowed push service")
	got, err := storage.GetPushSubscription(ctx, testPool, d.row.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, got)
	require.Zero(t, ps.hits["/d"])
}

func TestPushHosts(t *testing.T) {
	got := pushHosts(" https://Push.Example.org:8443/wpush , autopush.example.net:443,*.notify.example.com, ,fcm.googleapis.com")
	require.Equal(t, append(slices.Clone(webpush.DefaultHosts), "push.example.org", "autopush.example.net", "*.notify.example.com"), got)
}

func TestDeviceFault(t *testing.T) {
	for status, want := range map[int]bool{
		0: false, 400: true, 401: true, 403: true, 404: true, 408: false, 413: false,
		425: false, 429: false, 500: false, 502: false, 503: false,
	} {
		require.Equal(t, want, deviceFault(status), "status %d", status)
	}
}

func TestFirePush_sendsInParallel(t *testing.T) {
	// Every request waits until all of them have arrived, so sending one
	// device after the other would time out.
	const devices = 4
	arrived := make(chan struct{}, devices)
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		if len(arrived) == devices {
			once.Do(func() { close(release) })
		}
		select {
		case <-release:
			w.WriteHeader(http.StatusCreated)
		case <-time.After(5 * time.Second):
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(srv.Close)
	ps := &pushService{Server: srv}
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-parallel@example.com")
	for i := range devices {
		addPushDevice(t, ps, u.ID, fmt.Sprintf("/d%d", i))
	}
	rule := pushRule(t, u.ID)

	start := time.Now()
	_, err := e.deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 4*time.Second)
}

func TestFirePush_errorsWithoutSenderOrDevices(t *testing.T) {
	u := pushUser(t, "push-nodevice@example.com")
	rule := pushRule(t, u.ID)

	_, err := testEvaluator(nil).deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID})
	require.ErrorContains(t, err, "web push not configured")

	e := pushEvaluator(t, newPushService(t))
	_, err = e.deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID})
	require.ErrorContains(t, err, "no recipient has a device")
	require.ErrorIs(t, err, errNotRetryable, "a retry would find nobody either")
}

func TestFireTest_push(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-firetest@example.com")
	d := addPushDevice(t, ps, u.ID, "/test")
	rule := pushRule(t, u.ID)

	require.NoError(t, e.FireTest(context.Background(), rule))
	n := decodeNotification(t, d, ps.body(d.path))
	require.Equal(t, "Test: push rule", n.Title)
	require.Equal(t, "/alerts", n.URL)
}

func TestSendTestPush(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-device-test@example.com")
	d := addPushDevice(t, ps, u.ID, "/device")

	require.NoError(t, e.SendTestPush(context.Background(), d.row))
	n := decodeNotification(t, d, ps.body(d.path))
	require.Equal(t, "Tindra test notification", n.Title)

	require.ErrorContains(t, testEvaluator(nil).SendTestPush(context.Background(), d.row), "not configured")
}

func TestBuildPushNotification(t *testing.T) {
	many := AlertPayload{
		RuleID: "r1", RuleName: "errors", Trigger: "new_issue",
		Details: map[string]any{"new_issue_count": 5},
		Issues: []*storage.Issue{
			{ID: "1", Title: "A"}, {ID: "2", Title: "B"}, {ID: "3", Title: strings.Repeat("x", 300)},
			{ID: "4", Title: "D"}, {ID: "5", Title: "E"},
		},
	}
	n := buildPushNotification(many)
	require.Equal(t, "5 new issues", n.Title)
	lines := strings.Split(n.Body, "\n")
	require.Equal(t, []string{"A", "B", strings.Repeat("x", 117) + "...", "and 2 more", "Rule: errors"}, lines)
	require.Equal(t, "/issues", n.URL)

	uptime := buildPushNotification(AlertPayload{
		RuleID: "r2", RuleName: "site", Trigger: "uptime_down",
		Details:        map[string]any{"down_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{{Name: "shop.example.com", ProjectID: "p1"}},
	})
	require.Equal(t, "shop.example.com is down", uptime.Title)
	require.Equal(t, "/monitors/uptime?project_id=p1", uptime.URL)

	cron := buildPushNotification(AlertPayload{RuleName: "jobs", Trigger: "cron_missed",
		Details: map[string]any{"missed_count": 2}, Monitors: []*storage.CronMonitor{{Name: "backup"}, {Name: "billing"}}})
	require.Equal(t, "backup\nbilling\nRule: jobs", cron.Body)
	require.True(t, strings.HasPrefix(cron.URL, "/monitors/cron"))

	logs := buildPushNotification(AlertPayload{RuleName: "logs", Trigger: "log_count", ProjectID: "p9",
		Details: map[string]any{"log_count": 10, "window_mins": 5}})
	require.True(t, strings.HasPrefix(logs.URL, "/logs?"), logs.URL)

	huge := buildPushNotification(AlertPayload{RuleName: strings.Repeat("r", 5000), Trigger: "other", Details: map[string]any{}})
	b, err := json.Marshal(huge)
	require.NoError(t, err)
	require.Less(t, len(b), webpush.MaxPayload)

	// A long single issue title is cut in the subject; it is still not
	// repeated in the body.
	long := strings.Repeat("TypeError: something deep failed ", 10)
	single := buildPushNotification(AlertPayload{
		RuleID: "r3", RuleName: "errors", Trigger: "new_issue", ProjectName: "Shop",
		Details: map[string]any{"new_issue_count": 1},
		Issues:  []*storage.Issue{{ID: "9", Title: long}},
	})
	require.True(t, strings.HasPrefix(single.Title, "Shop - New issue: TypeError"), single.Title)
	require.Equal(t, "Rule: errors", single.Body)
}

func TestBuildPushNotification_oneLinePerName(t *testing.T) {
	n := buildPushNotification(AlertPayload{
		RuleID: "r", RuleName: "DB\nprod", Trigger: "new_issue",
		Details: map[string]any{"new_issue_count": 2},
		Issues:  []*storage.Issue{{ID: "1", Title: "Traceback:\n  File x\n    boom"}, {ID: "2", Title: "B"}},
	})
	require.Equal(t, []string{"Traceback: File x boom", "B", "Rule: DB prod"}, strings.Split(n.Body, "\n"))

	single := buildPushNotification(AlertPayload{
		RuleID: "r", RuleName: "errors", Trigger: "new_issue", ProjectName: "Shop",
		Details: map[string]any{"new_issue_count": 1},
		Issues:  []*storage.Issue{{ID: "1", Title: "Error:\nbad thing"}},
	})
	require.Equal(t, "Shop - New issue: Error: bad thing", single.Title)
	require.Equal(t, "Rule: errors", single.Body, "the issue is named in the title already")
}

func TestEncodePushNotification(t *testing.T) {
	decode := func(b []byte) PushNotification {
		var n PushNotification
		require.NoError(t, json.Unmarshal(b, &n))
		return n
	}

	// Markup is not escaped into \u003c sequences.
	b, err := encodePushNotification(PushNotification{Title: "<Foo> & <Bar>", URL: "/issues"})
	require.NoError(t, err)
	require.Contains(t, string(b), `"<Foo> & <Bar>"`)
	require.False(t, strings.HasSuffix(string(b), "\n"))

	// A log rule over many projects has a long link: the query goes first.
	ids := make([]string, 120)
	for i := range ids {
		ids[i] = fmt.Sprintf("3f1c2a52-8a7e-4c7b-9a51-%012d", i)
	}
	p := AlertPayload{RuleID: "r", RuleName: "logs", Trigger: "log_count",
		Details: map[string]any{"log_count": 10, "window_mins": 5, "project_ids": ids}}
	n := buildPushNotification(p)
	require.Greater(t, len(n.URL), webpush.MaxPayload)
	b, err = encodePushNotification(n)
	require.NoError(t, err)
	require.LessOrEqual(t, len(b), webpush.MaxPayload)
	got := decode(b)
	require.Equal(t, "/logs", got.URL)
	require.Equal(t, n.Body, got.Body)

	// Text that escapes badly loses the body lines, then the body, then title length.
	quotes := strings.Repeat(`"\`, 700) // 1400 runes, 2800 bytes once escaped
	bodyHeavy := PushNotification{Body: strings.Join([]string{quotes, quotes, "Rule: r"}, "\n"), URL: "/issues"}
	b, err = encodePushNotification(bodyHeavy)
	require.NoError(t, err)
	require.Equal(t, "Rule: r", decode(b).Body, "lines above the rule name go first")

	bodyHeavy.Body = "Rule: " + quotes + quotes
	b, err = encodePushNotification(bodyHeavy)
	require.NoError(t, err)
	require.Empty(t, decode(b).Body)

	worst := PushNotification{Title: strings.Repeat("\x01", 1000), Body: "Rule: " + quotes + quotes, URL: "/issues?x=1", Tag: "rule-x"}
	b, err = encodePushNotification(worst)
	require.NoError(t, err)
	require.LessOrEqual(t, len(b), webpush.MaxPayload)
	got = decode(b)
	require.Equal(t, "/issues", got.URL)
	require.Empty(t, got.Body)
	require.Len(t, []rune(got.Title), 60)
}

func TestVapidSubject(t *testing.T) {
	for explicit, want := range map[string]string{
		" mailto:me@example.com ":    "mailto:me@example.com",
		"https://status.example.com": "https://status.example.com",
	} {
		got, err := vapidSubject(explicit, "https://x.example.com")
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	for _, bad := range []string{"admin@example.com", "mailto:nobody", "http://example.com", "https://", "ops"} {
		_, err := vapidSubject(bad, "https://x.example.com")
		require.ErrorContains(t, err, "VAPID_SUBJECT", bad)
	}
	for publicURL, want := range map[string]string{
		"https://tindra.example.com/sub/": "https://tindra.example.com",
		"http://localhost:8080":           fallbackVapidSubject,
		"":                                fallbackVapidSubject,
	} {
		got, err := vapidSubject("", publicURL)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestLoadPushSender(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `DELETE FROM instance_secrets WHERE key = $1`, vapidSecretKey)
	require.NoError(t, err)
	t.Setenv("VAPID_PRIVATE_KEY", "")
	t.Setenv("VAPID_SUBJECT", "")

	t.Setenv("PUSH_EXTRA_HOSTS", " Push.Example.org , ,*.autopush.example.net,fcm.googleapis.com")
	first, err := LoadPushSender(ctx, testPool, "https://tindra.example.com")
	require.NoError(t, err)
	require.Equal(t, append(slices.Clone(webpush.DefaultHosts), "push.example.org", "*.autopush.example.net"), first.Hosts)
	require.Nil(t, first.Client, "the evaluator supplies the client")
	again, err := LoadPushSender(ctx, testPool, "https://tindra.example.com")
	require.NoError(t, err)
	require.Equal(t, first.Keys.Public, again.Keys.Public, "generated key persists")
	require.Equal(t, "https://tindra.example.com", first.Subject)

	override, err := webpush.GenerateKeys()
	require.NoError(t, err)
	t.Setenv("VAPID_PRIVATE_KEY", override.Private)
	t.Setenv("VAPID_SUBJECT", "mailto:ops@example.com")
	fromEnv, err := LoadPushSender(ctx, testPool, "")
	require.NoError(t, err)
	require.Equal(t, override.Public, fromEnv.Keys.Public)
	require.Equal(t, "mailto:ops@example.com", fromEnv.Subject)

	t.Setenv("VAPID_SUBJECT", "admin@example.com")
	_, err = LoadPushSender(ctx, testPool, "")
	require.ErrorContains(t, err, "VAPID_SUBJECT")

	t.Setenv("VAPID_SUBJECT", "")
	t.Setenv("VAPID_PRIVATE_KEY", "garbage")
	_, err = LoadPushSender(ctx, testPool, "")
	require.Error(t, err)

	var e *Evaluator
	require.Empty(t, e.PushPublicKey())
	e = testEvaluator(nil)
	require.Empty(t, e.PushPublicKey())
	require.False(t, e.PushEndpointAllowed("https://fcm.googleapis.com/fcm/send/x"), "push off allows nothing")
	e.SetPushSender(first)
	require.Equal(t, first.Keys.Public, e.PushPublicKey())
	require.Same(t, e.client, first.Client, "push shares the webhook client and its address checks")
	require.True(t, e.PushEndpointAllowed("https://fcm.googleapis.com/fcm/send/x"))
	require.True(t, e.PushEndpointAllowed("https://eu.autopush.example.net/x"))
	require.False(t, e.PushEndpointAllowed("https://internal.example.com/x"))
}
