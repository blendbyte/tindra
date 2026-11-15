package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/webpush"
	"github.com/blendbyte/tindra/internal/webpush/webpushtest"
)

type pushTestEnv struct {
	handler http.Handler
	service *httptest.Server
	sender  *webpush.Sender
	status  int
	reply   string
	hits    int
}

// newPushTestEnv builds a router whose evaluator sends push to a local fake
// push service. allowPrivate mirrors WEBHOOK_ALLOW_PRIVATE_IPS.
func newPushTestEnv(t *testing.T, allowPrivate bool) *pushTestEnv {
	t.Helper()
	env := &pushTestEnv{status: http.StatusCreated}
	env.service = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.hits++
		w.WriteHeader(env.status)
		_, _ = w.Write([]byte(env.reply))
	}))
	t.Cleanup(env.service.Close)

	keys, err := webpush.GenerateKeys()
	require.NoError(t, err)
	ev := alerts.NewEvaluator(testPool, nil, "", allowPrivate)
	// No Hosts: the fake push service runs on 127.0.0.1.
	env.sender = &webpush.Sender{Keys: keys, Subject: "mailto:ops@example.com", Client: env.service.Client()}
	ev.SetPushSender(env.sender)
	env.handler = api.NewRouter(api.Config{Pool: testPool, Events: ingest.NewBuffer(1), Evaluator: ev, WebhookAllowPrivateIPs: allowPrivate})
	return env
}

func truncatePushSubscriptions(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), "TRUNCATE push_subscriptions")
	require.NoError(t, err)
}

func pushRequest(t *testing.T, h http.Handler, cookie *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func subscriptionBody(endpoint string, s *webpushtest.Subscriber) map[string]any {
	return map[string]any{
		"endpoint":       endpoint,
		"expirationTime": nil,
		"keys":           map[string]string{"p256dh": s.P256dh(), "auth": s.Auth()},
	}
}

func otherUserCookie(t *testing.T, email string) (*storage.User, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `DELETE FROM users WHERE email = $1`, email)
	require.NoError(t, err)
	u, err := storage.CreateUser(ctx, testPool, email, "password123456")
	require.NoError(t, err)
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)
	return u, &http.Cookie{Name: "tindra_session", Value: sess.Token}
}

func TestPushConfig(t *testing.T) {
	rec := pushRequest(t, alertHandler(), authCookie(), http.MethodGet, "/api/push/config", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"enabled":false,"public_key":""}`, rec.Body.String())

	env := newPushTestEnv(t, true)
	rec = pushRequest(t, env.handler, authCookie(), http.MethodGet, "/api/push/config", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var cfg struct {
		Enabled   bool   `json:"enabled"`
		PublicKey string `json:"public_key"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cfg))
	require.True(t, cfg.Enabled)
	require.NotEmpty(t, cfg.PublicKey)
}

func TestPushRoutes_requireSession(t *testing.T) {
	env := newPushTestEnv(t, true)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/push/subscriptions"},
		{http.MethodPost, "/api/push/subscriptions"},
		{http.MethodPost, "/api/push/subscriptions/unsubscribe"},
		{http.MethodDelete, "/api/push/subscriptions/x"},
		{http.MethodPost, "/api/push/subscriptions/x/test"},
	} {
		req := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer tnd_whatever")
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", route.method, route.path)
	}

	// The VAPID public key is public: the service worker reads it without a
	// session when the push service rotates a subscription.
	rec := pushRequest(t, env.handler, nil, http.MethodGet, "/api/push/config", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"enabled":true`)
}

func TestCreatePushSubscription(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	s := webpushtest.NewSubscriber()
	endpoint := env.service.URL + "/push/1"

	rec := pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(endpoint, s))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), s.P256dh(), "keys are never returned")
	require.NotContains(t, rec.Body.String(), s.Auth())
	var created storage.PushSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.Equal(t, "Safari on iOS", created.Label)
	require.Equal(t, endpoint, created.Endpoint)

	// Registering again updates instead of duplicating; a client label wins.
	body := subscriptionBody(endpoint, s)
	body["label"] = "  Work phone  "
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", body)
	require.Equal(t, http.StatusCreated, rec.Code)
	var again storage.PushSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &again))
	require.Equal(t, created.ID, again.ID)
	require.Equal(t, "Work phone", again.Label)

	rec = pushRequest(t, env.handler, authCookie(), http.MethodGet, "/api/push/subscriptions", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Subscriptions []storage.PushSubscription `json:"subscriptions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Subscriptions, 1)
}

