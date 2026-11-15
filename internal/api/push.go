package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/webpush"
)

// maxPushDevices caps how many devices one user can register.
const maxPushDevices = 20

const maxPushLabelLen = 100

// handleGetPushConfig tells the SPA whether push works and which VAPID key
// to subscribe with.
func (ro *router) handleGetPushConfig(w http.ResponseWriter, r *http.Request) {
	key := ro.evaluator.PushPublicKey()
	writeJSON(w, map[string]any{"enabled": key != "", "public_key": key})
}

func (ro *router) handleListPushSubscriptions(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(ctxUserID).(string)
	subs, err := storage.ListPushSubscriptions(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("list push subscriptions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"subscriptions": subs})
}

// pushSubscriptionBody is the JSON form of a browser PushSubscription.
type pushSubscriptionBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// validPushSubscription checks a subscription the server will post to. It
// writes the error response and returns false when it is not acceptable.
// resolve adds a DNS check of the endpoint, for a clear error up front; a
// rotation skips it, because a resolver hiccup there would lose the device
// (the worker cannot ask again) and every send checks the address anyway.
func (ro *router) validPushSubscription(w http.ResponseWriter, r *http.Request, b pushSubscriptionBody, resolve bool) (webpush.Subscription, bool) {
	sub := webpush.Subscription{Endpoint: b.Endpoint, P256dh: b.Keys.P256dh, Auth: b.Keys.Auth}
	if err := webpush.ValidateSubscription(sub); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return sub, false
	}
	// The server posts to registered endpoints, so only known push services
	// are accepted.
	if !ro.evaluator.PushEndpointAllowed(sub.Endpoint) {
		http.Error(w, "endpoint is not a known push service", http.StatusBadRequest)
		return sub, false
	}
	// Push services are public. Checking here gives a clear error up front;
	// delivery checks the resolved address again.
	if !resolve {
		return sub, true
	}
	if err := alerts.ValidateWebhookURL(r.Context(), sub.Endpoint, ro.webhookAllowPrivateIPs); err != nil {
		http.Error(w, "endpoint: "+strings.Replace(err.Error(), "webhook URL", "push endpoint", 1), http.StatusBadRequest)
		return sub, false
	}
	return sub, true
}

// handleCreatePushSubscription registers this browser for the signed-in
// user: the JSON form of a browser PushSubscription plus an optional label.
func (ro *router) handleCreatePushSubscription(w http.ResponseWriter, r *http.Request) {
	if ro.evaluator.PushPublicKey() == "" {
		http.Error(w, "web push not configured", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		pushSubscriptionBody
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sub, ok := ro.validPushSubscription(w, r, body.pushSubscriptionBody, true)
	if !ok {
		return
	}

	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = deviceLabel(r.UserAgent())
	}
	if r := []rune(label); len(r) > maxPushLabelLen {
		label = string(r[:maxPushLabelLen])
	}

	userID, _ := r.Context().Value(ctxUserID).(string)
	opts := storage.PushUpsertOptions{MaxDevices: maxPushDevices}
	if c, err := r.Cookie("tindra_session"); err == nil {
		opts.SessionToken = c.Value
	}
	res, err := storage.UpsertPushSubscription(r.Context(), ro.pool, &storage.PushSubscription{
		UserID: userID, Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth, Label: label,
	}, opts)
	switch {
	case errors.Is(err, storage.ErrPushSessionEnded):
		http.Error(w, "session ended", http.StatusUnauthorized)
		return
	case errors.Is(err, storage.ErrPushEndpointTaken):
		http.Error(w, "endpoint is registered to another user", http.StatusConflict)
		return
	case errors.Is(err, storage.ErrTooManyPushDevices):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		slog.Error("save push subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	saved := res.Subscription
	if res.Created {
		details := map[string]any{"label": saved.Label}
		if res.PreviousOwner != "" {
			// Same browser, another account: the previous owner loses it.
			details["previous_user_id"] = res.PreviousOwner
		}
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "push_device.added",
			ActorID:   actorFromContext(r.Context()),
			TargetID:  &saved.ID,
			IP:        r.RemoteAddr,
			Details:   details,
		})
	}
	writeJSONStatus(w, http.StatusCreated, saved)
}

// handleRotatePushSubscription moves a device to the subscription its push
// service rotated to. The service worker calls it, possibly long after the
// session ended, so it needs no session: the old subscription's auth secret,
// which only that browser and this server know, proves it is the device.
func (ro *router) handleRotatePushSubscription(w http.ResponseWriter, r *http.Request) {
	if ro.evaluator.PushPublicKey() == "" {
		http.Error(w, "web push not configured", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		pushSubscriptionBody
		Replaces     string `json:"replaces"`
		ReplacesAuth string `json:"replaces_auth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Replaces == "" || body.ReplacesAuth == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sub, ok := ro.validPushSubscription(w, r, body.pushSubscriptionBody, false)
	if !ok {
		return
	}
	saved, err := storage.ReplacePushSubscription(r.Context(), ro.pool, body.Replaces, body.ReplacesAuth, &storage.PushSubscription{
		Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth,
	})
	switch {
	case errors.Is(err, storage.ErrPushEndpointTaken):
		http.Error(w, "endpoint is registered to another user", http.StatusConflict)
		return
	case err != nil:
		slog.Error("rotate push subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	case saved == nil:
		// Removed from the device list, or the secret does not match: the
		// rotation must not bring a device back.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "push_device.rotated",
		TargetID:  &saved.ID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"label": saved.Label, "user_id": saved.UserID},
	})
	w.WriteHeader(http.StatusNoContent)
}

func (ro *router) handleDeletePushSubscription(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(ctxUserID).(string)
	id := chi.URLParam(r, "subscriptionID")
	deleted, err := storage.DeletePushSubscription(r.Context(), ro.pool, id, userID)
	if err != nil {
		slog.Error("delete push subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "push_device.removed",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &id,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleUnsubscribePush removes the caller's subscription for a browser
// endpoint. The SPA calls it when a device turns push off, when it knows the
// endpoint but not the stored ID. (Logout passes the endpoint to
// /api/auth/logout instead.)
func (ro *router) handleUnsubscribePush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID, _ := r.Context().Value(ctxUserID).(string)
	id, err := storage.DeletePushSubscriptionByEndpoint(r.Context(), ro.pool, body.Endpoint, userID)
	if err != nil {
		slog.Error("unsubscribe push", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if id != "" {
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "push_device.removed",
			ActorID:   actorFromContext(r.Context()),
			TargetID:  &id,
			IP:        r.RemoteAddr,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (ro *router) handleTestPushSubscription(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(ctxUserID).(string)
	sub, err := storage.GetPushSubscription(r.Context(), ro.pool, chi.URLParam(r, "subscriptionID"), userID)
	if err != nil {
		slog.Error("get push subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sub == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if ro.evaluator == nil {
		http.Error(w, "web push not configured", http.StatusServiceUnavailable)
		return
	}
	if err := ro.evaluator.SendTestPush(r.Context(), sub); err != nil {
		slog.Warn("test push failed", "subscription", sub.ID, "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deviceLabel names a device from its User-Agent, e.g. "Safari on iOS".
// iPadOS sends a Mac User-Agent; the app sends a label for it instead.
func deviceLabel(ua string) string {
	browser := "Browser"
	// Forks name themselves next to "Chrome/", so they come first.
	switch {
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgA/") || strings.Contains(ua, "EdgiOS/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/") || strings.Contains(ua, "OPiOS/"):
		browser = "Opera"
	case strings.Contains(ua, "SamsungBrowser/"):
		browser = "Samsung Internet"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(ua, "iPad"):
		os = "iPadOS"
	case strings.Contains(ua, "iPhone"):
		os = "iOS"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		os = "macOS"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " on " + os
}
