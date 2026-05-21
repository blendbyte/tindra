package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// realIPFromTrustedProxy sets r.RemoteAddr to the client IP from X-Forwarded-For
// or X-Real-IP, but only when the TCP connection comes from a configured trusted
// proxy CIDR. When trustedProxies is empty, the raw TCP address is always used
// and forwarded headers are ignored entirely.
func realIPFromTrustedProxy(trustedProxies []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trustedProxies) > 0 {
				host, _, err := net.SplitHostPort(r.RemoteAddr)
				if err == nil {
					if ip := net.ParseIP(host); ip != nil && cidrContains(trustedProxies, ip) {
						if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
							// Leftmost entry is the original client IP.
							clientIP := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
							if net.ParseIP(clientIP) != nil {
								r = r.Clone(r.Context())
								r.RemoteAddr = clientIP + ":0"
							}
						} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
							if net.ParseIP(strings.TrimSpace(xri)) != nil {
								r = r.Clone(r.Context())
								r.RemoteAddr = strings.TrimSpace(xri) + ":0"
							}
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func cidrContains(cidrs []*net.IPNet, ip net.IP) bool {
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (ro *router) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:")
		if ro.cookieSecure {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Max-Age", "300")
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type ctxKey int

const (
	ctxUserID        ctxKey = 0
	ctxTokenProjID   ctxKey = 1
	ctxUserPerms     ctxKey = 2
	ctxTokenWritable ctxKey = 3
)

// actorFromContext returns the authenticated user ID when the request was
// made via session cookie, or nil when authenticated via Bearer token
// (tokens carry a project scope, not a user identity).
func actorFromContext(ctx context.Context) *string {
	if id, ok := ctx.Value(ctxUserID).(string); ok && id != "" {
		return &id
	}
	return nil
}

func permsFromContext(ctx context.Context) *storage.UserPermissions {
	if p, ok := ctx.Value(ctxUserPerms).(*storage.UserPermissions); ok {
		return p
	}
	return nil
}

// requireAuth accepts either a valid session cookie or a Bearer API token.
// On success it sets ctxUserID (session) or ctxTokenProjID (token) in context.
func (ro *router) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			plaintext := strings.TrimPrefix(auth, "Bearer ")
			hash := storage.HashAPIToken(plaintext)
			tok, err := storage.GetAPITokenByHash(r.Context(), ro.pool, hash)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if tok != nil {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					storage.TouchAPIToken(ctx, ro.pool, tok.ID)
				}()
				ctx := context.WithValue(r.Context(), ctxTokenProjID, tok.ProjectID)
				ctx = context.WithValue(ctx, ctxTokenWritable, tok.Writable)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		cookie, err := r.Cookie("tindra_session")
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		session, err := storage.GetSession(r.Context(), ro.pool, cookie.Value)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if session == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, session.UserID)
		// Fetch permissions so requirePerm can check them without an extra DB call.
		u, err := storage.GetUserByID(ctx, ro.pool, session.UserID)
		if err == nil && u != nil {
			ctx = context.WithValue(ctx, ctxUserPerms, &u.Permissions)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireSessionAuth only allows cookie-session auth. Used for endpoints that
// manage API tokens themselves so a token cannot create or revoke other tokens.
func (ro *router) requireSessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("tindra_session")
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		session, err := storage.GetSession(r.Context(), ro.pool, cookie.Value)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if session == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, session.UserID)
		u, err := storage.GetUserByID(ctx, ro.pool, session.UserID)
		if err == nil && u != nil {
			ctx = context.WithValue(ctx, ctxUserPerms, &u.Permissions)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requirePerm returns a middleware that enforces a named permission.
// Bearer-token requests (project-scoped) bypass the check - they are already
// scoped to a single project by the token itself.
func (ro *router) requirePerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bearer tokens are project-scoped and carry no user permissions.
			// Project-level operations (issues, sourcemaps, alert rules) are scoped
			// via projectFromSlug, not requirePerm. Anything behind requirePerm
			// (user management, global project mutations) requires a session cookie.
			if _, ok := r.Context().Value(ctxTokenProjID).(string); ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			p := permsFromContext(r.Context())
			if p == nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			var allowed bool
			switch perm {
			case "manage_projects":
				allowed = p.ManageProjects
			case "manage_users":
				allowed = p.ManageUsers
			case "manage_alerts":
				allowed = p.ManageAlerts
			case "manage_issues":
				allowed = p.ManageIssues
			}
			if !allowed {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