func TestCreatePushSubscription_validation(t *testing.T) {
	truncatePushSubscriptions(t)
	s := webpushtest.NewSubscriber()

	strict := newPushTestEnv(t, false)
	for name, body := range map[string]any{
		"http endpoint":    subscriptionBody("http://push.example.com/x", s),
		"private endpoint": subscriptionBody("https://127.0.0.1/x", s),
		"missing keys":     map[string]any{"endpoint": "https://push.example.com/x"},
	} {
		rec := pushRequest(t, strict.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader("{"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	strict.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Without a configured sender there is nothing to subscribe to.
	rec = pushRequest(t, alertHandler(), authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody("https://push.example.com/x", s))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestCreatePushSubscription_onlyKnownPushServices(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	env.sender.Hosts = webpush.DefaultHosts
	s := webpushtest.NewSubscriber()
	for _, endpoint := range []string{env.service.URL + "/x", "https://internal-service/admin", "https://example.com/push"} {
		rec := pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(endpoint, s))
		require.Equal(t, http.StatusBadRequest, rec.Code, endpoint)
		require.Contains(t, rec.Body.String(), "not a known push service")
	}
	require.Zero(t, env.hits)
}

func TestRotatePushSubscription(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	s := webpushtest.NewSubscriber()
	oldEndpoint := env.service.URL + "/old"

	rec := pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(oldEndpoint, s))
	require.Equal(t, http.StatusCreated, rec.Code)
	var created storage.PushSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	// The worker sends no cookie: the old auth secret is the proof.
	rotate := func(replaces, replacesAuth, endpoint string) *httptest.ResponseRecorder {
		body := subscriptionBody(endpoint, webpushtest.NewSubscriber())
		body["replaces"] = replaces
		body["replaces_auth"] = replacesAuth
		return pushRequest(t, env.handler, nil, http.MethodPost, "/api/push/rotate", body)
	}

	rec = rotate(oldEndpoint, webpushtest.NewSubscriber().Auth(), env.service.URL+"/stolen")
	require.Equal(t, http.StatusNotFound, rec.Code, "a wrong secret moves nothing")
	rec = pushRequest(t, env.handler, nil, http.MethodPost, "/api/push/rotate", subscriptionBody(env.service.URL+"/x", s))
	require.Equal(t, http.StatusBadRequest, rec.Code, "replaces and replaces_auth are required")

	rec = rotate(oldEndpoint, s.Auth(), env.service.URL+"/new")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	rec = pushRequest(t, env.handler, authCookie(), http.MethodGet, "/api/push/subscriptions", nil)
	var list struct {
		Subscriptions []storage.PushSubscription `json:"subscriptions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Subscriptions, 1)
	require.Equal(t, created.ID, list.Subscriptions[0].ID)
	require.Equal(t, env.service.URL+"/new", list.Subscriptions[0].Endpoint)

	// A device removed from the list is not brought back by a rotation.
	rec = pushRequest(t, env.handler, authCookie(), http.MethodDelete, "/api/push/subscriptions/"+created.ID, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = rotate(env.service.URL+"/new", s.Auth(), env.service.URL+"/newer")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Long endpoints (2048 bytes each, the most a subscription may have)
	// still fit the request.
	longOld := env.service.URL + "/" + strings.Repeat("o", 2000)
	ls := webpushtest.NewSubscriber()
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(longOld, ls))
	require.Equal(t, http.StatusCreated, rec.Code)
	rec = rotate(longOld, ls.Auth(), env.service.URL+"/"+strings.Repeat("n", 2000))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	// A rotation does not depend on DNS: a resolver hiccup would lose the
	// device, and every send checks the address anyway. Registering does.
	// (Only a strict instance resolves endpoints up front.)
	strict := newPushTestEnv(t, false)
	strict.sender.Hosts = []string{"*.invalid"}
	unresolvable := "https://push.tindra-test.invalid/new"
	rec = pushRequest(t, strict.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(unresolvable, webpushtest.NewSubscriber()))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "push endpoint")
	body := subscriptionBody(unresolvable, webpushtest.NewSubscriber())
	body["replaces"] = "https://push.tindra-test.invalid/old"
	body["replaces_auth"] = webpushtest.NewSubscriber().Auth()
	rec = pushRequest(t, strict.handler, nil, http.MethodPost, "/api/push/rotate", body)
	require.Equal(t, http.StatusNotFound, rec.Code, "past validation; only the unknown device stops it")

	// Endpoints outside the push services are refused here too.
	env.sender.Hosts = webpush.DefaultHosts
	rec = rotate(oldEndpoint, s.Auth(), "https://internal-service/admin")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreatePushSubscription_endpointOwnership(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	other, otherCookie := otherUserCookie(t, "push-owner-other@example.com")
	browser := webpushtest.NewSubscriber()
	endpoint := env.service.URL + "/shared-browser"

	rec := pushRequest(t, env.handler, otherCookie, http.MethodPost, "/api/push/subscriptions", subscriptionBody(endpoint, browser))
	require.Equal(t, http.StatusCreated, rec.Code)

	// Knowing the URL is not enough to take the device over.
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(endpoint, webpushtest.NewSubscriber()))
	require.Equal(t, http.StatusConflict, rec.Code)
	rec = pushRequest(t, env.handler, otherCookie, http.MethodGet, "/api/push/subscriptions", nil)
	require.Contains(t, rec.Body.String(), endpoint, "still the other user's")

	// The same browser (its keys) signing in as someone else moves it, and
	// the audit log names who lost it.
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(endpoint, browser))
	require.Equal(t, http.StatusCreated, rec.Code)
	var moved storage.PushSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &moved))
	rec = pushRequest(t, env.handler, otherCookie, http.MethodGet, "/api/push/subscriptions", nil)
	require.JSONEq(t, `{"subscriptions":[]}`, rec.Body.String())
	require.Eventually(t, func() bool {
		var previous string
		err := testPool.QueryRow(context.Background(), `SELECT details->>'previous_user_id' FROM audit_log
			WHERE event_type = 'push_device.added' AND target_id = $1 ORDER BY created_at DESC LIMIT 1`, moved.ID).Scan(&previous)
		return err == nil && previous == other.ID
	}, 5*time.Second, 20*time.Millisecond)
}

func TestChangePassword_keepsPushOnThisDevice(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	u, cookie := otherUserCookie(t, "push-password@example.com")
	ctx := context.Background()
	for _, path := range []string{"/here", "/laptop"} {
		rec := pushRequest(t, env.handler, cookie, http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+path, webpushtest.NewSubscriber()))
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	rec := pushRequest(t, env.handler, cookie, http.MethodPatch, "/api/me/password", map[string]string{
		"current_password": "password123456", "new_password": "a-brand-new-password", "keep_push_endpoint": env.service.URL + "/here",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	subs, err := storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Equal(t, env.service.URL+"/here", subs[0].Endpoint)
}

func TestLogout_removesThisPushDevice(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	u, cookie := otherUserCookie(t, "push-logout@example.com")
	for _, path := range []string{"/here", "/phone"} {
		rec := pushRequest(t, env.handler, cookie, http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+path, webpushtest.NewSubscriber()))
		require.Equal(t, http.StatusCreated, rec.Code)
	}
	rec := pushRequest(t, env.handler, cookie, http.MethodPost, "/api/auth/logout", map[string]string{"push_endpoint": env.service.URL + "/here"})
	require.Equal(t, http.StatusOK, rec.Code)
	subs, err := storage.ListPushSubscriptions(context.Background(), testPool, u.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1, "only the browser that signed out")
	require.Equal(t, env.service.URL+"/phone", subs[0].Endpoint)

	// Without a session nothing is removed, whatever endpoint is named.
	rec = pushRequest(t, env.handler, nil, http.MethodPost, "/api/auth/logout", map[string]string{"push_endpoint": env.service.URL + "/phone"})
	require.Equal(t, http.StatusOK, rec.Code)
	subs, err = storage.ListPushSubscriptions(context.Background(), testPool, u.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
}

func TestCreatePushSubscription_deviceLimit(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	s := webpushtest.NewSubscriber()
	for i := range 20 {
		rec := pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(fmt.Sprintf("%s/d/%d", env.service.URL, i), s))
		require.Equal(t, http.StatusCreated, rec.Code)
	}
	rec := pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+"/d/new", s))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	// A device that is already registered can still refresh its keys.
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+"/d/3", s))
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestPushSubscription_ownershipDeleteAndUnsubscribe(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	_, otherCookie := otherUserCookie(t, "push-other@example.com")
	s := webpushtest.NewSubscriber()

	rec := pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+"/mine", s))
	require.Equal(t, http.StatusCreated, rec.Code)
	var mine storage.PushSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mine))

	rec = pushRequest(t, env.handler, otherCookie, http.MethodGet, "/api/push/subscriptions", nil)
	require.JSONEq(t, `{"subscriptions":[]}`, rec.Body.String())
	rec = pushRequest(t, env.handler, otherCookie, http.MethodDelete, "/api/push/subscriptions/"+mine.ID, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec = pushRequest(t, env.handler, otherCookie, http.MethodPost, "/api/push/subscriptions/"+mine.ID+"/test", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions/not-a-uuid/test", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec = pushRequest(t, env.handler, otherCookie, http.MethodPost, "/api/push/subscriptions/unsubscribe", map[string]string{"endpoint": mine.Endpoint})
	require.Equal(t, http.StatusNoContent, rec.Code)

	// The other user's unsubscribe did not touch it.
	rec = pushRequest(t, env.handler, authCookie(), http.MethodDelete, "/api/push/subscriptions/"+mine.ID, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+"/mine2", s))
	require.Equal(t, http.StatusCreated, rec.Code)
	var mine2 storage.PushSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mine2))
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions/unsubscribe", map[string]string{"endpoint": env.service.URL + "/mine2"})
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Eventually(t, func() bool {
		var n int
		err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log
			WHERE event_type = 'push_device.removed' AND target_id = $1`, mine2.ID).Scan(&n)
		return err == nil && n == 1
	}, 5*time.Second, 20*time.Millisecond, "turning push off is audited like removing a device")
	rec = pushRequest(t, env.handler, authCookie(), http.MethodGet, "/api/push/subscriptions", nil)
	require.JSONEq(t, `{"subscriptions":[]}`, rec.Body.String())

	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions/unsubscribe", map[string]string{})
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTestPushSubscription(t *testing.T) {
	truncatePushSubscriptions(t)
	env := newPushTestEnv(t, true)
	s := webpushtest.NewSubscriber()
	rec := pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+"/t", s))
	require.Equal(t, http.StatusCreated, rec.Code)
	var sub storage.PushSubscription
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sub))

	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions/"+sub.ID+"/test", nil)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Equal(t, 1, env.hits)

	// What the endpoint answers never reaches the user: endpoints are user
	// supplied, so echoing them would turn the test button into a proxy.
	env.status = http.StatusForbidden
	env.reply = "internal admin page"
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions/"+sub.ID+"/test", nil)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "push service returned 403")
	require.NotContains(t, rec.Body.String(), "internal admin page")

	env.status = http.StatusGone
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions/"+sub.ID+"/test", nil)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "unsubscribed")
	rec = pushRequest(t, env.handler, authCookie(), http.MethodGet, "/api/push/subscriptions", nil)
	require.JSONEq(t, `{"subscriptions":[]}`, rec.Body.String(), "gone device is removed")
}

