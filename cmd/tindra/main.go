package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/spf13/cobra"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/background"
	"github.com/blendbyte/tindra/internal/digest"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/issues"
	"github.com/blendbyte/tindra/internal/retention"
	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/uptime"
	"github.com/blendbyte/tindra/internal/version"
)

// Version and Commit are injected at build time via -ldflags.
var Version = "dev"
var Commit = "unknown"

// startupLog always emits regardless of the configured log level so startup
// info (version, settings, addresses) is visible even at warn/error level.
var startupLog = slog.Default()

type config struct {
	databaseURL      string
	bindAddr         string
	publicURL        string
	statsAPIKey      string
	billingURL       string
	logLevel         string
	logFormat        string
	ingestBufferSize int
	// profileBufferSize is sized independently of ingestBufferSize: a profile
	// is orders of magnitude larger than an event, so the queue is far shorter.
	profileBufferSize      int
	dataDir                string
	retentionDays          int
	cookieSecure           bool
	corsOrigin             string
	projectLimit           int
	eventLimit             int
	userLimit              int
	rateLimitLogin         int // max login attempts per 15 min per IP; 0 = disabled
	rateLimitEnvelope      int // max envelope requests per minute per project; 0 = disabled
	logRowLimit            int // max log rows per project (0 = no cap)
	txRowLimit             int // max transaction rows per project (0 = no cap)
	profileRetentionDays   int // days to keep profiles (0 = keep forever)
	profileStorageLimitMB  int // instance-wide profile storage budget (0 = no cap)
	webhookAllowPrivateIPs bool
	uptimeAllowPrivateIPs  bool
	requireMFA             bool
	trustedProxies         []*net.IPNet
	skipAutoMigrate        bool
	disableVersionCheck    bool
	socketMode             fs.FileMode
}

