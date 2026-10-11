package ingest

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// forwardTimeout bounds one upstream delivery, independent of the client's own
// timeout, so a stalled upstream cannot hold a worker indefinitely.
const forwardTimeout = 10 * time.Second

// Forwarder ships envelopes to upstream Sentry DSNs (passthrough) from a fixed
// pool of workers. Enqueue never blocks ingestion: when upstream is slow and
// the queue is full, envelopes are dropped and counted instead of piling up
// goroutines and memory. Close drains the queue on shutdown.
type Forwarder struct {
	client *http.Client
	jobs   chan forwardJob
	wg     sync.WaitGroup

	mu     sync.RWMutex // guards closed against sends on a closed channel
	closed bool

	dropped     atomic.Int64
	lastDropLog atomic.Int64 // unix seconds
}

type forwardJob struct {
	dsn  string
	body []byte
}

// NewForwarder starts workers that deliver queued envelopes with client. The
// caller is responsible for a client with SSRF protections.
func NewForwarder(client *http.Client, workers, queueSize int) *Forwarder {
	f := &Forwarder{client: client, jobs: make(chan forwardJob, queueSize)}
	for range workers {
		f.wg.Go(func() {
			for job := range f.jobs {
				ctx, cancel := context.WithTimeout(context.Background(), forwardTimeout)
				if err := forwardEnvelope(ctx, f.client, job.dsn, job.body); err != nil {
					slog.Warn("passthrough forward failed", "dsn_host", dsnHost(job.dsn), "err", err)
				}
				cancel()
			}
		})
	}
	return f
}

// Enqueue queues an envelope for upstream delivery. It returns false, dropping
// the envelope, when the queue is full or the forwarder is closed.
func (f *Forwarder) Enqueue(dsn string, envelope []byte) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.closed {
		select {
		case f.jobs <- forwardJob{dsn: dsn, body: envelope}:
			return true
		default:
		}
	}
	total := f.dropped.Add(1)
	if now := time.Now().Unix(); now-f.lastDropLog.Load() >= 60 {
		f.lastDropLog.Store(now)
		slog.Warn("passthrough queue full, dropping envelopes", "dropped_total", total)
	}
	return false
}

// Dropped reports how many envelopes were dropped because the queue was full.
func (f *Forwarder) Dropped() int64 { return f.dropped.Load() }

// Close stops accepting envelopes and waits for queued ones to be delivered,
// or until ctx ends.
func (f *Forwarder) Close(ctx context.Context) error {
	f.mu.Lock()
	if !f.closed {
		f.closed = true
		close(f.jobs)
	}
	f.mu.Unlock()
	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// upstreamEndpoint derives the envelope endpoint and public key from a DSN.
// The project ID is the last path segment; anything before it is a path prefix
// (Sentry behind a sub-path), which goes before /api/.
func upstreamEndpoint(dsnStr string) (endpoint, publicKey string, err error) {
	u, err := url.Parse(dsnStr)
	if err != nil {
		return "", "", fmt.Errorf("parse DSN: %w", err)
	}
	publicKey = u.User.Username()
	if publicKey == "" {
		return "", "", fmt.Errorf("DSN missing public key")
	}
	path := strings.Trim(u.Path, "/")
	prefix, projectID := "", path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		prefix, projectID = "/"+path[:i], path[i+1:]
	}
	if projectID == "" {
		return "", "", fmt.Errorf("DSN missing project ID")
	}
	return fmt.Sprintf("%s://%s%s/api/%s/envelope/", u.Scheme, u.Host, prefix, projectID), publicKey, nil
}

func forwardEnvelope(ctx context.Context, client *http.Client, dsnStr string, raw []byte) error {
	endpoint, publicKey, err := upstreamEndpoint(dsnStr)
	if err != nil {
		return err
	}

	// Rewrite the envelope header's dsn field to match the upstream DSN.
	// Relay validates that the dsn in the envelope header matches the
	// authenticated key; forwarding the original Tindra DSN causes a 400.
	body, err := rewriteEnvelopeDSN(raw, dsnStr)
	if err != nil {
		return fmt.Errorf("rewrite envelope header: %w", err)
	}

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(body); err != nil {
		return fmt.Errorf("compress: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("compress: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &compressed)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7, sentry_key=%s", publicKey))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// rewriteEnvelopeDSN replaces (or adds) the dsn field in the envelope header
// line with upstreamDSN, leaving all item lines unchanged.
func rewriteEnvelopeDSN(raw []byte, upstreamDSN string) ([]byte, error) {
	reader := bufio.NewReader(bytes.NewReader(raw))
	headerLine, err := reader.ReadBytes('\n')
	if err != nil && len(headerLine) == 0 {
		return nil, fmt.Errorf("read header line: %w", err)
	}

	var hdr map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimRight(headerLine, "\r\n"), &hdr); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if hdr == nil {
		return nil, fmt.Errorf("envelope header must be a JSON object")
	}
	dsnJSON, _ := json.Marshal(upstreamDSN)
	hdr["dsn"] = dsnJSON

	newHeader, err := json.Marshal(hdr)
	if err != nil {
		return nil, fmt.Errorf("marshal header: %w", err)
	}

	rest, _ := io.ReadAll(reader)

	var out bytes.Buffer
	out.Write(newHeader)
	out.WriteByte('\n')
	out.Write(rest)
	return out.Bytes(), nil
}

func dsnHost(dsnStr string) string {
	u, err := url.Parse(dsnStr)
	if err != nil {
		return dsnStr
	}
	return u.Host
}
