package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/background"
	"github.com/blendbyte/tindra/internal/storage"
)

// expireSnoozesJob ends time-based snoozes once a minute.
func expireSnoozesJob(pool *pgxpool.Pool) func(context.Context) {
	return func(ctx context.Context) {
		background.Every(ctx, time.Minute, func(ctx context.Context) {
			n, err := storage.ExpireIgnoredIssues(ctx, pool)
			if err != nil {
				slog.Error("expire ignored issues", "err", err)
			} else if n > 0 {
				slog.Info("expired ignored issues", "count", n)
			}
		})
	}
}

// autoResolveJob resolves performance issues quiet for 14 days, at startup
// and then daily, and notifies alert rules about them.
func autoResolveJob(pool *pgxpool.Pool, evaluator *alerts.Evaluator) func(context.Context) {
	return func(ctx context.Context) {
		background.Every(ctx, 24*time.Hour, func(ctx context.Context) {
			resolved, err := storage.AutoResolvePerformanceIssues(ctx, pool, 14*24*time.Hour)
			if err != nil {
				slog.Error("auto-resolve performance issues", "err", err)
			} else if len(resolved) > 0 {
				slog.Info("auto-resolved performance issues", "count", len(resolved))
				evaluator.NotifyAutoResolved(ctx, resolved)
			}
		})
	}
}
