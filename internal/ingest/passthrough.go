package ingest

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// ForwardEnvelope ships a raw (decompressed) envelope to the passthrough DSN in a fire-and-forget
// goroutine. Errors are logged but never surfaced to the ingest caller.
// The caller is responsible for providing a client with appropriate SSRF protections.
func ForwardEnvelope(client *http.Client, dsnStr string, raw []byte) {
	go func() {
		if err := forwardEnvelope(client, dsnStr, raw); err != nil {
			slog.Warn("passthrough forward failed", "dsn_host", dsnHost(dsnStr), "err", err)
		}
	}()
}

func forwardEnvelope(client *http.Client, dsnStr string, raw []byte) error {
	u, err := url.Parse(dsnStr)
	if err != nil {
		return fmt.Errorf("parse DSN: %w", err)
	}
	publicKey := u.User.Username()
	if publicKey == "" {
		return fmt.Errorf("DSN missing public key")
	}
	projectID := strings.Trim(u.Path, "/")
	if projectID == "" {
		return fmt.Errorf("DSN missing project ID")
	}

	endpoint := fmt.Sprintf("%s://%s/api/%s/envelope/", u.Scheme, u.Host, projectID)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7, sentry_key=%s", publicKey))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream returned %d", resp.StatusCode)
	}
	return nil
}

func dsnHost(dsnStr string) string {
	u, err := url.Parse(dsnStr)
	if err != nil {
		return dsnStr
	}
	return u.Host
}
