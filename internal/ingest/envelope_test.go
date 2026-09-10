package ingest_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestParse_basicEvent(t *testing.T) {
	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error"}`
	body := `{"event_id":"abc123","sent_at":"2024-01-01T00:00:00Z"}` + "\n" +
		fmt.Sprintf(`{"type":"event","length":%d}`, len(payload)) + "\n" +
		payload + "\n"

	header, items, err := ingest.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.EventID != "abc123" {
		t.Errorf("expected event_id abc123, got %q", header.EventID)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Header.Type != "event" {
		t.Errorf("expected type event, got %q", items[0].Header.Type)
	}
	if string(items[0].Payload) != payload {
		t.Errorf("payload mismatch:\n got  %q\n want %q", items[0].Payload, payload)
	}
}

func TestParse_newlineDelimitedPayload(t *testing.T) {
	payload := `{"timestamp":"2024-01-01T00:00:00Z"}`
	body := `{}` + "\n" +
		`{"type":"event"}` + "\n" +
		payload + "\n"

	_, items, err := ingest.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if string(items[0].Payload) != payload {
		t.Errorf("payload mismatch: got %q", items[0].Payload)
	}
}

func TestParse_multipleItems(t *testing.T) {
	body := `{"event_id":"abc"}` + "\n" +
		`{"type":"session"}` + "\n" +
		`{"sid":"123"}` + "\n" +
		`{"type":"event"}` + "\n" +
		`{"timestamp":"2024-01-01T00:00:00Z"}` + "\n"

	_, items, err := ingest.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Header.Type != "session" {
		t.Errorf("expected session, got %q", items[0].Header.Type)
	}
	if items[1].Header.Type != "event" {
		t.Errorf("expected event, got %q", items[1].Header.Type)
	}
}

func TestParse_emptyEnvelope(t *testing.T) {
	_, items, err := ingest.Parse(strings.NewReader("{}\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestParse_malformedHeader(t *testing.T) {
	_, _, err := ingest.Parse(strings.NewReader("not json\n"))
	if err == nil {
		t.Error("expected error for malformed envelope header")
	}
}

func TestParse_noTrailingNewline(t *testing.T) {
	payload := `{"timestamp":"2024-01-01T00:00:00Z"}`
	body := `{}` + "\n" + `{"type":"event"}` + "\n" + payload // no trailing \n

	_, items, err := ingest.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}

func TestParse_emptyReader(t *testing.T) {
	_, _, err := ingest.Parse(strings.NewReader(""))
	if err == nil {
		t.Error("expected error for completely empty reader")
	}
}

func TestParse_malformedItemHeader(t *testing.T) {
	body := `{}` + "\n" + `not json item header` + "\n"
	_, _, err := ingest.Parse(strings.NewReader(body))
	if err == nil {
		t.Error("expected error for malformed item header JSON")
	}
}

func TestParse_itemExceedsMaxBytes(t *testing.T) {
	// Claim an item length just over the 20 MB cap - parser must reject it
	// before allocating that much memory.
	const oversized = 20*1024*1024 + 1
	body := fmt.Sprintf("{}\n{\"type\":\"event\",\"length\":%d}\n", oversized)
	_, _, err := ingest.Parse(strings.NewReader(body))
	if err == nil {
		t.Error("expected error for item length exceeding 20 MB limit")
	}
}

// Limit underlying reads so a regression fails promptly instead of hanging the suite.
type boundedEnvelopeReader struct {
	t      *testing.T
	reader io.Reader
	reads  int
}

func (r *boundedEnvelopeReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 16 {
		r.t.Fatal("parser kept reading after a persistent stream error")
	}
	return r.reader.Read(p)
}

type failedEnvelopeReader struct{ err error }

func (r failedEnvelopeReader) Read([]byte) (int, error) { return 0, r.err }

func TestParse_streamErrors(t *testing.T) {
	streamErr := errors.New("stream failed")
	for _, tc := range []struct{ name, body, stage string }{
		{"empty header", "", "read envelope header"},
		{"partial header", "{}", "read envelope header"},
		{"empty item header", "{}\n", "read item header"},
		{"blank item header", "{}\n \t", "read item header"},
		{"partial item header", "{}\n{}", "read item header"},
		{"line payload", "{}\n{}\nabc", "read item payload"},
		{"fixed payload", "{}\n{\"length\":2}\na", "read item payload"},
		{"separator", "{}\n{\"length\":1}\na", "read item separator"},
		{"CRLF separator", "{}\n{\"length\":1}\na\r", "read item separator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &boundedEnvelopeReader{t: t, reader: io.MultiReader(strings.NewReader(tc.body), failedEnvelopeReader{streamErr})}
			_, _, err := ingest.Parse(r)
			if !errors.Is(err, streamErr) || !strings.Contains(err.Error(), tc.stage) {
				t.Fatalf("expected %s wrapping stream error, got %v", tc.stage, err)
			}
		})
	}
}

func TestParse_gzipIntegrity(t *testing.T) {
	for _, body := range []string{"{}\n", "{}\n{}\nhello\n", "{}\n{\"length\":5}\nhello", "{}\n{\"length\":5}\nhello\n"} {
		for _, damage := range []string{"none", "checksum", "truncated"} {
			t.Run(fmt.Sprintf("%q/%s", body, damage), func(t *testing.T) {
				var compressed bytes.Buffer
				zw := gzip.NewWriter(&compressed)
				if _, err := zw.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
				data := compressed.Bytes()
				var want error
				switch damage {
				case "checksum":
					data[len(data)-8] ^= 1
					want = gzip.ErrChecksum
				case "truncated":
					data = data[:len(data)-4]
					want = io.ErrUnexpectedEOF
				}
				zr, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				defer zr.Close()
				_, _, err = ingest.Parse(&boundedEnvelopeReader{t: t, reader: zr})
				if !errors.Is(err, want) {
					t.Fatalf("expected %v, got %v", want, err)
				}
			})
		}
	}
}

func TestParse_fixedLengthSeparators(t *testing.T) {
	for _, tc := range []struct {
		name, suffix string
		wantErr      bool
		count        int
	}{
		{"EOF", "", false, 1},
		{"LF", "\n", false, 1},
		{"CRLF", "\r\n", false, 1},
		{"blank lines", "\n\n \t\n", false, 1},
		{"next item", "\n{\"length\":1}\nb", false, 2},
		{"CRLF next item", "\r\n{\"length\":1}\nb\r\n", false, 2},
		{"invalid byte", "x", true, 0},
		{"missing separator", "{\"length\":1}\nb", true, 0},
		{"incomplete CRLF", "\r", true, 0},
		{"invalid CRLF", "\rx", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, items, err := ingest.Parse(strings.NewReader("{}\n{\"length\":1}\na" + tc.suffix))
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(items) != tc.count {
				t.Fatalf("expected %d items, got %d", tc.count, len(items))
			}
			if !tc.wantErr && string(items[0].Payload) != "a" {
				t.Fatalf("unexpected payload: %q", items[0].Payload)
			}
		})
	}
}

func TestParse_truncatedFixedPayload(t *testing.T) {
	_, _, err := ingest.Parse(strings.NewReader("{}\n{\"length\":2}\na"))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected unexpected EOF, got %v", err)
	}
}
