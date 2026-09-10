package api

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestAuthenticationRoutesBoundBodies(t *testing.T) {
	// Oversized requests must be rejected before any handler touches the database.
	h := NewRouter(nil, nil, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/auth/login"}, {"POST", "/api/auth/logout"}, {"POST", "/api/auth/mfa/verify"},
		{"POST", "/api/auth/invite/token/accept"}, {"POST", "/api/auth/password-reset/token"},
		{"GET", "/api/auth/invite/token"}, {"GET", "/api/auth/password-reset/token"},
		{"GET", "/api/auth/providers"}, {"GET", "/api/auth/google/redirect"}, {"GET", "/api/auth/google/callback"},
	} {
		for _, chunked := range []bool{false, true} {
			t.Run(route.method+route.path+fmt.Sprint(chunked), func(t *testing.T) {
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`+strings.Repeat(" ", maxAuthBodyBytes)))
				if chunked {
					req.ContentLength = -1
					req.TransferEncoding = []string{"chunked"}
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
				require.Empty(t, rec.Result().Cookies())
				require.Equal(t, "close", rec.Header().Get("Connection"))
			})
		}
	}
}

func TestAuthBodyLimitBoundaryAndReadFailure(t *testing.T) {
	for _, size := range []int{0, maxAuthBodyBytes, maxAuthBodyBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			called := false
			h := limitAuthBody(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Len(t, body, size)
				w.WriteHeader(http.StatusNoContent)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", size))))
			require.Equal(t, size <= maxAuthBodyBytes, called)
			if called {
				require.Equal(t, http.StatusNoContent, rec.Code)
			} else {
				require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
			}
		})
	}
	rec := httptest.NewRecorder()
	h := limitAuthBody(time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("failed read reached handler") }))
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", iotest.ErrReader(errors.New("read failure"))))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthBodySlowConnectionThroughLogger(t *testing.T) {
	var called atomic.Bool
	handler := slogRequestLogger(limitAuthBody(100 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Store(true) })))
	server := httptest.NewServer(handler)
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
	_, err = fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 100\r\n\r\n{")
	require.NoError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusRequestTimeout, response.StatusCode)
	require.True(t, response.Close)
	require.False(t, called.Load())
}

func TestAuthBodyDeadlineClearedForKeepAlive(t *testing.T) {
	handler := slogRequestLogger(limitAuthBody(50 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Processing is not subject to the body deadline.
		w.WriteHeader(http.StatusNoContent)
	})))
	server := httptest.NewServer(handler)
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
	reader := bufio.NewReader(conn)
	for range 2 {
		_, err = fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 2\r\n\r\n{}")
		require.NoError(t, err)
		response, err := http.ReadResponse(reader, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, response.StatusCode)
		require.NoError(t, response.Body.Close())
	}
}

type deadlineFailureWriter struct {
	*httptest.ResponseRecorder
	calls  int
	failAt int
}

func (w *deadlineFailureWriter) SetReadDeadline(time.Time) error {
	w.calls++
	if w.calls == w.failAt {
		return errors.New("deadline failed")
	}
	return nil
}
func TestAuthBodyDeadlineErrorsFailClosed(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			w := &deadlineFailureWriter{ResponseRecorder: httptest.NewRecorder(), failAt: failAt}
			h := limitAuthBody(time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("deadline error reached handler") }))
			h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader("{}")))
			require.Equal(t, http.StatusInternalServerError, w.Code)
		})
	}
}

func TestAuthBodyRejectsOversizedUploadWithoutWaitingForRest(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprint(chunked), func(t *testing.T) {
			var called atomic.Bool
			server := httptest.NewServer(slogRequestLogger(limitAuthBody(time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))))
			defer server.Close()
			conn, err := net.Dial("tcp", server.Listener.Addr().String())
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
			payload := strings.Repeat("x", maxAuthBodyBytes+1)
			wire := "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 100000\r\n\r\n" + payload
			if chunked {
				wire = fmt.Sprintf("POST / HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n", len(payload), payload)
			}
			// Leave the remaining advertised body (or final chunk) unsent.
			_, err = fmt.Fprint(conn, wire)
			require.NoError(t, err)
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
			require.True(t, response.Close)
			require.False(t, called.Load())
		})
	}
}

func TestAuthenticatedMFARoutesBoundBodies(t *testing.T) {
	pool := oauthDB(t)
	fixture := seedRecovery(t, pool)
	h := NewRouter(pool, nil, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/auth/mfa/setup"}, {"POST", "/api/auth/mfa/confirm"}, {"DELETE", "/api/auth/mfa"},
	} {
		t.Run(route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(strings.Repeat("x", maxAuthBodyBytes+1)))
			req.AddCookie(&http.Cookie{Name: "tindra_session", Value: fixture.sessions[0].Token})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
			require.Empty(t, rec.Result().Cookies())
			secret, err := storage.GetMFASecret(t.Context(), pool, fixture.user.ID)
			require.NoError(t, err)
			require.NotNil(t, secret)
			require.Equal(t, "JBSWY3DPEHPK3PXP", *secret)
			pending, err := storage.GetPendingMFASecret(t.Context(), pool, fixture.user.ID)
			require.NoError(t, err)
			require.NotNil(t, pending)
			require.Equal(t, "pending-secret", *pending)
		})
	}
}
