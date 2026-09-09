package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blendbyte/tindra/internal/ui"
)

type staticAsset struct {
	data, compressed            []byte
	etag, gzipETag, contentType string
}

var fingerprintedAsset = regexp.MustCompile(`^assets/.+-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)
var embeddedAssets = sync.OnceValue(func() http.Handler {
	dist, _ := fs.Sub(ui.FS, "dist")
	return newAssetHandler(dist)
})

// Prepare immutable representations once per binary, not on each request.
func newAssetHandler(dist fs.FS) http.Handler {
	assets := make(map[string]staticAsset)
	_ = fs.WalkDir(dist, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		ct := mime.TypeByExtension(path.Ext(name))
		if ct == "" {
			ct = http.DetectContentType(data)
		}
		asset := staticAsset{data: data, contentType: ct, etag: fmt.Sprintf(`"%x"`, sha256.Sum256(data))}
		if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") || strings.Contains(ct, "json") || strings.Contains(ct, "svg") {
			var buf bytes.Buffer
			gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = gz.Write(data)
			_ = gz.Close()
			if buf.Len() < len(data) {
				asset.compressed = buf.Bytes()
				asset.gzipETag = fmt.Sprintf(`"%x"`, sha256.Sum256(asset.compressed))
			}
		}
		assets[name] = asset
		return nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		asset, ok := assets[name]
		if !ok {
			// A missing deployment chunk must fail so the existing reload handler runs.
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
			asset, ok = assets[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		if fingerprintedAsset.MatchString(name) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		data, etag := asset.data, asset.etag
		if len(asset.compressed) > 0 {
			w.Header().Add("Vary", "Accept-Encoding")
			// Range responses retain byte offsets in the original representation.
			if r.Header.Get("Range") == "" && acceptsGzip(r.Header.Values("Accept-Encoding")) {
				data, etag = asset.compressed, asset.gzipETag
				w.Header().Set("Content-Encoding", "gzip")
			}
		}
		if w.Header().Get("Content-Encoding") == "" && encodingQuality(r.Header.Values("Accept-Encoding"), "identity") == 0 {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "no acceptable representation", http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Type", asset.contentType)
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

func acceptsGzip(headers []string) bool {
	return encodingQuality(headers, "gzip") > 0
}

func encodingQuality(headers []string, wanted string) float64 {
	wildcard := -1.0
	for _, header := range headers {
		for _, item := range strings.Split(header, ",") {
			parts := strings.Split(item, ";")
			coding := strings.ToLower(strings.TrimSpace(parts[0]))
			quality := 1.0
			for _, param := range parts[1:] {
				key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
				if ok && strings.EqualFold(key, "q") {
					q, err := strconv.ParseFloat(value, 64)
					if err != nil || !(q >= 0 && q <= 1) {
						quality = 0
					} else {
						quality = q
					}
				}
			}
			if coding == wanted {
				return quality
			}
			if coding == "*" {
				wildcard = quality
			}
		}
	}
	if wanted == "identity" && wildcard != 0 {
		return 1
	}
	if wildcard < 0 {
		return 0
	}
	return wildcard
}
