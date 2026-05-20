package main

import (
	"context"
	"fmt"
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
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/digest"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/issues"
	"github.com/blendbyte/tindra/internal/retention"
	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
)

// Version and Commit are injected at build time via -ldflags.
var Version = "dev"
var Commit = "unknown"

type config struct {
	databaseURL            string
	bindAddr               string
	publicURL              string
	statsAPIKey            string
	billingURL             string
	logLevel               string
	logFormat              string
	ingestBufferSize       int
	dataDir                string
	retentionDays          int
	cookieSecure           bool
	corsOrigin             string
	projectLimit           int
	eventLimit             int
	userLimit              int
	rateLimitLogin         int // max login attempts per 15 min per IP; 0 = disabled
	rateLimitEnvelope      int // max envelope requests per minute per project; 0 = disabled
	webhookAllowPrivateIPs bool
	trustedProxies         []*net.IPNet
	skipAutoMigrate        bool
}

func main() {
	// Load .env if present - no-op in production where env vars are set externally.
	_ = godotenv.Load()

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
		serveCmd(cfg),
		migrateCmd(cfg),
		projectsCmd(cfg),
		usersCmd(cfg),
		sendDigestCmd(cfg),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func loadConfig() config {
	bufSize := 10000
	if s := strings.TrimSpace(os.Getenv("INGEST_BUFFER_SIZE")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			bufSize = n
		}
	}
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
	retentionDays := 90
	if s := strings.TrimSpace(os.Getenv("RETENTION_DAYS")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			retentionDays = n
		}
	}
	projectLimit := 0
	if s := strings.TrimSpace(os.Getenv("PROJECT_LIMIT")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			projectLimit = n
		}
	}
	eventLimit := 0
	if s := strings.TrimSpace(os.Getenv("EVENT_LIMIT")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			eventLimit = n
		}
	}
	userLimit := 0
	if s := strings.TrimSpace(os.Getenv("USER_LIMIT")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			userLimit = n
		}
	}
	rateLimitLogin := 10
	if s := strings.TrimSpace(os.Getenv("RATE_LIMIT_LOGIN")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			rateLimitLogin = n
		}
	}
	rateLimitEnvelope := 300
	if s := strings.TrimSpace(os.Getenv("RATE_LIMIT_ENVELOPE")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			rateLimitEnvelope = n
		}
	}
	var trustedProxies []*net.IPNet
	if s := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); s != "" {
		for _, raw := range strings.Split(s, ",") {
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
		databaseURL:            os.Getenv("DATABASE_URL"),
		bindAddr:               bindAddr,
		logLevel:               logLevel,
		logFormat:              os.Getenv("LOG_FORMAT"),
		ingestBufferSize:       bufSize,
		dataDir:                dataDir,
		retentionDays:          retentionDays,
		cookieSecure:           os.Getenv("COOKIE_SECURE") == "true",
		corsOrigin:             os.Getenv("CORS_ORIGIN"),
		publicURL:              os.Getenv("PUBLIC_URL"),
		statsAPIKey:            os.Getenv("STATS_API_KEY"),
		billingURL:             strings.TrimSpace(os.Getenv("BILLING_URL")),
		projectLimit:           projectLimit,
		eventLimit:             eventLimit,
		userLimit:              userLimit,
		rateLimitLogin:         rateLimitLogin,
		rateLimitEnvelope:      rateLimitEnvelope,
		webhookAllowPrivateIPs: os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true",
		trustedProxies:         trustedProxies,
		skipAutoMigrate:        os.Getenv("SKIP_AUTO_MIGRATE") == "true",
	}
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
}