func main() {
	// Apply ./.env or /etc/tindra/tindra.env if present, beneath the real environment.
	env, err := loadDotenv(envFilePath(systemEnvFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	version.App = Version
	api.AppVersion = Version
	api.AppCommit = Commit

	cfg := loadConfig()

	root := &cobra.Command{
		Use:   "tindra",
		Short: "Self-hosted error tracking and performance monitoring",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			setupLogger(cfg.logLevel, cfg.logFormat)
		},
		SilenceUsage: true,
	}

	root.AddCommand(
		serveCmd(cfg, env),
		migrateCmd(cfg),
		projectsCmd(cfg),
		usersCmd(cfg),
		sendDigestCmd(cfg),
		healthcheckCmd(cfg),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func loadConfig() config {
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = ":8080"
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/data"
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	var trustedProxies []*net.IPNet
	if s := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); s != "" {
		for raw := range strings.SplitSeq(s, ",") {
			raw = strings.TrimSpace(raw)
			// Accept bare IPs as /32 or /128.
			if net.ParseIP(raw) != nil {
				raw = raw + "/32"
				if strings.Contains(raw, ":") {
					raw = strings.TrimSuffix(raw, "/32") + "/128"
				}
			}
			_, cidr, err := net.ParseCIDR(raw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: invalid TRUSTED_PROXIES entry %q, skipping\n", raw)
				continue
			}
			trustedProxies = append(trustedProxies, cidr)
		}
	}

	return config{
		databaseURL:       os.Getenv("DATABASE_URL"),
		bindAddr:          bindAddr,
		logLevel:          logLevel,
		logFormat:         os.Getenv("LOG_FORMAT"),
		ingestBufferSize:  envInt("INGEST_BUFFER_SIZE", 10000, 1),
		profileBufferSize: envInt("PROFILE_BUFFER_SIZE", 500, 1),
		dataDir:           dataDir,
		retentionDays:     envInt("RETENTION_DAYS", 90, 0),
		cookieSecure:      envBool("COOKIE_SECURE", false),
		corsOrigin:        os.Getenv("CORS_ORIGIN"),
		publicURL:         os.Getenv("PUBLIC_URL"),
		statsAPIKey:       os.Getenv("STATS_API_KEY"),
		billingURL:        strings.TrimSpace(os.Getenv("BILLING_URL")),
		projectLimit:      envInt("PROJECT_LIMIT", 0, 0),
		eventLimit:        envInt("EVENT_LIMIT", 0, 0),
		userLimit:         envInt("USER_LIMIT", 0, 0),
		logRowLimit:       envInt("LOG_ROW_LIMIT", 0, 0),
		txRowLimit:        envInt("TX_ROW_LIMIT", 0, 0),
		// Profiles carry their own retention window and a byte budget rather
		// than following RETENTION_DAYS. They are orders of magnitude larger
		// per unit of time than anything else stored, so the 90-day default
		// would run to tens of gigabytes for a few continuously profiled processes.
		profileRetentionDays:   envInt("PROFILE_RETENTION_DAYS", 7, 0),
		profileStorageLimitMB:  envInt("PROFILE_STORAGE_LIMIT_MB", 2048, 0),
		rateLimitLogin:         envInt("RATE_LIMIT_LOGIN", 10, 0),
		rateLimitEnvelope:      envInt("RATE_LIMIT_ENVELOPE", 300, 0),
		webhookAllowPrivateIPs: envBool("WEBHOOK_ALLOW_PRIVATE_IPS", false),
		uptimeAllowPrivateIPs:  envBool("UPTIME_ALLOW_PRIVATE_IPS", false),
		requireMFA:             requireMFAFromEnv(),
		trustedProxies:         trustedProxies,
		skipAutoMigrate:        envBool("SKIP_AUTO_MIGRATE", false),
		disableVersionCheck:    envBool("DISABLE_VERSION_CHECK", false),
		socketMode:             parseSocketMode(os.Getenv("SOCKET_MODE"), 0660),
	}
}

// envInt reads an integer of at least minimum from the environment. Unset
// means def. An invalid value warns and uses def, so a typo shows up in the
// log instead of silently changing behaviour.
func envInt(name string, def, minimum int) int {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < minimum {
		fmt.Fprintf(os.Stderr, "warn: invalid %s %q (want an integer >= %d), using %d\n", name, s, minimum, def)
		return def
	}
	return n
}

// requireMFAFromEnv keeps MFA enforcement on unless REQUIRE_MFA is exactly
// "false" (any case). A security control is only switched off explicitly, so
// values like "0" or "no" keep it on, with a warning.
func requireMFAFromEnv() bool {
	s := strings.TrimSpace(os.Getenv("REQUIRE_MFA"))
	switch {
	case s == "" || strings.EqualFold(s, "true"):
		return true
	case strings.EqualFold(s, "false"):
		return false
	}
	fmt.Fprintf(os.Stderr, "warn: invalid REQUIRE_MFA %q (want true or false), keeping MFA required\n", s)
	return true
}

// envBool reads a boolean from the environment, accepting the usual spellings
// (true/false, 1/0, TRUE/FALSE). Unset means def. An invalid value warns and
// uses def: for example COOKIE_SECURE=yes used to switch secure cookies off.
func envBool(name string, def bool) bool {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: invalid %s %q (want true or false), using %t\n", name, s, def)
		return def
	}
	return b
}

// parseSocketMode parses an octal string like "0666" into a FileMode.
// Falls back to def on empty input or parse error.
//
// A common gotcha: in a docker-compose `environment:` block, an unquoted
// `SOCKET_MODE: 0666` is parsed by YAML as the octal integer 438 and reaches
// the process as the string "438", which is not valid octal. Quote it
// (`SOCKET_MODE: "0666"`) so the literal string is passed through. The warning
// below spells this out rather than guessing, since silently treating a decimal
// like "432" as octal would apply the wrong permissions to the socket.
func parseSocketMode(s string, def fs.FileMode) fs.FileMode {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: invalid SOCKET_MODE %q (expected octal like 0666), using %04o; "+
			"if set via a docker-compose environment block, quote it: SOCKET_MODE: \"0666\"\n", s, def)
		return def
	}
	return fs.FileMode(n)
}

func setupLogger(level, format string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var logger *slog.Logger
	if format == "json" {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, opts))
	} else {
		logger = slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	slog.SetDefault(logger)

	// startupLog uses the same format but no level filter so startup messages
	// (version, settings, addresses) are always visible.
	alwaysOpts := &slog.HandlerOptions{Level: slog.Level(-100)}
	if format == "json" {
		startupLog = slog.New(slog.NewJSONHandler(os.Stdout, alwaysOpts))
	} else {
		startupLog = slog.New(slog.NewTextHandler(os.Stdout, alwaysOpts))
	}
}

