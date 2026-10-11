package api

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func compressWith(t *testing.T, encoding string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	switch encoding {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "br":
		w = brotli.NewWriter(&buf)
	case "zstd":
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		w = zw
	case "deflate-zlib":
		w = zlib.NewWriter(&buf)
	case "deflate-raw":
		fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}
		w = fw
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeBody_supportedEncodings(t *testing.T) {
	want := []byte(`{"event_id":"abc"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"hi"}`)
	for _, tc := range []struct{ header, kind string }{
		{"gzip", "gzip"}, {"GZIP", "gzip"}, {"br", "br"}, {"zstd", "zstd"},
		{"deflate", "deflate-zlib"}, {"deflate", "deflate-raw"},
	} {
		r, closeFn, err := decodeBody(tc.header, bytes.NewReader(compressWith(t, tc.kind, want)))
		if err != nil {
			t.Fatalf("%s (%s): %v", tc.header, tc.kind, err)
		}
		got, err := io.ReadAll(r)
		closeFn()
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s (%s): got %q, err %v", tc.header, tc.kind, got, err)
		}
	}
}

func TestDecodeBody_identityAndUnsupported(t *testing.T) {
	r, closeFn, err := decodeBody("identity", bytes.NewReader([]byte("raw")))
	if err != nil {
		t.Fatal(err)
	}
	closeFn()
	if got, _ := io.ReadAll(r); string(got) != "raw" {
		t.Errorf("identity must pass through, got %q", got)
	}
	if _, closeFn, err := decodeBody("compress", bytes.NewReader(nil)); !errors.Is(err, errUnsupportedEncoding) {
		t.Errorf("expected errUnsupportedEncoding, got %v", err)
	} else {
		closeFn()
	}
}
