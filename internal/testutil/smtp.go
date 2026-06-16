package testutil

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// FakeSMTPServer is a minimal SMTP server for use in integration tests.
// It accepts plain-text SMTP connections on a random loopback port, stores
// received message bodies, and can optionally reject specific commands to
// exercise error-handling paths in callers.
type FakeSMTPServer struct {
	mu            sync.Mutex
	received      []string
	ln            net.Listener
	advertiseAuth bool
	// FailCmd, if set, causes the server to reject that SMTP verb with
	// "550 rejected". Recognised values: "MAIL", "RCPT", "DATA", "AUTH".
	FailCmd string
	// AdvertiseSTARTTLS, if set, includes STARTTLS in the EHLO banner.
	// The server responds "220 Ready" and immediately closes the connection,
	// so the client's TLS handshake fails — useful for testing the starttls
	// error path in callers.
	AdvertiseSTARTTLS bool
}

// NewFakeSMTP starts a minimal SMTP server on a random loopback port and
// registers a cleanup function that closes it when t finishes. Set
// advertiseAuth to true to advertise AUTH PLAIN LOGIN in the EHLO banner.
func NewFakeSMTP(t *testing.T, advertiseAuth bool) *FakeSMTPServer {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testutil.NewFakeSMTP: listen: %v", err)
	}
	s := &FakeSMTPServer{ln: ln, advertiseAuth: advertiseAuth}
	go s.serve()
	t.Cleanup(s.Close)
	return s
}

// NewFakeSMTPFailAt is like NewFakeSMTP but rejects the given SMTP verb with
// "550 rejected". Supported values: "MAIL", "RCPT", "DATA", "AUTH".
func NewFakeSMTPFailAt(t *testing.T, failCmd string) *FakeSMTPServer {
	t.Helper()
	s := NewFakeSMTP(t, false)
	s.FailCmd = failCmd
	return s
}

// NewFakeSMTPWithSTARTTLS starts a fake SMTP server that advertises STARTTLS
// in its EHLO banner. When the client sends STARTTLS the server responds
// "220 Ready" and closes the connection so the client's TLS handshake fails,
// exercising the starttls error return in callers.
func NewFakeSMTPWithSTARTTLS(t *testing.T) *FakeSMTPServer {
	t.Helper()
	s := NewFakeSMTP(t, false)
	s.AdvertiseSTARTTLS = true
	return s
}

// Close shuts down the server listener.
func (s *FakeSMTPServer) Close() { s.ln.Close() }

// Host returns the loopback address the server is listening on.
func (s *FakeSMTPServer) Host() string {
	host, _, _ := net.SplitHostPort(s.ln.Addr().String())
	return host
}

// Port returns the TCP port the server is listening on.
func (s *FakeSMTPServer) Port() int {
	_, p, _ := net.SplitHostPort(s.ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return port
}

// Received returns a snapshot of all message bodies received so far.
func (s *FakeSMTPServer) Received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]string, len(s.received))
	copy(cp, s.received)
	return cp
}

func (s *FakeSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *FakeSMTPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	bw := bufio.NewWriter(conn)
	br := bufio.NewReader(conn)

	write := func(line string) {
		fmt.Fprintf(bw, "%s\r\n", line)
		bw.Flush()
	}
	read := func() string {
		line, _ := br.ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}

	write("220 localhost ESMTP")

	for {
		line := read()
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			ehlo := "250-localhost Hello\r\n"
			if s.advertiseAuth {
				ehlo += "250-AUTH PLAIN LOGIN\r\n"
			}
			if s.AdvertiseSTARTTLS {
				ehlo += "250-STARTTLS\r\n"
			}
			ehlo += "250 OK\r\n"
			fmt.Fprint(bw, ehlo)
			bw.Flush()
		case upper == "STARTTLS":
			write("220 Ready to start TLS")
			return // drop connection so client's TLS handshake fails
		case strings.HasPrefix(upper, "AUTH"):
			if s.FailCmd == "AUTH" {
				write("535 5.7.8 Authentication credentials invalid")
			} else {
				write("235 Authentication successful")
			}
		case strings.HasPrefix(upper, "MAIL FROM"):
			if s.FailCmd == "MAIL" {
				write("550 rejected")
			} else {
				write("250 OK")
			}
		case strings.HasPrefix(upper, "RCPT TO"):
			if s.FailCmd == "RCPT" {
				write("550 rejected")
			} else {
				write("250 OK")
			}
		case upper == "DATA":
			if s.FailCmd == "DATA" {
				write("550 rejected")
			} else {
				write("354 Start mail input; end with <CRLF>.<CRLF>")
				var body strings.Builder
				for {
					dl := read()
					if dl == "." {
						break
					}
					body.WriteString(dl)
					body.WriteByte('\n')
				}
				s.mu.Lock()
				s.received = append(s.received, body.String())
				s.mu.Unlock()
				write("250 OK")
			}
		case upper == "QUIT":
			write("221 Bye")
			return
		default:
			if upper == "" {
				return // connection closed
			}
			write("500 Command not recognized")
		}
	}
}
