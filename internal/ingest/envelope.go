package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// maxEnvelopeBytes caps the pre-allocation for a single envelope item.
// Matches the outer io.LimitReader in the HTTP handler - no legitimate item
// can exceed the total envelope size limit.
const maxEnvelopeBytes = 20 * 1024 * 1024

type EnvelopeHeader struct {
	EventID string `json:"event_id"`
	SentAt  string `json:"sent_at"`
}

type ItemHeader struct {
	Type   string `json:"type"`
	Length int    `json:"length"`
}

type Item struct {
	Header  ItemHeader
	Payload []byte
}

// Parse reads a Sentry envelope from r.
// Spec: https://develop.sentry.dev/sdk/envelopes/
func Parse(r io.Reader) (EnvelopeHeader, []Item, error) {
	reader := bufio.NewReaderSize(r, 64*1024)

	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return EnvelopeHeader{}, nil, fmt.Errorf("read envelope header: %w", err)
	}
	var header EnvelopeHeader
	if err := json.Unmarshal(bytes.TrimRight(line, "\n"), &header); err != nil {
		return EnvelopeHeader{}, nil, fmt.Errorf("parse envelope header: %w", err)
	}

	var items []Item
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF && len(line) == 0 {
			break
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var ih ItemHeader
		if err := json.Unmarshal(bytes.TrimRight(line, "\n"), &ih); err != nil {
			return header, items, fmt.Errorf("parse item header: %w", err)
		}

		var payload []byte
		if ih.Length > 0 {
			if ih.Length > maxEnvelopeBytes {
				return header, items, fmt.Errorf("item length %d exceeds maximum %d", ih.Length, maxEnvelopeBytes)
			}
			payload = make([]byte, ih.Length)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return header, items, fmt.Errorf("read item payload: %w", err)
			}
			_, _ = reader.ReadByte() // consume trailing newline, ignore EOF
		} else {
			payload, err = reader.ReadBytes('\n')
			if err != nil && err != io.EOF {
				return header, items, fmt.Errorf("read item payload: %w", err)
			}
			payload = bytes.TrimRight(payload, "\n")
		}

		items = append(items, Item{Header: ih, Payload: payload})
	}

	return header, items, nil
}
