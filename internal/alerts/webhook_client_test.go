package alerts_test

import (
	"net/http"
	"net/http/httptest"
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
