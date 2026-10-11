package api

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// A client that stops sending mid-body gets a 408 once the read deadline passes,
// instead of holding the connection and its buffers open.
func TestHandleEnvelope_slowBodyTimesOut(t *testing.T) {
	pool := oauthDB(t)
	project, err := storage.CreateProject(t.Context(), pool, "slow-body-"+uuid.NewString()[:8], "Slow Body")
	require.NoError(t, err)

	old := envelopeReadTimeout
	envelopeReadTimeout = 200 * time.Millisecond
	t.Cleanup(func() { envelopeReadTimeout = old })

	srv := httptest.NewServer(http.HandlerFunc((&router{pool: pool}).handleEnvelope))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	// Declare a 1000-byte body but send only the first line, then stall.
	_, err = conn.Write([]byte("POST /api/1/envelope/ HTTP/1.1\r\n" +
		"Host: tindra.test\r\n" +
		"X-Sentry-Auth: Sentry sentry_version=7, sentry_key=" + project.PublicKey + "\r\n" +
		"Content-Length: 1000\r\n\r\n" +
		"{}\n"))
	require.NoError(t, err)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusRequestTimeout, resp.StatusCode)
}