func TestAlertRule_pushChannel(t *testing.T) {
	truncateAlertRules(t)
	h := alertHandler()
	base := map[string]any{"name": "push me", "trigger": "new_issue", "channel": "push"}

	rec := pushRequest(t, h, authCookie(), http.MethodPost, "/api/alert-rules", base)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "recipient_ids required")

	base["recipient_ids"] = []string{"00000000-0000-0000-0000-000000000000"}
	rec = pushRequest(t, h, authCookie(), http.MethodPost, "/api/alert-rules", base)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "existing users")

	base["recipient_ids"] = []string{testUser.ID}
	rec = pushRequest(t, h, authCookie(), http.MethodPost, "/api/alert-rules", base)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var rule storage.AlertRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	require.Equal(t, []string{testUser.ID}, rule.RecipientIDs)

	// Removing every recipient is rejected.
	rec = pushRequest(t, h, authCookie(), http.MethodPatch, "/api/alert-rules/"+rule.ID, map[string]any{"recipient_ids": []string{}})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Switching to another channel drops the recipients.
	rec = pushRequest(t, h, authCookie(), http.MethodPatch, "/api/alert-rules/"+rule.ID, map[string]any{"channel": "email", "email_to": "a@example.com"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var updated storage.AlertRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Empty(t, updated.RecipientIDs)
}

func TestAlertRule_pushRuleWithoutRecipientsCanBePaused(t *testing.T) {
	truncateAlertRules(t)
	h := alertHandler()
	gone, _ := otherUserCookie(t, "push-sole-recipient@example.com")
	rec := pushRequest(t, h, authCookie(), http.MethodPost, "/api/alert-rules", map[string]any{
		"name": "on-call", "trigger": "new_issue", "channel": "push", "enabled": true, "recipient_ids": []string{gone.ID},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var rule storage.AlertRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))

	// The only recipient is deleted: the rule is left without one.
	_, err := testPool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, gone.ID)
	require.NoError(t, err)

	rec = pushRequest(t, h, authCookie(), http.MethodPatch, "/api/alert-rules/"+rule.ID, map[string]any{"name": "renamed"})
	require.Equal(t, http.StatusBadRequest, rec.Code, "an enabled push rule still needs a recipient")
	rec = pushRequest(t, h, authCookie(), http.MethodPatch, "/api/alert-rules/"+rule.ID, map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = pushRequest(t, h, authCookie(), http.MethodPatch, "/api/alert-rules/"+rule.ID, map[string]any{"name": "paused, renamed"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = pushRequest(t, h, authCookie(), http.MethodPatch, "/api/alert-rules/"+rule.ID, map[string]any{"enabled": true})
	require.Equal(t, http.StatusBadRequest, rec.Code, "turning it back on needs a recipient")
	rec = pushRequest(t, h, authCookie(), http.MethodPatch, "/api/alert-rules/"+rule.ID, map[string]any{"enabled": true, "recipient_ids": []string{testUser.ID}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestAlertRules_recipientsHiddenFromTokens(t *testing.T) {
	truncateAlertRules(t)
	h := alertHandler()
	rec := pushRequest(t, h, authCookie(), http.MethodPost, "/api/alert-rules", map[string]any{
		"name": "on-call", "trigger": "new_issue", "channel": "push", "enabled": true, "recipient_ids": []string{testUser.ID},
		"project_ids": []string{testProject.ID},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var rule storage.AlertRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))

	// A session sees them.
	rec = pushRequest(t, h, authCookie(), http.MethodGet, "/api/alert-rules/"+rule.ID, nil)
	require.Contains(t, rec.Body.String(), testUser.ID)

	// A project token does not, in the list or the rule.
	truncateTokens(t)
	_, token, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "tok", false)
	require.NoError(t, err)
	for _, path := range []string{"/api/alert-rules", "/api/alert-rules/" + rule.ID} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Contains(t, rec.Body.String(), rule.ID, path)
		require.NotContains(t, rec.Body.String(), testUser.ID, path)
	}

	// The MCP tools never do.
	result := toolCall(t, mcpHandler(), "list_alerts", map[string]any{}, authCookie())
	require.NotContains(t, toolText(t, result), testUser.ID)

	// Tokens cannot write alert rules at all (requirePerm), so no create or
	// update answer ever reaches one.
	req := httptest.NewRequest(http.MethodPatch, "/api/alert-rules/"+rule.ID, strings.NewReader(`{"recipient_ids":[]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestMCP_createAlertRule_rejectsPush(t *testing.T) {
	truncateAlertRules(t)
	result := toolCall(t, mcpHandler(), "create_alert_rule", map[string]any{
		"name": "MCP push", "trigger": "new_issue", "channel": "push",
	}, authCookie())
	isErr, _ := result["isError"].(bool)
	require.True(t, isErr)
	require.Contains(t, toolText(t, result), "web UI")
}
