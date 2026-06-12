package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestListen_tcp(t *testing.T) {
	ln, err := listen(":0", 0)
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	if _, ok := ln.(*net.TCPListener); !ok {
		t.Errorf("expected *net.TCPListener, got %T", ln)
	}
}

func TestListen_tcp_reachable(t *testing.T) {
	ln, err := listen(":0", 0)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial tcp listener: %v", err)
	}
	conn.Close()
}

func TestListen_unix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tindra.sock")
	ln, err := listen("unix:"+path, 0660)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	if _, ok := ln.(*net.UnixListener); !ok {
		t.Errorf("expected *net.UnixListener, got %T", ln)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("socket file not created: %v", err)
	}
}

func TestListen_unix_permissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tindra.sock")
	ln, err := listen("unix:"+path, 0660)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0660 {
		t.Errorf("socket permissions: got %04o, want 0660", perm)
	}
}

func TestListen_unix_reachable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tindra.sock")
	ln, err := listen("unix:"+path, 0660)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial unix socket: %v", err)
	}
	conn.Close()
}

func TestListen_unix_removes_stale_socket(t *testing.T) {
	// Use os.MkdirTemp with a short prefix: macOS caps Unix socket paths at 104 chars
	// and t.TempDir() embeds the full test name which exceeds that limit.
	dir, err := os.MkdirTemp("", "ts")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "t.sock")

	// Create a first listener to leave a stale socket behind.
	ln1, err := listen("unix:"+path, 0660)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	ln1.Close()

	// Second listen must succeed despite the stale socket file.
	ln2, err := listen("unix:"+path, 0660)
	if err != nil {
		t.Fatalf("second listen (stale socket): %v", err)
	}
	ln2.Close()
}

func TestListen_unix_serves_http(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tindra.sock")
	ln, err := listen("unix:"+path, 0660)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	transport := &http.Transport{
		Dial: func(_, _ string) (net.Conn, error) {
			return net.Dial("unix", path)
		},
	}
	client := &http.Client{Transport: transport}

	resp, err := client.Get("http://unix/healthz")
	if err != nil {
		t.Fatalf("http over unix socket: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestParseSocketMode_default(t *testing.T) {
	if got := parseSocketMode("", 0660); got != 0660 {
		t.Errorf("empty string: got %04o, want 0660", got)
	}
}

func TestParseSocketMode_valid(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  uint32
	}{
		{"0666", 0666},
		{"0660", 0660},
		{"0600", 0600},
		{"666", 0666},
	} {
		got := parseSocketMode(tc.input, 0660)
		if uint32(got) != tc.want {
			t.Errorf("input %q: got %04o, want %04o", tc.input, got, tc.want)
		}
	}
}

func TestParseSocketMode_invalid(t *testing.T) {
	// Invalid input should return the default, not panic.
	if got := parseSocketMode("notoctal", 0660); got != 0660 {
		t.Errorf("invalid: got %04o, want 0660 (default)", got)
	}
}

func TestListen_unix_socketMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode.sock")
	ln, err := listen("unix:"+path, 0666)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0666 {
		t.Errorf("socket perm: got %04o, want 0666", got)
	}
}
