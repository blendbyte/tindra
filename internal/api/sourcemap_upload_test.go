package api_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/sourcemaps"
)

func TestSourcemapUploadLimits(t *testing.T) {
	const valid = `{"version":3,"sources":["app.ts"],"mappings":"AAAA"}`
	for _, tc := range []struct {
		name    string
		size    int
		extra   bool
		invalid bool
		status  int
	}{
		{name: "exact file limit", size: sourcemaps.MaxUploadSize, status: http.StatusCreated},
		{name: "file over limit", size: sourcemaps.MaxUploadSize + 1, status: http.StatusRequestEntityTooLarge},
		{name: "request over limit", size: sourcemaps.MaxUploadSize, extra: true, status: http.StatusRequestEntityTooLarge},
		{name: "invalid map", invalid: true, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Unknown content length exercises the streaming cap rather than trusting headers.
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			h, _ := smHandler(t)
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			require.NoError(t, mw.WriteField("release", t.Name()))
			require.NoError(t, mw.WriteField("url", "~/app.js"))
			fw, err := mw.CreateFormFile("file", "app.js.map")
			require.NoError(t, err)
			data := `{"version":3`
			if !tc.invalid {
				data = valid + strings.Repeat(" ", tc.size-len(valid))
			}
			_, err = fw.Write([]byte(data))
			require.NoError(t, err)
			if tc.extra {
				require.NoError(t, mw.WriteField("extra", strings.Repeat("x", 1<<20)))
			}
			require.NoError(t, mw.Close())
			req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/sourcemaps", &body)
			req.ContentLength = -1
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req.AddCookie(authCookie())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			// Multipart parsing must not leave temporary upload files behind.
			files, err := os.ReadDir(tmp)
			require.NoError(t, err)
			for _, file := range files {
				require.False(t, strings.HasPrefix(file.Name(), "multipart-"), file.Name())
			}
		})
	}
}

func TestSourcemapUploadMalformedRequests(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing file", true: "malformed multipart"}[malformed], func(t *testing.T) {
			h, _ := smHandler(t)
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			require.NoError(t, mw.WriteField("release", t.Name()))
			require.NoError(t, mw.WriteField("url", "~/app.js"))
			if !malformed {
				require.NoError(t, mw.Close())
			}
			req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/sourcemaps", &body)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req.AddCookie(authCookie())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}
