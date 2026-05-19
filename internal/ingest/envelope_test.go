package ingest_test

import (
	"fmt"
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
