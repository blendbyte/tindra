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
	status  int
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
	}))
	t.Cleanup(env.service.Close)

	keys, err := webpush.GenerateKeys()
	require.NoError(t, err)
	ev := alerts.NewEvaluator(testPool, nil, "", allowPrivate)
	ev.SetPushSender(&webpush.Sender{Keys: keys, Subject: "mailto:ops@example.com", Client: env.service.Client()})
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
		{http.MethodGet, "/api/push/config"},
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
	rec = pushRequest(t, env.handler, otherCookie, http.MethodPost, "/api/push/subscriptions/unsubscribe", map[string]string{"endpoint": mine.Endpoint})
	require.Equal(t, http.StatusNoContent, rec.Code)

	// The other user's unsubscribe did not touch it.
	rec = pushRequest(t, env.handler, authCookie(), http.MethodDelete, "/api/push/subscriptions/"+mine.ID, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions", subscriptionBody(env.service.URL+"/mine2", s))
	require.Equal(t, http.StatusCreated, rec.Code)
	rec = pushRequest(t, env.handler, authCookie(), http.MethodPost, "/api/push/subscriptions/unsubscribe", map[string]string{"endpoint": env.service.URL + "/mine2"})
	require.Equal(t, http.StatusNoContent, rec.Code)
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
