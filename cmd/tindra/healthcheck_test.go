package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serveHealth(t *testing.T, ln net.Listener, status int) {
	t.Helper()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

func TestCheckHealth_tcp(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveHealth(t, ln, http.StatusOK)
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// A wildcard BIND_ADDR is checked on loopback.
	for _, addr := range []string{":" + port, "0.0.0.0:" + port, "127.0.0.1:" + port} {
		if err := checkHealth(t.Context(), addr); err != nil {
			t.Errorf("checkHealth(%q) = %v, want nil", addr, err)
		}
	}
}

func TestCheckHealth_unixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tindra.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	serveHealth(t, ln, http.StatusOK)
	if err := checkHealth(t.Context(), "unix:"+path); err != nil {
		t.Fatalf("checkHealth = %v, want nil", err)
	}
}

func TestCheckHealth_failures(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveHealth(t, ln, http.StatusServiceUnavailable)

	closed := httptest.NewUnstartedServer(nil)
	closedAddr := closed.Listener.Addr().String()
	closed.Listener.Close()

	cases := map[string]string{
		ln.Addr().String(): "503",
		closedAddr:         "health check",
		"not-an-address":   "parse BIND_ADDR",
	}
	for addr, want := range cases {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		err := checkHealth(ctx, addr)
		cancel()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("checkHealth(%q) = %v, want error mentioning %q", addr, err, want)
		}
	}
}
