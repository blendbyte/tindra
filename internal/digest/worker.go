package digest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
)

// Worker sends weekly digest emails to opted-in users.
// It ticks hourly and fires only within the 07:00–09:00 UTC send window to
// ensure emails arrive at a predictable time regardless of when the server
// started.
type Worker struct {
	pool      *pgxpool.Pool
	email     alerts.EmailSender
	publicURL string
}

func NewWorker(pool *pgxpool.Pool, email alerts.EmailSender, publicURL string) *Worker {
	return &Worker{pool: pool, email: email, publicURL: publicURL}
}

func (w *Worker) Run(ctx context.Context) {
	if w.email == nil {
		slog.Info("digest: email not configured, weekly digest disabled")
		return
	}

	// Check immediately on startup in case we're within the send window and
	// users have pending digests, then tick hourly.
	w.sendDue(ctx)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sendDue(ctx)
		}
	}
}

// SendNow sends the digest immediately, bypassing the time window. Pass
// force=true to also bypass the 7-day cooldown and send to all opted-in users.
func (w *Worker) SendNow(ctx context.Context, force bool) {
	w.send(ctx, force)
}

func (w *Worker) sendDue(ctx context.Context) {
	hour := time.Now().UTC().Hour()
	if hour < 7 || hour >= 9 {
		return
	}
	w.send(ctx, false)
}

func (w *Worker) send(ctx context.Context, force bool) {
	if w.pool == nil {
		return
	}
	users, err := storage.ListDigestDueUsers(ctx, w.pool, force)
	if err != nil {
		slog.Error("digest: list due users", "err", err)
		return
	}
	if len(users) == 0 {
		return
	}

	projects, err := storage.ListProjects(ctx, w.pool)
	if err != nil {
		slog.Error("digest: list projects", "err", err)
		return
	}
	if len(projects) == 0 {
		return
	}

	projectIDs := make([]string, len(projects))
	for i, p := range projects {
		projectIDs[i] = p.ID
	}

	now := time.Now().UTC()
	from := now.Truncate(24*time.Hour).AddDate(0, 0, -6)

	report, err := w.buildReport(ctx, projectIDs, from, now)
	if err != nil {
		slog.Error("digest: build report", "err", err)
		return
	}

	for _, u := range users {
		report.UserName = u.Name
		if report.UserName == "" {
			report.UserName = u.Email
		}

		subject, html, text, err := RenderDigestEmail(report)
		if err != nil {
			slog.Error("digest: render email", "user", u.ID, "err", err)
			continue
		}

		if err := w.email.Send(ctx, alerts.EmailMessage{
			To:      u.Email,
			Subject: subject,
			HTML:    html,
			Text:    text,
		}); err != nil {
			slog.Error("digest: send email", "user", u.ID, "err", err)
			continue
		}

		if err := storage.MarkDigestSent(ctx, w.pool, u.ID); err != nil {
			slog.Error("digest: mark sent", "user", u.ID, "err", err)
		}

		slog.Info("digest: sent", "user", u.ID, "email", u.Email)
	}
}

func (w *Worker) buildReport(ctx context.Context, projectIDs []string, from, to time.Time) (*Report, error) {
	if w.pool == nil {
		return nil, fmt.Errorf("no database pool")
	}
	var (
		errs []error
		r    = &Report{
			From:      from,
			To:        to,
			PublicURL: w.publicURL,
		}
	)

	var err error

	r.DailyErrors, err = dailyErrorCounts(ctx, w.pool, projectIDs, from, to)
	errs = append(errs, err)

	r.DailyTx, err = dailyTxCounts(ctx, w.pool, projectIDs, from, to)
	errs = append(errs, err)

	r.Projects, err = projectBreakdown(ctx, w.pool, projectIDs, from, to)
	errs = append(errs, err)

	r.Issues, err = issuesSummary(ctx, w.pool, projectIDs, from, to)
	errs = append(errs, err)

	r.TopIssues, err = topIssues(ctx, w.pool, projectIDs, from, to, 5)
	errs = append(errs, err)

	r.TopTx, err = topTransactions(ctx, w.pool, projectIDs, from, to, 5)
	errs = append(errs, err)

	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}

	for _, s := range r.DailyErrors {
		r.TotalErrors += s.Count
	}
	for _, s := range r.DailyTx {
		r.TotalTx += s.Count
	}

	return r, nil
}
