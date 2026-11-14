package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
}

func newPushService(t *testing.T) *pushService {
	ps := &pushService{bodies: map[string][]byte{}, statuses: map[string]int{}}
	ps.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		defer ps.mu.Unlock()
		ps.bodies[r.URL.Path] = body
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
	row, err := storage.UpsertPushSubscription(context.Background(), testPool, &storage.PushSubscription{
		UserID: userID, Endpoint: ps.URL + path, P256dh: s.P256dh(), Auth: s.Auth(), Label: "device " + path,
	})
	require.NoError(t, err)
	return pushDevice{sub: s, row: row, path: path}
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
		require.Equal(t, "rule-"+rule.ID, n.Tag)

		got, err := storage.GetPushSubscription(context.Background(), testPool, d.row.ID, d.row.UserID)
		require.NoError(t, err)
		require.NotNil(t, got.LastSuccessAt)
	}
	require.Nil(t, ps.body("/outsider"), "non-recipients get nothing")
}

func TestFirePush_partialFailureIsNotRetried(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-partial@example.com")
	ok := addPushDevice(t, ps, u.ID, "/ok")
	gone := addPushDevice(t, ps, u.ID, "/gone")
	flaky := addPushDevice(t, ps, u.ID, "/flaky")
	ps.statuses["/gone"] = http.StatusGone
	ps.statuses["/flaky"] = http.StatusServiceUnavailable
	rule := pushRule(t, u.ID)

	_, err := e.deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID, RuleName: rule.Name, Trigger: "new_issue", Details: map[string]any{}})
	require.NoError(t, err, "one device got it, so the firing counts as delivered")
	require.NotNil(t, ps.body(ok.path))

	ctx := context.Background()
	got, err := storage.GetPushSubscription(ctx, testPool, gone.row.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, got, "410 removes the device")
	got, err = storage.GetPushSubscription(ctx, testPool, flaky.row.ID, u.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.FailureCount)
}

func TestFirePush_allFailedReturnsError(t *testing.T) {
	ps := newPushService(t)
	e := pushEvaluator(t, ps)
	u := pushUser(t, "push-allfail@example.com")
	d := addPushDevice(t, ps, u.ID, "/down")
	ps.statuses["/down"] = http.StatusInternalServerError
	rule := pushRule(t, u.ID)

	for i := 1; i <= pushMaxFailures; i++ {
		_, err := e.deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID, Trigger: "new_issue", Details: map[string]any{}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "push failed on all 1 devices")
	}
	got, err := storage.GetPushSubscription(context.Background(), testPool, d.row.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, got, "device is dropped after repeated failures")
}

func TestFirePush_errorsWithoutSenderOrDevices(t *testing.T) {
	u := pushUser(t, "push-nodevice@example.com")
	rule := pushRule(t, u.ID)

	_, err := testEvaluator(nil).deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID})
	require.ErrorContains(t, err, "web push not configured")

	e := pushEvaluator(t, newPushService(t))
	_, err = e.deliver(context.Background(), rule, AlertPayload{RuleID: rule.ID})
	require.ErrorContains(t, err, "no recipient has a device")
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
}

func TestVapidSubject(t *testing.T) {
	require.Equal(t, "mailto:me@example.com", vapidSubject(" mailto:me@example.com ", "https://x.example.com"))
	require.Equal(t, "https://tindra.example.com", vapidSubject("", "https://tindra.example.com/sub/"))
	require.Equal(t, "mailto:webpush@localhost", vapidSubject("", "http://localhost:8080"))
	require.Equal(t, "mailto:webpush@localhost", vapidSubject("", ""))
}

func TestLoadPushSender(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `DELETE FROM instance_secrets WHERE key = $1`, vapidSecretKey)
	require.NoError(t, err)
	t.Setenv("VAPID_PRIVATE_KEY", "")
	t.Setenv("VAPID_SUBJECT", "")

	first, err := LoadPushSender(ctx, testPool, "https://tindra.example.com", false)
	require.NoError(t, err)
	again, err := LoadPushSender(ctx, testPool, "https://tindra.example.com", false)
	require.NoError(t, err)
	require.Equal(t, first.Keys.Public, again.Keys.Public, "generated key persists")
	require.Equal(t, "https://tindra.example.com", first.Subject)

	override, err := webpush.GenerateKeys()
	require.NoError(t, err)
	t.Setenv("VAPID_PRIVATE_KEY", override.Private)
	t.Setenv("VAPID_SUBJECT", "mailto:ops@example.com")
	fromEnv, err := LoadPushSender(ctx, testPool, "", false)
	require.NoError(t, err)
	require.Equal(t, override.Public, fromEnv.Keys.Public)
	require.Equal(t, "mailto:ops@example.com", fromEnv.Subject)

	t.Setenv("VAPID_PRIVATE_KEY", "garbage")
	_, err = LoadPushSender(ctx, testPool, "", false)
	require.Error(t, err)

	var e *Evaluator
	require.Empty(t, e.PushPublicKey())
	e = testEvaluator(nil)
	require.Empty(t, e.PushPublicKey())
	e.SetPushSender(first)
	require.Equal(t, first.Keys.Public, e.PushPublicKey())
}
