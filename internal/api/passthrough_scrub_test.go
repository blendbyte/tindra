package api_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// Forwarded envelopes carry what Tindra stores: scrubbed payloads, and only
// item types Tindra itself ingests.
func TestPassthrough_forwardsScrubbedStoredItemsOnly(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var forwarded []byte
	// A real loopback listener: the forwarder uses its own SSRF-checked client,
	// which cannot reach NewTestServer's in-memory example.com host.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("forwarded body is not gzip: %v", err)
			return
		}
		b, _ := io.ReadAll(gz)
		mu.Lock()
		forwarded = b
		mu.Unlock()
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	dsn := fmt.Sprintf("%s://upstreamkey@%s/9", u.Scheme, u.Host)

	project, err := storage.CreateProject(ctx, testPool, "passthrough-"+uuid.NewString()[:8], "Passthrough")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", project.ID) })
	if _, err := storage.UpdateProject(ctx, testPool, project.ID, project.Name, project.Slug, &dsn, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.UpdateProjectScrubbing(ctx, testPool, project.ID, []string{"user.email"}, json.RawMessage(`[]`)); err != nil {
		t.Fatal(err)
	}

	// Loopback upstream: allow private IPs for this router only.
	h := api.NewRouter(api.Config{Pool: testPool, Events: ingest.NewBuffer(10), WebhookAllowPrivateIPs: true})

	event := `{"message":"checkout failed","user":{"email":"alice@example.com","id":"42"}}`
	attachment := "secret attachment bytes"
	body := `{"event_id":"8800e8400e29b41d4a716446655440aa"}` + "\n" +
		fmt.Sprintf(`{"type":"event","length":%d}`, len(event)) + "\n" + event + "\n" +
		fmt.Sprintf(`{"type":"attachment","length":%d,"filename":"dump.txt"}`, len(attachment)) + "\n" + attachment + "\n"
	req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(project.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	drain, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.Close(drain); err != nil {
		t.Fatalf("drain forwarder: %v", err)
	}

	mu.Lock()
	got := string(forwarded)
	mu.Unlock()
	if !strings.Contains(got, "checkout failed") {
		t.Fatalf("event was not forwarded: %q", got)
	}
	if strings.Contains(got, "alice@example.com") {
		t.Errorf("forwarded event was not scrubbed: %q", got)
	}
	if strings.Contains(got, attachment) || strings.Contains(got, `"attachment"`) {
		t.Errorf("attachment must not be forwarded: %q", got)
	}
	if !strings.Contains(got, `"dsn":"`+dsn+`"`) {
		t.Errorf("envelope header should carry the upstream DSN: %q", got)
	}
}