func serveCmd(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

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
					slog.Info("database migrations applied", "from", before, "to", after)
				} else {
					slog.Info("database schema up to date", "version", after)
				}
			}

			buf := ingest.NewBuffer(cfg.ingestBufferSize)
			bufDone := make(chan struct{})
			go func() {
				defer close(bufDone)
				buf.Run(ctx, pool)
			}()

			txBuf := ingest.NewTransactionBuffer(cfg.ingestBufferSize)
			txBuf.Hook = issues.NewN1Detector(pool).ProcessBatch
			txBufDone := make(chan struct{})
			go func() {
				defer close(txBufDone)
				txBuf.Run(ctx, pool)
			}()

			logBuf := ingest.NewLogBuffer(cfg.ingestBufferSize)
			logBufDone := make(chan struct{})
			go func() {
				defer close(logBufDone)
				logBuf.Run(ctx, pool)
			}()

			grouper := issues.NewGrouper(pool)
			go grouper.Run(ctx)

			emailSender, err := alerts.NewEmailSenderFromEnv()
			if err != nil {
				return fmt.Errorf("email sender: %w", err)
			}
			evaluator := alerts.NewEvaluator(pool, emailSender, cfg.publicURL, cfg.webhookAllowPrivateIPs)
			go evaluator.Run(ctx)

			api.AppEmailSender = emailSender

			smStore := sourcemaps.NewStore(cfg.dataDir, pool)
			oauthProviders := api.LoadOAuthProviders(ctx)

			go retention.NewWorker(pool, cfg.retentionDays).Run(ctx)
			go digest.NewWorker(pool, emailSender, cfg.publicURL).Run(ctx)

			go func() {
				ticker := time.NewTicker(60 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						n, err := storage.ExpireIgnoredIssues(ctx, pool)
						if err != nil {
							slog.Error("expire ignored issues", "err", err)
						} else if n > 0 {
							slog.Info("expired ignored issues", "count", n)
						}
					}
				}
			}()

			slog.Info("starting tindra",
				"version", Version,
				"commit", Commit,
				"addr", cfg.bindAddr,
				"project_limit", cfg.projectLimit,
				"event_limit", cfg.eventLimit,
				"user_limit", cfg.userLimit,
				"public_url", cfg.publicURL,
				"rate_limit_login", cfg.rateLimitLogin,
				"rate_limit_envelope", cfg.rateLimitEnvelope,
			)

			handler := api.NewRouter(pool, buf, txBuf, logBuf, smStore, oauthProviders, cfg.cookieSecure, cfg.corsOrigin, cfg.publicURL, cfg.statsAPIKey, cfg.billingURL, cfg.retentionDays, cfg.projectLimit, cfg.eventLimit, cfg.userLimit, cfg.rateLimitLogin, cfg.rateLimitEnvelope, evaluator, cfg.webhookAllowPrivateIPs, cfg.trustedProxies)

			sighup := make(chan os.Signal, 1)
			signal.Notify(sighup, syscall.SIGHUP)
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-sighup:
						_ = godotenv.Overload()
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

			srv := &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: 10 * time.Second,
			}

			ln, err := listen(cfg.bindAddr)
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}

			go func() {
				<-ctx.Done()
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer shutCancel()
				if err := srv.Shutdown(shutCtx); err != nil {
					slog.Error("server shutdown error", "err", err)
				}
			}()

			slog.Info("starting server", "addr", cfg.bindAddr)
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				return fmt.Errorf("serve: %w", err)
			}
			<-bufDone
			<-txBufDone
			<-logBufDone
			return nil
		},
	}
}

func listen(addr string) (net.Listener, error) {
	if strings.HasPrefix(addr, "unix:") {
		path := strings.TrimPrefix(addr, "unix:")
		// Remove stale socket from a previous run.
		_ = os.Remove(path)
		ln, err := net.Listen("unix", path)
		if err != nil {
			return nil, err
		}
		// 0660 lets the tindra process owner and group-members (e.g. www-data) connect.
		if err := os.Chmod(path, 0660); err != nil {
			_ = ln.Close()
			return nil, err
		}
		return ln, nil
	}
	return net.Listen("tcp", addr)
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
