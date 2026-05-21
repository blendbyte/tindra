package ingest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	// Rewrite the envelope header's dsn field to match the upstream DSN.
	// Relay validates that the dsn in the envelope header matches the
	// authenticated key; forwarding the original Tindra DSN causes a 400.
	body, err := rewriteEnvelopeDSN(raw, dsnStr)
	if err != nil {
		return fmt.Errorf("rewrite envelope header: %w", err)
	}

	endpoint := fmt.Sprintf("%s://%s/api/%s/envelope/", u.Scheme, u.Host, projectID)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
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
	if err := json.Unmarshal(bytes.TrimRight(headerLine, "\n"), &hdr); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
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
