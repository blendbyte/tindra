package api

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/blendbyte/tindra/internal/ui"
)

func TestAssetDelivery(t *testing.T) {
	body := strings.Repeat("console.log('Tindra');\n", 1000)
	handler := newAssetHandler(fstest.MapFS{
		"index.html":             {Data: []byte("<html>Tindra</html>")},
		"assets/app-aB12_345.js": {Data: []byte(body)},
		"favicon.svg":            {Data: []byte("<svg />")},
	})
	request := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if rejected := request("GET", "/assets/app-aB12_345.js", map[string]string{"Accept-Encoding": "gzip;q=0, identity;q=0"}); rejected.Code != 406 {
		t.Fatal("unacceptable encodings accepted")
	}
	plain := request("GET", "/assets/app-aB12_345.js", nil)
	zipped := request("GET", "/assets/app-aB12_345.js", map[string]string{"Accept-Encoding": "br, gzip"})
	if zipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("missing gzip")
	}
	zr, err := gzip.NewReader(zipped.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := io.ReadAll(zr)
	zr.Close()
	if string(decoded) != body {
		t.Fatal("gzip body mismatch")
	}
	if plain.Header().Get("ETag") == zipped.Header().Get("ETag") {
		t.Fatal("representation tags must differ")
	}
	if !strings.Contains(zipped.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("missing immutable policy")
	}
	if zipped.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatal("missing vary")
	}
	fresh := request("GET", "/assets/app-aB12_345.js", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": zipped.Header().Get("ETag")})
	if fresh.Code != http.StatusNotModified || fresh.Body.Len() != 0 {
		t.Fatal("conditional request did not return empty 304")
	}
	head := request("HEAD", "/assets/app-aB12_345.js", map[string]string{"Accept-Encoding": "gzip"})
	if head.Header().Get("Content-Length") == "" || head.Body.Len() != 0 || head.Header().Get("Content-Length") != zipped.Header().Get("Content-Length") {
		t.Fatal("HEAD mismatch")
	}
	partial := request("GET", "/assets/app-aB12_345.js", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"})
	if partial.Code != 206 || partial.Body.String() != body[:10] || partial.Header().Get("Content-Encoding") != "" {
		t.Fatal("range mismatch")
	}
	html := request("GET", "/issues/123", nil)
	if html.Code != 200 || html.Body.String() != "<html>Tindra</html>" || html.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("SPA fallback mismatch")
	}
	if request("GET", "/assets/missing-oldhash.js", nil).Code != 404 {
		t.Fatal("missing chunk must fail")
	}
	if request("POST", "/issues/123", nil).Code != 405 {
		t.Fatal("unexpected method accepted")
	}
	if strings.Contains(request("GET", "/favicon.svg", nil).Header().Get("Cache-Control"), "immutable") {
		t.Fatal("unhashed asset immutable")
	}
	t.Logf("static JavaScript bytes: identity=%d gzip=%s", plain.Body.Len(), zipped.Header().Get("Content-Length"))
}

func TestAcceptsGzip(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"", false}, {"br", false}, {"gzip", true}, {"gzip;q=0", false},
		{"*;q=1, gzip;q=0", false}, {"br, *;q=0.5", true},
		{"gzip;q=invalid", false}, {"gzip;q=NaN", false}, {"gzip;q=1.5", false}, {"GZIP; q=0.3", true},
	} {
		if got := acceptsGzip([]string{tc.header}); got != tc.want {
			t.Errorf("%q: %v", tc.header, got)
		}
	}
}

func TestEmbeddedAssetSizes(t *testing.T) {
	if os.Getenv("TINDRA_PERF_SOURCEMAPS") != "1" {
		t.Skip("set TINDRA_PERF_SOURCEMAPS=1")
	}
	dist, _ := fs.Sub(ui.FS, "dist")
	start := time.Now()
	handler := newAssetHandler(dist)
	prepare := time.Since(start)
	var plainBytes, gzipBytes, files int
	err := fs.WalkDir(dist, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".css") {
			return nil
		}
		data, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		request := httptest.NewRequest("GET", "/"+name, nil)
		request.Header.Set("Accept-Encoding", "gzip")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s: %d", name, response.Code)
		}
		plainBytes += len(data)
		gzipBytes += response.Body.Len()
		files++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("all emitted JS/CSS, including deferred assets: files=%d identity=%d gzip=%d preparation=%s", files, plainBytes, gzipBytes, prepare)
}

// A file can be listed successfully but fail when its contents are read.
type unreadableAssetFS struct{ fstest.MapFS }

func (f unreadableAssetFS) ReadFile(name string) ([]byte, error) {
	return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrPermission}
}

func TestMissingAndUnreadableAssetBundles(t *testing.T) {
	for name, bundle := range map[string]fs.FS{
		"missing index":    fstest.MapFS{},
		"unreadable index": unreadableAssetFS{fstest.MapFS{"index.html": {Data: []byte("hidden")}}},
	} {
		t.Run(name, func(t *testing.T) {
			handler := newAssetHandler(bundle)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET", "/issues", nil))
			if rec.Code != 404 {
				t.Fatalf("status=%d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "hidden") {
				t.Fatal("unreadable data was served")
			}
		})
	}
	handler := newAssetHandler(fstest.MapFS{"download.tindraunknown": {Data: []byte("plain source")}})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/download.tindraunknown", nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || rec.Body.String() != "plain source" {
		t.Fatalf("response=%+v", rec)
	}
}
