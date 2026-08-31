package alerts_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/alerts"
)

func TestNewWebhookClient_blocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := alerts.NewWebhookClient(false)
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Error("expected error: loopback address should be blocked when allowPrivate=false")
	}
}

func TestNewWebhookClient_allowsLoopbackWhenAllowPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := alerts.NewWebhookClient(true)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected success with allowPrivate=true, got %v", err)
	}
	resp.Body.Close()
}

func TestNewWebhookClient_userAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := alerts.NewWebhookClient(true)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	resp.Body.Close()
	if !strings.HasPrefix(gotUA, "Tindra/") {
		t.Errorf("expected Tindra/ User-Agent, got %q", gotUA)
	}
}

func TestNewWebhookClient_dnsFailure(t *testing.T) {
	// .invalid is an IANA-reserved TLD guaranteed to never resolve.
	client := alerts.NewWebhookClient(false)
	_, err := client.Get("http://nonexistent-host.invalid/path")
	if err == nil {
		t.Error("expected error for unresolvable host")
	}
}

func TestNewWebhookClient_blocksNAT64(t *testing.T) {
	// Dial-time check must catch IPv4-in-IPv6 destinations, not only save-time validation.
	client := alerts.NewWebhookClient(false)
	_, err := client.Get("http://[64:ff9b::169.254.169.254]/hook")
	if err == nil {
		t.Error("expected NAT64 link-local destination to be blocked at dial time")
	}
}
