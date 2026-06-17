package api

import (
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/ui"
)

func init() {
	// Distroless and other minimal container images lack a system mime.types
	// database, causing http.FileServer to fall back to text/plain for JS/CSS.
	// Register the types we serve explicitly so they are always correct.
	for ext, typ := range map[string]string{
		".js":    "text/javascript; charset=utf-8",
		".mjs":   "text/javascript; charset=utf-8",
		".css":   "text/css; charset=utf-8",
		".woff2": "font/woff2",
		".svg":   "image/svg+xml",
	} {
		if err := mime.AddExtensionType(ext, typ); err != nil {
			panic(err)
		}
	}
}

type router struct {
	pool                   *pgxpool.Pool
	buf                    *ingest.Buffer
	txBuf                  *ingest.TransactionBuffer
	logBuf                 *ingest.LogBuffer
	smStore                *sourcemaps.Store
	oauthProviders         []oauthProvider
	cookieSecure           bool
	corsOrigin             string
	publicURL              string
	statsAPIKey            string
	billingURL             string
	projectLimit           atomic.Int32
	eventLimit             atomic.Int32
	userLimit              atomic.Int32
	evaluator              *alerts.Evaluator
	passthroughClient      *http.Client
	retentionDays          int
	webhookAllowPrivateIPs bool
	requireMFA             bool
	trustedProxies         []*net.IPNet
	loginEmailRL           *rateLimiter
	envelopeRL             *rateLimiter
	cronPingRL             *rateLimiter
	startedAt              time.Time
	versionMu              sync.RWMutex
	latestVersion          string
	releaseURL             string
}

// Handle wraps the HTTP handler returned by NewRouter and exposes SetLimits
// for updating plan quotas at runtime without restarting the process.
type Handle struct {
	http.Handler
	ro *router
}

// SetLimits atomically updates the project, event, and user limits.
// Safe to call from any goroutine while the server is running.
func (h *Handle) SetLimits(projectLimit, eventLimit, userLimit int) {
	h.ro.projectLimit.Store(int32(projectLimit))
	h.ro.eventLimit.Store(int32(eventLimit))
	h.ro.userLimit.Store(int32(userLimit))
}

