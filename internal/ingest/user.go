package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SentryUser is the end-user attached to an event, transaction, or log via
// sentry_sdk.set_user(). Identity is COALESCE(id, username, email).
type SentryUser struct {
	Identity string
	ID       string
	Username string
	Email    string
	Name     string
}

// UserIdentity returns COALESCE(id, username, email), trimmed. Empty if none.
func UserIdentity(id, username, email string) string {
	for _, s := range []string{id, username, email} {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}

func (u SentryUser) withIdentity() SentryUser {
	u.ID = strings.TrimSpace(u.ID)
	u.Username = strings.TrimSpace(u.Username)
	u.Email = strings.TrimSpace(u.Email)
	u.Name = strings.TrimSpace(u.Name)
	u.Identity = UserIdentity(u.ID, u.Username, u.Email)
	return u
}

// ParseSentryUser decodes a Sentry user object (the event/transaction "user"
// field). id may be a string or a number.
func ParseSentryUser(raw json.RawMessage) SentryUser {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return SentryUser{}
	}
	var u struct {
		ID       json.RawMessage `json:"id"`
		Username string          `json:"username"`
		Email    string          `json:"email"`
		Name     string          `json:"name"`
	}
	if json.Unmarshal(raw, &u) != nil {
		return SentryUser{}
	}
	return SentryUser{
		ID:       jsonScalarString(u.ID),
		Username: u.Username,
		Email:    u.Email,
		Name:     u.Name,
	}.withIdentity()
}

// ParseSentryUserFromPayload extracts user from a full event JSON payload.
func ParseSentryUserFromPayload(payload json.RawMessage) SentryUser {
	var wrap struct {
		User json.RawMessage `json:"user"`
	}
	if json.Unmarshal(payload, &wrap) != nil {
		return SentryUser{}
	}
	return ParseSentryUser(wrap.User)
}

// ParseSentryUserFromAttrs reads user.id / user.username / user.email / user.name
// from flattened log attributes.
func ParseSentryUserFromAttrs(attrs map[string]any) SentryUser {
	if len(attrs) == 0 {
		return SentryUser{}
	}
	return SentryUser{
		ID:       anyString(attrs["user.id"]),
		Username: anyString(attrs["user.username"]),
		Email:    anyString(attrs["user.email"]),
		Name:     anyString(attrs["user.name"]),
	}.withIdentity()
}

func jsonScalarString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return strings.TrimSpace(s)
		}
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func anyString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// AppUserRow is one upsert into app_users.
type AppUserRow struct {
	ProjectID string
	User      SentryUser
	LastSeen  time.Time
}

// UpsertAppUsers merges seen end-users into app_users. Empty identities are skipped.
func UpsertAppUsers(ctx context.Context, pool *pgxpool.Pool, rows []AppUserRow) {
	if len(rows) == 0 {
		return
	}
	type key struct{ projectID, identity string }
	best := map[key]AppUserRow{}
	for _, r := range rows {
		if r.User.Identity == "" || r.User.Identity == scrubPlaceholder || r.ProjectID == "" {
			continue
		}
		k := key{r.ProjectID, r.User.Identity}
		if prev, ok := best[k]; ok {
			if r.LastSeen.After(prev.LastSeen) {
				r.User.ID = coalesceStr(r.User.ID, prev.User.ID)
				r.User.Username = coalesceStr(r.User.Username, prev.User.Username)
				r.User.Email = coalesceStr(r.User.Email, prev.User.Email)
				r.User.Name = coalesceStr(r.User.Name, prev.User.Name)
				best[k] = r
			} else {
				prev.User.ID = coalesceStr(prev.User.ID, r.User.ID)
				prev.User.Username = coalesceStr(prev.User.Username, r.User.Username)
				prev.User.Email = coalesceStr(prev.User.Email, r.User.Email)
				prev.User.Name = coalesceStr(prev.User.Name, r.User.Name)
				best[k] = prev
			}
			continue
		}
		best[k] = r
	}
	if len(best) == 0 {
		return
	}
	b := &pgx.Batch{}
	for _, r := range best {
		seen := r.LastSeen
		if seen.IsZero() {
			seen = time.Now().UTC()
		}
		b.Queue(`
			INSERT INTO app_users (project_id, identity, user_id, username, email, name, last_seen)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (project_id, identity) DO UPDATE SET
				user_id   = COALESCE(EXCLUDED.user_id, app_users.user_id),
				username  = COALESCE(EXCLUDED.username, app_users.username),
				email     = COALESCE(EXCLUDED.email, app_users.email),
				name      = COALESCE(EXCLUDED.name, app_users.name),
				last_seen = GREATEST(app_users.last_seen, EXCLUDED.last_seen)
		`, r.ProjectID, r.User.Identity, nilStr(r.User.ID), nilStr(r.User.Username),
			nilStr(r.User.Email), nilStr(r.User.Name), seen)
	}
	br := pool.SendBatch(ctx, b)
	for range best {
		if _, err := br.Exec(); err != nil {
			slog.Error("app_users upsert", "err", err)
		}
	}
	if err := br.Close(); err != nil {
		slog.Error("app_users batch close", "err", err)
	}
}

func coalesceStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
