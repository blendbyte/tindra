package api

import (
	"encoding/json"
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

// handleCreatePushSubscription accepts the JSON form of a browser
// PushSubscription plus an optional device label.
func (ro *router) handleCreatePushSubscription(w http.ResponseWriter, r *http.Request) {
	if ro.evaluator.PushPublicKey() == "" {
		http.Error(w, "web push not configured", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sub := webpush.Subscription{Endpoint: body.Endpoint, P256dh: body.Keys.P256dh, Auth: body.Keys.Auth}
	if err := webpush.ValidateSubscription(sub); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Push services are public. Checking here gives a clear error up front;
	// delivery checks the resolved address again.
	if err := alerts.ValidateWebhookURL(r.Context(), sub.Endpoint, ro.webhookAllowPrivateIPs); err != nil {
		http.Error(w, "endpoint: "+err.Error(), http.StatusBadRequest)
		return
	}

	userID, _ := r.Context().Value(ctxUserID).(string)
	existing, err := storage.ListPushSubscriptions(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("list push subscriptions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	known := false
	for _, s := range existing {
		if s.Endpoint == sub.Endpoint {
			known = true
			break
		}
	}
	if !known && len(existing) >= maxPushDevices {
		http.Error(w, "too many devices registered; remove one first", http.StatusBadRequest)
		return
	}

	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = deviceLabel(r.UserAgent())
	}
	if r := []rune(label); len(r) > maxPushLabelLen {
		label = string(r[:maxPushLabelLen])
	}

	saved, err := storage.UpsertPushSubscription(r.Context(), ro.pool, &storage.PushSubscription{
		UserID: userID, Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth, Label: label,
	})
	if err != nil {
		slog.Error("save push subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !known {
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "push_device.added",
			ActorID:   actorFromContext(r.Context()),
			TargetID:  &saved.ID,
			IP:        r.RemoteAddr,
			Details:   map[string]any{"label": saved.Label},
		})
	}
	writeJSONStatus(w, http.StatusCreated, saved)
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
// endpoint. The SPA calls it when a device turns push off or logs out, when
// it knows the endpoint but not the stored ID.
func (ro *router) handleUnsubscribePush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID, _ := r.Context().Value(ctxUserID).(string)
	if _, err := storage.DeletePushSubscriptionByEndpoint(r.Context(), ro.pool, body.Endpoint, userID); err != nil {
		slog.Error("unsubscribe push", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
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
func deviceLabel(ua string) string {
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
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