func NewRouter(pool *pgxpool.Pool, buf *ingest.Buffer, txBuf *ingest.TransactionBuffer, logBuf *ingest.LogBuffer, smStore *sourcemaps.Store, oauthProviders []oauthProvider, cookieSecure bool, corsOrigin string, publicURL string, statsAPIKey string, billingURL string, retentionDays int, projectLimit int, eventLimit int, userLimit int, rateLimitLogin int, rateLimitEnvelope int, evaluator *alerts.Evaluator, webhookAllowPrivateIPs bool, requireMFA bool, trustedProxies []*net.IPNet) *Handle {
	ro := &router{
		pool:                   pool,
		buf:                    buf,
		txBuf:                  txBuf,
		logBuf:                 logBuf,
		smStore:                smStore,
		oauthProviders:         oauthProviders,
		cookieSecure:           cookieSecure,
		corsOrigin:             corsOrigin,
		publicURL:              publicURL,
		statsAPIKey:            statsAPIKey,
		billingURL:             billingURL,
		retentionDays:          retentionDays,
		evaluator:              evaluator,
		passthroughClient:      alerts.NewWebhookClient(webhookAllowPrivateIPs),
		webhookAllowPrivateIPs: webhookAllowPrivateIPs,
		requireMFA:             requireMFA,
		trustedProxies:         trustedProxies,
		startedAt:              time.Now().UTC(),
	}
	ro.projectLimit.Store(int32(projectLimit))
	ro.eventLimit.Store(int32(eventLimit))
	ro.userLimit.Store(int32(userLimit))

	loginRL := newRateLimiter(rateLimitLogin, 15*time.Minute)
	ro.envelopeRL = newRateLimiter(rateLimitEnvelope, time.Minute)
	ro.loginEmailRL = newRateLimiter(rateLimitLogin, 15*time.Minute)
	ro.cronPingRL = newRateLimiter(5, time.Minute)

	r := chi.NewRouter()
	r.Use(realIPFromTrustedProxy(ro.trustedProxies))
	r.Use(corsMiddleware(ro.corsOrigin))
	r.Use(ro.securityHeaders)
	r.Use(slogRequestLogger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", ro.healthz)
	r.Get("/assets/email-logo.png", ro.handleEmailLogo)

	// Envelope ingest: URL carries project UUID, public key comes via X-Sentry-Auth header.
	r.Options("/api/{projectID}/envelope/", ro.handleEnvelopeCORS)
	r.With(ro.envelopeRL.limitBy(func(r *http.Request) string {
		return chi.URLParam(r, "projectID")
	})).Post("/api/{projectID}/envelope/", ro.handleEnvelope)

	r.With(loginRL.limitByIP()).Post("/api/auth/login", ro.handleLogin)
	r.Post("/api/auth/logout", ro.handleLogout)
	r.With(loginRL.limitByIP()).Post("/api/auth/mfa/verify", ro.handleMFAVerify)

	// Invite accept - public, no auth required.
	r.Get("/api/auth/invite/{token}", ro.handleGetInvite)
	r.Post("/api/auth/invite/{token}/accept", ro.handleAcceptInvite)

	// Password reset - public, no auth required.
	r.Get("/api/auth/password-reset/{token}", ro.handleGetPasswordReset)
	r.Post("/api/auth/password-reset/{token}", ro.handleDoPasswordReset)

	// Public config (no auth needed).
	r.Get("/api/config", ro.handleGetConfig)

	// OAuth/OIDC - no prior auth needed.
	r.Get("/api/auth/providers", ro.handleListProviders)
	r.Get("/api/auth/{provider}/redirect", ro.handleOAuthRedirect)
	r.Get("/api/auth/{provider}/callback", ro.handleOAuthCallback)

	// Stats endpoint - authenticated by STATS_API_KEY bearer token, no session required.
	r.Get("/api/stats", ro.handleGetStats)

	// Bearer token or session cookie accepted for data-read/write routes.
	r.Group(func(r chi.Router) {
		r.Use(ro.requireAuth)

		// Global (cross-project) endpoints used by the SPA.
		r.Get("/api/settings", ro.handleGetSettings)
		r.Get("/api/me", ro.handleGetMe)
		r.Patch("/api/me", ro.handleUpdateMe)
		r.Get("/api/users", ro.handleListUsers)
		r.Get("/api/projects", ro.handleListProjects)
		r.Get("/api/projects/stats", ro.handleGetProjectStats)
		r.With(ro.requirePerm("manage_projects")).Post("/api/projects", ro.handleCreateProject)
		r.Get("/api/projects/{projectID}/quota", ro.handleGetProjectQuota)
		r.With(ro.requirePerm("manage_projects")).Patch("/api/projects/{projectID}", ro.handleUpdateProject)
		r.With(ro.requirePerm("manage_projects")).Patch("/api/projects/{projectID}/privacy", ro.handleUpdateProjectPrivacy)
		r.With(ro.requirePerm("manage_projects")).Delete("/api/projects/{projectID}", ro.handleDeleteProject)
		r.Get("/api/issues", ro.handleListAllIssues)
		r.Get("/api/issues/export", ro.handleExportIssues)
		r.Get("/api/issues/{issueID}", ro.handleGetIssueGlobal)
		r.Get("/api/issues/{issueID}/events/histogram", ro.handleGetIssueHistogram)
		r.Get("/api/issues/{issueID}/events", ro.handleListEventsForIssue)
		r.Get("/api/issues/{issueID}/events/latest", ro.handleGetLatestEventGlobal)
		r.Get("/api/issues/{issueID}/trace", ro.handleGetIssueTrace)
		r.Get("/api/issues/{issueID}/tags", ro.handleGetIssueTags)
		r.Get("/api/issues/{issueID}/history", ro.handleGetIssueHistory)
		r.Get("/api/issues/{issueID}/perf-events", ro.handleListPerfEvents)
		r.Get("/api/issues/{issueID}/comments", ro.handleListComments)
		r.Get("/api/transactions", ro.handleListAllTransactions)
		r.Get("/api/transactions/summaries", ro.handleListTransactionSummaries)
		r.Get("/api/transactions/timeseries", ro.handleTransactionTimeseries)
		r.Get("/api/transactions/{txID}", ro.handleGetTransactionGlobal)
		r.Get("/api/transactions/{txID}/spans", ro.handleGetSpansGlobal)
		r.Get("/api/transactions/{txID}/errors", ro.handleGetTransactionErrors)
		r.Get("/api/spans/db", ro.handleSpanSummaries("db"))
		r.Get("/api/spans/db/timeseries", ro.handleSpanTimeseries("db"))
		r.Get("/api/spans/cache", ro.handleSpanSummaries("cache"))
		r.Get("/api/spans/cache/timeseries", ro.handleSpanTimeseries("cache"))
		r.Get("/api/spans/jobs", ro.handleSpanSummaries("job"))
		r.Get("/api/spans/jobs/timeseries", ro.handleSpanTimeseries("job"))
		r.Get("/api/spans/samples", ro.handleSpanSamples)
		r.Get("/api/vitals", ro.handleGetWebVitals)
		r.Get("/api/vitals/pages", ro.handleGetWebVitalsPages)
		r.Get("/api/tokens", ro.handleListAllTokens)

		// manage_projects: token write operations (a token grants project-scoped write access,
		// so creating or revoking one is equivalent to granting/removing that access).
		r.With(ro.requirePerm("manage_projects")).Post("/api/tokens", ro.handleCreateTokenGlobal)
		r.With(ro.requirePerm("manage_projects")).Patch("/api/tokens/{tokenID}", ro.handleUpdateTokenGlobal)
		r.With(ro.requirePerm("manage_projects")).Delete("/api/tokens/{tokenID}", ro.handleDeleteTokenGlobal)
		r.Get("/api/logs", ro.handleListLogs)
		r.Get("/api/releases", ro.handleListReleases)
		r.Get("/api/releases/{releaseID}", ro.handleGetRelease)
		r.Get("/api/releases/{releaseID}/issues", ro.handleGetReleaseIssues)
		r.Get("/api/releases/{releaseID}/transactions", ro.handleGetReleaseTransactions)
		r.With(ro.requirePerm("manage_users")).Get("/api/audit", ro.handleListAuditLog)
		r.With(ro.requirePerm("manage_projects")).Get("/api/instance/health", ro.handleGetInstanceHealth)

		r.Get("/api/projects/{projectSlug}/issues", ro.handleListIssues)
		r.Get("/api/projects/{projectSlug}/issues/{issueID}", ro.handleGetIssue)
		r.Get("/api/projects/{projectSlug}/issues/{issueID}/fingerprints", ro.handleGetIssueFingerprints)
		r.Get("/api/projects/{projectSlug}/issues/{issueID}/events/latest", ro.handleGetLatestEvent)

		// Register /stats before /{txID} so chi's literal match takes priority.
		r.Get("/api/projects/{projectSlug}/transactions", ro.handleListTransactions)
		r.Get("/api/projects/{projectSlug}/transactions/stats", ro.handleTransactionStats)
		r.Get("/api/projects/{projectSlug}/transactions/{txID}", ro.handleGetTransaction)

		r.Get("/api/projects/{projectSlug}/sourcemaps", ro.handleListSourcemaps)

		// Comments are open to all authenticated users.
		r.Post("/api/issues/{issueID}/comments", ro.handleCreateComment)
		r.Put("/api/comments/{commentID}", ro.handleUpdateComment)
		r.Delete("/api/comments/{commentID}", ro.handleDeleteComment)

		// manage_issues: write operations on issues.
		r.With(ro.requirePerm("manage_issues")).Patch("/api/issues/bulk", ro.handleBulkUpdateIssues)
		r.With(ro.requirePerm("manage_issues")).Patch("/api/issues/{issueID}", ro.handleUpdateIssueGlobal)
		r.With(ro.requirePerm("manage_issues")).Patch("/api/projects/{projectSlug}/issues/{issueID}", ro.handleUpdateIssue)
		r.With(ro.requirePerm("manage_issues")).Post("/api/projects/{projectSlug}/issues/merge", ro.handleMergeIssues)
		r.With(ro.requirePerm("manage_issues")).Post("/api/projects/{projectSlug}/issues/{issueID}/unmerge", ro.handleUnmergeIssue)

		// manage_projects: sourcemap write operations (project create/delete TBD in later phases).
		r.With(ro.requirePerm("manage_projects")).Post("/api/projects/{projectSlug}/sourcemaps", ro.handleUploadSourcemap)
		r.With(ro.requirePerm("manage_projects")).Delete("/api/projects/{projectSlug}/sourcemaps/{smID}", ro.handleDeleteSourcemap)

		// manage_users: invite management, user deletion, permission management, and admin user actions.
		r.With(ro.requirePerm("manage_users")).Post("/api/invites", ro.handleCreateInvite)
		r.With(ro.requirePerm("manage_users")).Get("/api/invites", ro.handleListInvites)
		r.With(ro.requirePerm("manage_users")).Delete("/api/invites/{id}", ro.handleRevokeInvite)
		r.With(ro.requirePerm("manage_users")).Delete("/api/users/{userID}", ro.handleDeleteUser)
		r.With(ro.requirePerm("manage_users")).Put("/api/users/{userID}/permissions", ro.handleUpdateUserPermissions)
		r.With(ro.requirePerm("manage_users")).Delete("/api/users/{userID}/mfa", ro.handleAdminDisableMFA)
		r.With(ro.requirePerm("manage_users")).Put("/api/users/{userID}/password", ro.handleAdminSetPassword)
		r.With(ro.requirePerm("manage_users")).Post("/api/users/{userID}/password-reset", ro.handleAdminSendPasswordReset)

		// MCP (Model Context Protocol) - Streamable HTTP transport.
		r.Post("/mcp", ro.handleMCP)
	})

	// Session-only: token management and MFA management cannot be done via Bearer token.
	r.Group(func(r chi.Router) {
		r.Use(ro.requireSessionAuth)

		r.Get("/api/projects/{projectSlug}/tokens", ro.handleListTokens)
		r.With(ro.requirePerm("manage_projects")).Post("/api/projects/{projectSlug}/tokens", ro.handleCreateToken)
		r.With(ro.requirePerm("manage_projects")).Delete("/api/projects/{projectSlug}/tokens/{tokenID}", ro.handleDeleteToken)

		r.Patch("/api/me/password", ro.handleChangePassword)

		r.Get("/api/auth/mfa/setup", ro.handleMFASetup)
		r.Post("/api/auth/mfa/confirm", ro.handleMFAConfirm)
		r.Delete("/api/auth/mfa", ro.handleMFADisable)
	})

	// Alert rules - session or Bearer, top-level (not scoped to a single project).
	r.Group(func(r chi.Router) {
		r.Use(ro.requireAuth)

		r.Get("/api/alert-rules", ro.handleListAlertRules)
		r.Get("/api/alert-rules/{ruleID}", ro.handleGetAlertRule)
		r.Get("/api/alert-rules/{ruleID}/firings", ro.handleListAlertFirings)

		// manage_alerts: write operations.
		r.With(ro.requirePerm("manage_alerts")).Post("/api/alert-rules", ro.handleCreateAlertRule)
		r.With(ro.requirePerm("manage_alerts")).Patch("/api/alert-rules/{ruleID}", ro.handleUpdateAlertRule)
		r.With(ro.requirePerm("manage_alerts")).Delete("/api/alert-rules/{ruleID}", ro.handleDeleteAlertRule)
		r.With(ro.requirePerm("manage_alerts")).Post("/api/alert-rules/{ruleID}/test", ro.handleTestAlertRule)
	})

	// Cron monitor ingest - public, auth is by knowing the monitor UUID.
	// Accepts GET and POST so shell scripts can use curl without -X POST.
	// Rate-limited per monitor ID to prevent alert suppression via ping flooding.
	cronRL := ro.cronPingRL.limitBy(func(r *http.Request) string {
		return chi.URLParam(r, "monitorID")
	})
	r.With(cronRL).Get("/api/cron/{monitorID}", ro.handleCronPing)
	r.With(cronRL).Post("/api/cron/{monitorID}", ro.handleCronPing)
	r.With(cronRL).Post("/api/cron/{monitorID}/checkins/", ro.handleCronCheckinStart)
	r.With(cronRL).Put("/api/cron/{monitorID}/checkins/{checkinID}/", ro.handleCronCheckinFinish)
	// Oh Dear / Spatie laravel-schedule-monitor compatibility.
	r.With(cronRL).Post("/api/cron/{monitorID}/starting", ro.handleOhDearStarting)
	r.With(cronRL).Post("/api/cron/{monitorID}/finished", ro.handleOhDearFinished)
	r.With(cronRL).Post("/api/cron/{monitorID}/failed", ro.handleOhDearFailed)

	// Cron monitor CRUD - authenticated.
	r.Group(func(r chi.Router) {
		r.Use(ro.requireAuth)
		r.Get("/api/monitors", ro.handleListMonitors)
		r.Get("/api/monitors/{monitorID}", ro.handleGetMonitor)
		r.Get("/api/monitors/{monitorID}/checkins", ro.handleListCheckins)
		r.With(ro.requirePerm("manage_projects")).Post("/api/monitors", ro.handleCreateMonitor)
		r.With(ro.requirePerm("manage_projects")).Patch("/api/monitors/{monitorID}", ro.handleUpdateMonitor)
		r.With(ro.requirePerm("manage_projects")).Delete("/api/monitors/{monitorID}", ro.handleDeleteMonitor)
	})

	// Uptime monitor CRUD - authenticated.
	r.Group(func(r chi.Router) {
		r.Use(ro.requireAuth)
		r.Get("/api/uptime-monitors", ro.handleListUptimeMonitors)
		r.Get("/api/uptime-monitors/{monitorID}", ro.handleGetUptimeMonitor)
		r.Get("/api/uptime-monitors/{monitorID}/checks", ro.handleListUptimeChecks)
		r.Get("/api/uptime-monitors/{monitorID}/stats", ro.handleGetUptimeStats)
		r.With(ro.requirePerm("manage_projects")).Post("/api/uptime-monitors", ro.handleCreateUptimeMonitor)
		r.With(ro.requirePerm("manage_projects")).Patch("/api/uptime-monitors/{monitorID}", ro.handleUpdateUptimeMonitor)
		r.With(ro.requirePerm("manage_projects")).Delete("/api/uptime-monitors/{monitorID}", ro.handleDeleteUptimeMonitor)
	})

	// MCP OAuth discovery endpoints — must be registered before the SPA catch-all so
	// clients get JSON instead of index.html when probing for OAuth metadata.
	r.Get("/.well-known/oauth-authorization-server", ro.handleMCPOAuthAS)
	r.Get("/.well-known/oauth-protected-resource", ro.handleMCPOAuthPR)
	// GET /mcp for clients probing SSE support — we only support POST; return 405 JSON.
	r.Get("/mcp", ro.handleMCPGet)

	// Serve the embedded Vue SPA. If a file exists in dist, serve it
	// directly; otherwise fall back to index.html for client-side routing.
	dist, _ := fs.Sub(ui.FS, "dist")
	fileServer := http.FileServer(http.FS(dist))
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if f, err := dist.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		f, err := dist.Open("index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer f.Close()
		stat, _ := f.Stat()
		http.ServeContent(w, r, "index.html", stat.ModTime(), f.(io.ReadSeeker))
	})

	return &Handle{Handler: r, ro: ro}
}

func (ro *router) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (ro *router) handleEmailLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(alerts.LogoPNG)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
