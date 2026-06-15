// Package uptime implements outbound HTTP probing for uptime monitors.
package uptime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

const (
	maxConcurrent = 20
	tickInterval  = 30 * time.Second
	bodyReadLimit = 1 << 20 // 1 MB
)

// Worker periodically fetches due uptime monitors and probes each URL.
type Worker struct {
	pool   *pgxpool.Pool
	client *http.Client
	sem    chan struct{}
}

func NewWorker(pool *pgxpool.Pool) *Worker {
	return &Worker{
		pool: pool,
		client: &http.Client{
			Timeout: 70 * time.Second, // hard cap well above any per-monitor timeout
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		sem: make(chan struct{}, maxConcurrent),
	}
}

// Run starts the probe loop. Call in a dedicated goroutine.
// Runs one tick immediately on startup, then every tickInterval.
func (w *Worker) Run(ctx context.Context) {
	w.tick(ctx)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// RunOnce runs a single probe cycle. Exported for tests.
func (w *Worker) RunOnce(ctx context.Context) {
	w.tick(ctx)
}

func (w *Worker) tick(ctx context.Context) {
	monitors, err := storage.GetDueUptimeMonitors(ctx, w.pool)
	if err != nil {
		slog.Error("uptime: get due monitors", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, m := range monitors {
		select {
		case w.sem <- struct{}{}:
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-w.sem }()
				w.probe(ctx, m)
			}()
		case <-ctx.Done():
			wg.Wait()
			return
		}
	}
	wg.Wait()
}

func (w *Worker) probe(ctx context.Context, m *storage.UptimeMonitor) {
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutSecs)*time.Second)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(probeCtx, m.Method, m.URL, nil)
	if err != nil {
		errStr := fmt.Sprintf("build request: %v", err)
		w.record(ctx, m, "down", nil, nil, errStr)
		return
	}
	req.Header.Set("User-Agent", "Tindra-Uptime/1.0")

	resp, err := w.client.Do(req)
	responseMs := int(time.Since(start).Milliseconds())

	if err != nil {
		errStr := err.Error()
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(errStr, "context deadline exceeded") {
			errStr = "timeout"
		}
		w.record(ctx, m, "down", nil, &responseMs, errStr)
		return
	}
	defer resp.Body.Close()

	// Check status code against allowed set
	allowed, parseErr := storage.ParseExpectedCodes(m.ExpectedCodes)
	if parseErr != nil {
		// Misconfigured monitor: treat as down so the user notices
		errStr := fmt.Sprintf("invalid expected_codes: %v", parseErr)
		w.record(ctx, m, "down", &resp.StatusCode, &responseMs, errStr)
		return
	}
	allowedSet := make(map[int]struct{}, len(allowed))
	for _, c := range allowed {
		allowedSet[c] = struct{}{}
	}

	statusCode := resp.StatusCode
	if _, ok := allowedSet[statusCode]; !ok {
		w.record(ctx, m, "down", &statusCode, &responseMs, "")
		return
	}

	// Body check: only applies to GET requests
	if m.Method == "GET" && m.BodyContains != nil && *m.BodyContains != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, bodyReadLimit))
		if err != nil {
			errStr := fmt.Sprintf("read body: %v", err)
			w.record(ctx, m, "down", &statusCode, &responseMs, errStr)
			return
		}
		if !strings.Contains(string(body), *m.BodyContains) {
			errStr := fmt.Sprintf("body does not contain %q", *m.BodyContains)
			w.record(ctx, m, "down", &statusCode, &responseMs, errStr)
			return
		}
	}

	w.record(ctx, m, "up", &statusCode, &responseMs, "")
}

func (w *Worker) record(ctx context.Context, m *storage.UptimeMonitor, status string, statusCode *int, responseMs *int, errStr string) {
	check := &storage.UptimeCheck{
		Status:     status,
		StatusCode: statusCode,
		ResponseMs: responseMs,
	}
	if errStr != "" {
		check.Error = &errStr
	}
	if _, err := storage.RecordUptimeCheck(ctx, w.pool, m.ID, check); err != nil {
		slog.Error("uptime: record check", "monitor_id", m.ID, "err", err)
	}
}