func serveCmd(cfg config, env *dotenv) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			if err := checkDataDir(cfg.dataDir); err != nil {
				return err
			}

			pool, err := storage.Connect(ctx, cfg.databaseURL)
			if err != nil {
				return fmt.Errorf("connect to database: %w", err)
			}
			defer pool.Close()

			if !cfg.skipAutoMigrate {
				m, err := newMigrator(cfg)
				if err != nil {
					return fmt.Errorf("init migrator: %w", err)
				}
				defer m.Close()
				before, _, _ := m.Version()
				if err := m.Up(); err != nil && err != migrate.ErrNoChange {
					return fmt.Errorf("auto-migrate: %w", err)
				}
				after, _, _ := m.Version()
				if after > before {
					startupLog.Info("database migrations applied", "from", before, "to", after)
				} else {
					startupLog.Info("database schema up to date", "version", after)
				}
			}

			go storage.MonitorPool(ctx, pool)

			writerCtx, stopWriters := context.WithCancel(context.Background())
			defer stopWriters()
			buf := ingest.NewBuffer(cfg.ingestBufferSize)
			bufDone := make(chan struct{})
			go func() {
				defer close(bufDone)
				buf.Run(writerCtx, pool)
			}()

			txBuf := ingest.NewTransactionBuffer(cfg.ingestBufferSize)
			txBuf.Hook = issues.NewN1Detector(pool).ProcessBatch
			txBufDone := make(chan struct{})
			go func() {
				defer close(txBufDone)
				txBuf.Run(writerCtx, pool)
			}()

			logBuf := ingest.NewLogBuffer(cfg.ingestBufferSize)
			logBufDone := make(chan struct{})
			go func() {
				defer close(logBufDone)
				logBuf.Run(writerCtx, pool)
			}()

			profBuf := ingest.NewProfileBuffer(cfg.profileBufferSize)
			profBufDone := make(chan struct{})
			go func() {
				defer close(profBufDone)
				profBuf.Run(writerCtx, pool)
			}()

			emailSender, err := alerts.NewEmailSenderFromEnv()
			if err != nil {
				return fmt.Errorf("email sender: %w", err)
			}
			evaluator := alerts.NewEvaluator(pool, emailSender, cfg.publicURL, cfg.webhookAllowPrivateIPs)

			api.AppEmailSender = emailSender

			smStore := sourcemaps.NewStore(cfg.dataDir, pool)
			oauthProviders := api.LoadOAuthProviders(ctx)

			// Background jobs. The grouper is safe to run on every instance;
			// the rest run on one instance at a time (see package background).
			waitJobs := background.Start(ctx, pool, []background.Job{
				{Name: "grouper", Run: issues.NewGrouper(pool).Run},
				{Name: "alerts", Singleton: true, Run: evaluator.Run},
				{Name: "retention", Singleton: true, Run: retention.NewWorker(pool, cfg.retentionDays).
					WithRowLimits(cfg.logRowLimit, cfg.txRowLimit).
					WithProfileLimits(cfg.profileRetentionDays, cfg.profileStorageLimitMB).
					Run},
				{Name: "digest", Singleton: true, Run: digest.NewWorker(pool, emailSender, cfg.publicURL).Run},
				{Name: "uptime", Singleton: true, Run: uptime.NewWorker(pool, cfg.uptimeAllowPrivateIPs).Run},
				{Name: "expire-snoozes", Singleton: true, Run: expireSnoozesJob(pool)},
				{Name: "auto-resolve", Singleton: true, Run: autoResolveJob(pool, evaluator)},
			})

			if env != nil && env.found {
				startupLog.Info("configuration file loaded", "path", env.path)
			}
			startupLog.Info("starting tindra",
				"version", Version,
				"commit", Commit,
				"addr", cfg.bindAddr,
				"project_limit", cfg.projectLimit,
				"event_limit", cfg.eventLimit,
				"user_limit", cfg.userLimit,
				"log_row_limit", cfg.logRowLimit,
				"tx_row_limit", cfg.txRowLimit,
				"public_url", cfg.publicURL,
				"rate_limit_login", cfg.rateLimitLogin,
				"rate_limit_envelope", cfg.rateLimitEnvelope,
			)

			handler := api.NewRouter(api.Config{
				Pool:                   pool,
				Events:                 buf,
				Transactions:           txBuf,
				Logs:                   logBuf,
				Profiles:               profBuf,
				Sourcemaps:             smStore,
				OAuthProviders:         oauthProviders,
				CookieSecure:           cfg.cookieSecure,
				CORSOrigin:             cfg.corsOrigin,
				PublicURL:              cfg.publicURL,
				StatsAPIKey:            cfg.statsAPIKey,
				BillingURL:             cfg.billingURL,
				RetentionDays:          cfg.retentionDays,
				ProjectLimit:           cfg.projectLimit,
				EventLimit:             cfg.eventLimit,
				UserLimit:              cfg.userLimit,
				RateLimitLogin:         cfg.rateLimitLogin,
				RateLimitEnvelope:      cfg.rateLimitEnvelope,
				Evaluator:              evaluator,
				WebhookAllowPrivateIPs: cfg.webhookAllowPrivateIPs,
				RequireMFA:             cfg.requireMFA,
				TrustedProxies:         cfg.trustedProxies,
			})
			handler.SetRowLimits(cfg.logRowLimit, cfg.txRowLimit)
			if !cfg.disableVersionCheck {
				handler.StartVersionChecker(ctx)
			}

			sighup := make(chan os.Signal, 1)
			signal.Notify(sighup, syscall.SIGHUP)
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-sighup:
						if env != nil {
							if err := env.reload(); err != nil {
								slog.Warn("reload configuration file", "err", err)
							}
						}
						newCfg := loadConfig()
						handler.SetLimits(newCfg.projectLimit, newCfg.eventLimit, newCfg.userLimit)
						slog.Info("limits reloaded",
							"project_limit", newCfg.projectLimit,
							"event_limit", newCfg.eventLimit,
							"user_limit", newCfg.userLimit,
						)
					}
				}
			}()

			srv := newHTTPServer(handler)

			ln, err := listen(cfg.bindAddr, cfg.socketMode)
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}

			shutdownDone := make(chan struct{})
			go func() {
				defer close(shutdownDone)
				<-ctx.Done()
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer shutCancel()
				if err := srv.Shutdown(shutCtx); err != nil {
					slog.Error("server shutdown error", "err", err)
					_ = srv.Close()
				}
			}()

			startupLog.Info("starting server", "addr", cfg.bindAddr)
			serveErr := srv.Serve(ln)
			// Also drain accepted data if serving stops unexpectedly.
			cancel()
			<-shutdownDone
			// Deliver passthrough envelopes already accepted from SDKs.
			forwardCtx, forwardCancel := context.WithTimeout(context.Background(), 15*time.Second)
			if err := handler.Close(forwardCtx); err != nil {
				slog.Warn("passthrough queue not drained before shutdown", "err", err)
			}
			forwardCancel()
			// Reject late pushes even if HTTP shutdown reached its deadline.
			buf.StopAccepting()
			txBuf.StopAccepting()
			logBuf.StopAccepting()
			profBuf.StopAccepting()
			stopWriters()
			<-bufDone
			<-txBufDone
			<-logBufDone
			<-profBufDone
			// Background jobs stop with ctx; let them finish before the
			// deferred pool.Close so they never hit a closed pool.
			waitJobs()
			if serveErr != nil && serveErr != http.ErrServerClosed {
				return fmt.Errorf("serve: %w", serveErr)
			}
			return nil
		},
	}
}

