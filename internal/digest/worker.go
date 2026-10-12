package digest

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
)

// digestWindowStart and digestWindowHours define the Monday UTC send window.
// Users are spread evenly across the window by a stable hash of their ID.
const (
	digestWindowStart = 7 // 07:00 UTC
	digestWindowHours = 4 // 07:00–11:00 UTC
)

// userDigestSlot returns which hour-slot [0, digestWindowHours) a user belongs
// to, derived deterministically from their ID so it never changes.
func userDigestSlot(userID string) int {
	h := fnv.New32a()
	h.Write([]byte(userID))
	return int(h.Sum32() % digestWindowHours)
}

// Worker sends weekly digest emails to opted-in users.
// It ticks hourly and fires only within the Monday 07:00-11:00 UTC send window
// so emails arrive at a predictable time regardless of when the server started.
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
	now := time.Now().UTC()
	if now.Weekday() != time.Monday {
		return
	}
	slot := now.Hour() - digestWindowStart
	if slot < 0 || slot >= digestWindowHours {
		return
	}
	w.sendSlot(ctx, slot)
}

// sendSlot sends to due users whose slot is the current one or an earlier
// one. Earlier slots catch up users whose hour was missed (a restart, or the
// instance being down); users already sent this week are not due any more.
func (w *Worker) sendSlot(ctx context.Context, slot int) {
	if w.pool == nil {
		return
	}
	users, err := storage.ListDigestDueUsers(ctx, w.pool, false)
	if err != nil {
		slog.Error("digest: list due users", "err", err)
		return
	}
	var slotUsers []storage.DigestUser
	for _, u := range users {
		if userDigestSlot(u.ID) <= slot {
			slotUsers = append(slotUsers, u)
		}
	}
	if len(slotUsers) > 0 {
		w.sendToUsers(ctx, slotUsers)
	}
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
	w.sendToUsers(ctx, users)
}

func (w *Worker) sendToUsers(ctx context.Context, users []storage.DigestUser) {
	if len(users) == 0 {
		return
	}

	projects, err := storage.ListProjectMetadata(ctx, w.pool)
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

	from, to := reportWeek(time.Now().UTC())
	report, err := w.buildReport(ctx, projectIDs, from, to)
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

// reportWeek returns the last complete ISO week before now: Monday 00:00 UTC
// to the following Monday 00:00 UTC. A fixed calendar week means every day is
// covered exactly once, whatever hour the digest is sent.
func reportWeek(now time.Time) (from, to time.Time) {
	now = now.UTC()
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	to = time.Date(now.Year(), now.Month(), now.Day()-daysSinceMonday, 0, 0, 0, 0, time.UTC)
	return to.AddDate(0, 0, -7), to
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

	r.DailyErrors, r.DailyTx, r.Projects, err = reportVolume(ctx, w.pool, projectIDs, from, to)
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
