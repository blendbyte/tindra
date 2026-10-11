package api

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// errUnsupportedEncoding marks a Content-Encoding the envelope endpoint cannot decode.
var errUnsupportedEncoding = errors.New("unsupported content encoding")

// zstdMaxWindow bounds the zstd decoder's window so a crafted frame header
// cannot demand a huge allocation. Real SDK payloads use small windows.
const zstdMaxWindow = 8 << 20

// decodeBody wraps r in a decoder for the request's Content-Encoding. Sentry
// SDKs send gzip, and sentry-python sends brotli when the brotli package is
// installed; Relay also accepts deflate and zstd. The returned close function
// releases decoder resources and is always safe to call.
func decodeBody(encoding string, r io.Reader) (io.Reader, func(), error) {
	noop := func() {}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return r, noop, nil
	case "gzip", "x-gzip":
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, noop, err
		}
		return gz, func() { gz.Close() }, nil
	case "br":
		return brotli.NewReader(r), noop, nil
	case "zstd":
		dec, err := zstd.NewReader(r,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxWindow(zstdMaxWindow))
		if err != nil {
			return nil, noop, err
		}
		return dec, dec.Close, nil
	case "deflate":
		// HTTP "deflate" is zlib-wrapped (RFC 9110), but some clients send a
		// raw deflate stream. A zlib header has a known shape, so peek first.
		br := bufio.NewReader(r)
		if header, err := br.Peek(2); err == nil && isZlibHeader(header) {
			zr, err := zlib.NewReader(br)
			if err != nil {
				return nil, noop, err
			}
			return zr, func() { zr.Close() }, nil
		}
		fr := flate.NewReader(br)
		return fr, func() { fr.Close() }, nil
	default:
		return nil, noop, errUnsupportedEncoding
	}
}

// isZlibHeader reports whether b starts a zlib stream: deflate method with a
// valid window size, and a header checksum divisible by 31 (RFC 1950).
func isZlibHeader(b []byte) bool {
	return len(b) >= 2 && b[0]&0x0f == 8 && b[0]>>4 <= 7 && (uint16(b[0])<<8|uint16(b[1]))%31 == 0
}
