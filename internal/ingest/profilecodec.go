package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Profiles are stored as a compressed blob rather than exploded into rows.
// A single 66s chunk at the default 101 Hz is several thousand samples, so a
// relational profile_samples table would mean tens of millions of rows an hour
// on a busy instance. Keeping the payload opaque leaves the metadata rows
// narrow, which is what retention and listing actually scan.

// Profile blob encodings. The encoding is stored alongside every row so a
// future format can coexist with existing data instead of forcing a rewrite.
const (
	// ProfileEncodingZstdJSON is zstd-compressed JSON of the normalized Profile.
	ProfileEncodingZstdJSON int16 = 1
)

// maxDecodedProfileBytes bounds decompression. The compressed form is bounded
// by the envelope cap, but zstd ratios on repetitive sample data are high
// enough that an inflated blob still needs an explicit ceiling.
const maxDecodedProfileBytes = 64 << 20

var (
	profileEncoder *zstd.Encoder
	profileDecoder *zstd.Decoder
)

func init() {
	var err error
	// EncodeAll and DecodeAll are stateless and safe for concurrent use, so a
	// single shared pair serves every request.
	profileEncoder, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(fmt.Sprintf("ingest: build profile encoder: %v", err))
	}
	profileDecoder, err = zstd.NewReader(nil,
		zstd.WithDecoderMaxMemory(maxDecodedProfileBytes),
		zstd.WithDecoderConcurrency(0))
	if err != nil {
		panic(fmt.Sprintf("ingest: build profile decoder: %v", err))
	}
}

// EncodeProfile serializes and compresses a normalized profile for storage.
// Callers run this on the request goroutine so that only compressed bytes are
// held in the write buffer: profile payloads are orders of magnitude larger
// than events, and buffering them raw would put the memory ceiling out of reach.
func EncodeProfile(p *Profile) (data []byte, encoding int16, err error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal profile: %w", err)
	}
	return profileEncoder.EncodeAll(raw, nil), ProfileEncodingZstdJSON, nil
}

// DecodeProfile reverses EncodeProfile.
// ErrProfileDecodeBudget means the next chunk would exceed the request budget.
var ErrProfileDecodeBudget = errors.New("profile decode budget exceeded")

func DecodeProfile(encoding int16, data []byte) (*Profile, error) {
	p, _, err := DecodeProfileLimited(context.Background(), encoding, data, maxDecodedProfileBytes)
	return p, err
}

// DecodeProfileLimited charges all decompressed JSON, including frames and stacks,
// before allocating the decoded object. The decoder also caps any transient raw
// buffer at 64 MiB. Cancellation is checked between decompression and JSON parsing.
func DecodeProfileLimited(ctx context.Context, encoding int16, data []byte, remaining int) (*Profile, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if remaining <= 0 {
		return nil, 0, ErrProfileDecodeBudget
	}
	if encoding != ProfileEncodingZstdJSON {
		return nil, 0, fmt.Errorf("unknown profile encoding %d", encoding)
	}
	raw, err := profileDecoder.DecodeAll(data, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("decompress profile: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if len(raw) > remaining {
		return nil, 0, ErrProfileDecodeBudget
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, 0, fmt.Errorf("unmarshal profile: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return &p, len(raw), nil
}

// BufferedProfile is a profile on its way to Postgres: metadata as columns,
// samples as an opaque compressed blob.
type BufferedProfile struct {
	ProjectID string
	Format    ProfileFormat

	// TransactionEventID links a v1 profile to its transaction, matching
	// transactions.event_id. ProfilerID and ChunkID identify a v2 chunk, which
	// is reached from the other direction by slicing on profiler and time.
	TransactionEventID string
	ChunkID            string
	ProfilerID         string
	TraceID            string

	StartTs time.Time
	EndTs   time.Time

	Environment string
	Release     string
	Platform    string

	SampleCount int
	Encoding    int16
	Data        []byte
}

// SizeBytes is the stored size of the blob, tracked as a column so the
// retention worker can enforce a byte budget without inspecting the payload.
func (b BufferedProfile) SizeBytes() int { return len(b.Data) }

// NewBufferedProfile compresses a normalized profile and pairs it with the
// metadata that becomes queryable columns.
func NewBufferedProfile(projectID string, p *Profile) (BufferedProfile, error) {
	data, encoding, err := EncodeProfile(p)
	if err != nil {
		return BufferedProfile{}, err
	}
	return BufferedProfile{
		ProjectID:          projectID,
		Format:             p.Format,
		TransactionEventID: p.TransactionID,
		ChunkID:            p.ChunkID,
		ProfilerID:         p.ProfilerID,
		TraceID:            p.TraceID,
		StartTs:            p.Start(),
		EndTs:              p.End(),
		Environment:        p.Environment,
		Release:            p.Release,
		Platform:           p.Platform,
		SampleCount:        len(p.Samples),
		Encoding:           encoding,
		Data:               data,
	}, nil
}