func listen(addr string, socketMode fs.FileMode) (net.Listener, error) {
	lc := &net.ListenConfig{}
	if path, ok := strings.CutPrefix(addr, "unix:"); ok {
		// Remove stale socket from a previous run.
		_ = os.Remove(path)
		ln, err := lc.Listen(context.Background(), "unix", path)
		if err != nil {
			return nil, err
		}
		// Default 0660: owner + group (e.g. www-data) can connect; nothing else.
		// Use SOCKET_MODE=0666 when the socket is bind-mounted out of a Docker
		// container, where UID/GID matching with the host nginx is impractical.
		if err := os.Chmod(path, socketMode); err != nil {
			_ = ln.Close()
			return nil, err
		}
		startupLog.Info("unix socket ready", "path", path, "mode", fmt.Sprintf("%04o", socketMode))
		return ln, nil
	}
	return lc.Listen(context.Background(), "tcp", addr)
}

func sendDigestCmd(cfg config) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "send-digest",
		Short: "Send the weekly digest email to all opted-in users immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := storage.Connect(cmd.Context(), cfg.databaseURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer pool.Close()

			emailSender, err := alerts.NewEmailSenderFromEnv()
			if err != nil {
				return fmt.Errorf("email sender: %w", err)
			}
			if emailSender == nil {
				return fmt.Errorf("EMAIL_PROVIDER is not configured")
			}

			digest.NewWorker(pool, emailSender, cfg.publicURL).SendNow(cmd.Context(), force)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "send to all opted-in users, ignoring the 7-day cooldown")
	return cmd
}

// newHTTPServer applies connection-level timeouts. There is deliberately no
// server-wide ReadTimeout: sourcemap uploads can be large, so body read
// deadlines are set per route (envelopes, auth and cron bodies) instead.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Close idle keep-alive connections so they cannot pile up.
		IdleTimeout: 120 * time.Second,
	}
}
