package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// maxEnvelopeBytes caps the length a single envelope item may declare.
// Matches the outer io.LimitReader in the HTTP handler - no legitimate item
// can exceed the total envelope size limit.
const maxEnvelopeBytes = 20 * 1024 * 1024

// initialItemBuffer is the most an item buffer reserves before its bytes arrive.
const initialItemBuffer = 64 * 1024

type EnvelopeHeader struct {
	EventID string `json:"event_id"`
	// Raw is the header line as sent, kept so the envelope can be rebuilt for
	// passthrough forwarding without losing fields Tindra does not model.
	Raw json.RawMessage `json:"-"`
}

type ItemHeader struct {
	Type   string `json:"type"`
	Length int    `json:"length"`
	// Raw is the item header line as sent (content_type, item_count, ...).
	Raw json.RawMessage `json:"-"`
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
	if err != nil && (err != io.EOF || len(line) == 0) {
		return EnvelopeHeader{}, nil, fmt.Errorf("read envelope header: %w", err)
	}
	var parsedHeader *EnvelopeHeader
	if err := json.Unmarshal(bytes.TrimRight(line, "\r\n"), &parsedHeader); err != nil {
		return EnvelopeHeader{}, nil, fmt.Errorf("parse envelope header: %w", err)
	}
	if parsedHeader == nil {
		return EnvelopeHeader{}, nil, fmt.Errorf("envelope header must be a JSON object")
	}
	header := *parsedHeader
	header.Raw = bytes.Clone(bytes.TrimRight(line, "\r\n"))

	var items []Item
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return header, items, fmt.Errorf("read item header: %w", err)
		}
		if err == io.EOF && len(line) == 0 {
			break
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var ih ItemHeader
		if err := json.Unmarshal(bytes.TrimRight(line, "\r\n"), &ih); err != nil {
			return header, items, fmt.Errorf("parse item header: %w", err)
		}
		ih.Raw = bytes.Clone(bytes.TrimRight(line, "\r\n"))

		var payload []byte
		if ih.Length > 0 {
			if ih.Length > maxEnvelopeBytes {
				return header, items, fmt.Errorf("item length %d exceeds maximum %d", ih.Length, maxEnvelopeBytes)
			}
			// Grow with the bytes that actually arrive instead of allocating the
			// declared length up front: the header is client-controlled, so a
			// claimed 20 MB item must not cost 20 MB before any payload is sent.
			var buf bytes.Buffer
			buf.Grow(min(ih.Length, initialItemBuffer))
			if _, err := io.CopyN(&buf, reader, int64(ih.Length)); err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return header, items, fmt.Errorf("read item payload: %w", err)
			}
			payload = buf.Bytes()
			separator, err := reader.ReadByte()
			if err == nil && separator == '\r' {
				separator, err = reader.ReadByte()
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
			}
			if err != nil && err != io.EOF {
				return header, items, fmt.Errorf("read item separator: %w", err)
			}
			if err == nil && separator != '\n' {
				return header, items, fmt.Errorf("invalid item separator: %q", separator)
			}
		} else {
			payload, err = reader.ReadBytes('\n')
			if err != nil && err != io.EOF {
				return header, items, fmt.Errorf("read item payload: %w", err)
			}
			payload = bytes.TrimRight(payload, "\r\n")
		}

		items = append(items, Item{Header: ih, Payload: payload})
	}

	return header, items, nil
}

// EncodeEnvelope serializes an envelope from a header line and items. Each item
// keeps its original header fields, with length rewritten to match the payload,
// since a payload may have changed (scrubbing) since it was parsed.
func EncodeEnvelope(header json.RawMessage, items []Item) ([]byte, error) {
	var out bytes.Buffer
	out.Write(header)
	out.WriteByte('\n')
	for _, item := range items {
		fields := map[string]json.RawMessage{}
		if len(item.Header.Raw) > 0 {
			if err := json.Unmarshal(item.Header.Raw, &fields); err != nil {
				return nil, fmt.Errorf("item header: %w", err)
			}
		}
		if _, ok := fields["type"]; !ok {
			fields["type"], _ = json.Marshal(item.Header.Type)
		}
		fields["length"], _ = json.Marshal(len(item.Payload))
		itemHeader, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("item header: %w", err)
		}
		out.Write(itemHeader)
		out.WriteByte('\n')
		out.Write(item.Payload)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}
