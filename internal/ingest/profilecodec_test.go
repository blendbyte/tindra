package ingest_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/blendbyte/tindra/internal/ingest"
)

// compressForTest mirrors what the codec stores, so a test can build a blob
// that is valid zstd but invalid content.
func compressForTest(t *testing.T, raw []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	defer enc.Close()
	return enc.EncodeAll(raw, nil)
}

// Storage is lossy if the round trip is: everything the read path and the UI
// need has to survive compression, including BaseNs, which is what makes a
// decoded profile report the same duration as a freshly parsed one.
func TestEncodeDecodeProfile_roundTrip(t *testing.T) {
	fixtures := []struct {
		file     string
		itemType string
	}{
		{"v1_php_laravel.json", "profile"},
		{"v1_python.json", "profile"},
		{"v1_cocoa.json", "profile"},
		{"v2_python_chunk.json", "profile_chunk"},
		{"v2_cocoa_chunk.json", "profile_chunk"},
	}

	for _, f := range fixtures {
		t.Run(f.file, func(t *testing.T) {
			want, err := ingest.ParseProfileItem(f.itemType, fixture(t, f.file))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			data, encoding, err := ingest.EncodeProfile(want)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if encoding != ingest.ProfileEncodingZstdJSON {
				t.Errorf("encoding = %d, want %d", encoding, ingest.ProfileEncodingZstdJSON)
			}

			got, err := ingest.DecodeProfile(encoding, data)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("round trip changed the profile\n want %+v\n  got %+v", want, got)
			}
			if got.Duration() != want.Duration() {
				t.Errorf("duration = %s, want %s (BaseNs must survive storage)",
					got.Duration(), want.Duration())
			}
		})
	}
}

func TestDecodeProfile_unknownEncoding(t *testing.T) {
	if _, err := ingest.DecodeProfile(99, []byte("whatever")); err == nil {
		t.Error("expected an error for an unknown encoding")
	}
}

func TestDecodeProfile_corruptBlob(t *testing.T) {
	if _, err := ingest.DecodeProfile(ingest.ProfileEncodingZstdJSON, []byte("not zstd")); err == nil {
		t.Error("expected an error for a corrupt blob")
	}
}

// The whole reason for storing a blob rather than sample rows is that this data
// compresses extremely well. If that stops being true the storage sizing behind
// the retention defaults is wrong, so it is worth asserting rather than assuming.
func TestEncodeProfile_compressesRepetitiveSamples(t *testing.T) {
	p, err := ingest.ParseProfile(fixture(t, "v1_python.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Grow the profile the way a real one grows: many samples over a small set
	// of distinct stacks.
	base := p.Samples[0]
	for i := 1; i < 5000; i++ {
		s := base
		s.StackID = int32(i % len(p.Stacks))
		s.TimestampNs = base.TimestampNs + int64(i)*9_900_990
		p.Samples = append(p.Samples, s)
	}

	data, _, err := ingest.EncodeProfile(p)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// A 5000-sample profile should land far under the ~200 KB its JSON needs.
	if len(data) > 64*1024 {
		t.Errorf("5000 samples compressed to %d bytes, expected well under 64 KB", len(data))
	}

	got, err := ingest.DecodeProfile(ingest.ProfileEncodingZstdJSON, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Samples) != len(p.Samples) {
		t.Errorf("samples = %d, want %d", len(got.Samples), len(p.Samples))
	}
}

func TestNewBufferedProfile(t *testing.T) {
	t.Run("v1 carries the transaction link", func(t *testing.T) {
		p, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		b, err := ingest.NewBufferedProfile("project-1", p)
		if err != nil {
			t.Fatalf("buffer: %v", err)
		}

		if b.TransactionEventID != "3c2b1a09f8e7d6c5b4a39281706f5e4d" {
			t.Errorf("transaction event id = %q", b.TransactionEventID)
		}
		if b.ProfilerID != "" || b.ChunkID != "" {
			t.Errorf("v1 should carry no profiler or chunk id, got %q / %q", b.ProfilerID, b.ChunkID)
		}
		if b.SampleCount != len(p.Samples) {
			t.Errorf("sample count = %d, want %d", b.SampleCount, len(p.Samples))
		}
		if b.SizeBytes() != len(b.Data) {
			t.Errorf("size = %d, want %d", b.SizeBytes(), len(b.Data))
		}
		if !b.StartTs.Equal(p.Start()) || !b.EndTs.Equal(p.End()) {
			t.Error("bounds should match the parsed profile")
		}
	})

	t.Run("v2 carries the profiler link", func(t *testing.T) {
		p, err := ingest.ParseProfileChunk(fixture(t, "v2_python_chunk.json"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		b, err := ingest.NewBufferedProfile("project-1", p)
		if err != nil {
			t.Fatalf("buffer: %v", err)
		}

		if b.ProfilerID != "4d229f1d3807421ba62a5f8bc295d836" {
			t.Errorf("profiler id = %q", b.ProfilerID)
		}
		if b.ChunkID == "" {
			t.Error("v2 should carry a chunk id")
		}
		if b.TransactionEventID != "" {
			t.Errorf("v2 should carry no transaction link, got %q", b.TransactionEventID)
		}
	})
}

// Frame paths carry deploy paths and developer home directories, so they run
// through the project's scrub patterns like any other free text.
func TestScrubProfile(t *testing.T) {
	p, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(p.Frames[0].AbsPath, "/var/www/app") {
		t.Fatalf("fixture should contain the path being scrubbed, got %q", p.Frames[0].AbsPath)
	}

	cfg := ingest.ScrubConfig{
		Patterns: []ingest.ScrubPattern{{Name: "deploy-path", Pattern: `/var/www/app`, Enabled: true}},
	}
	ingest.ScrubProfile(p, cfg)

	for i, f := range p.Frames {
		if strings.Contains(f.AbsPath, "/var/www/app") {
			t.Errorf("frame %d abs_path was not scrubbed: %q", i, f.AbsPath)
		}
	}
	// Structure must be untouched: only the metadata strings are rewritten.
	if len(p.Stacks) == 0 || len(p.Samples) == 0 {
		t.Error("scrubbing should not touch stacks or samples")
	}
}

func TestScrubProfile_noPatternsIsANoop(t *testing.T) {
	p, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	before := p.Frames[0].AbsPath

	ingest.ScrubProfile(p, ingest.ScrubConfig{Fields: []string{"abs_path"}})

	if p.Frames[0].AbsPath != before {
		t.Errorf("abs_path = %q, want unchanged %q: field blocking does not apply to frames",
			p.Frames[0].AbsPath, before)
	}
}

// A blob that decompresses but is not a profile has to fail loudly. zstd will
// happily round-trip any bytes, so the JSON step is the only thing standing
// between a corrupt row and a nonsense graph.
func TestDecodeProfile_validZstdButNotAProfile(t *testing.T) {
	junk, _, err := ingest.EncodeProfile(nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// EncodeProfile(nil) produces the JSON literal "null", which decodes to an
	// empty profile rather than failing, so check that explicitly.
	if p, err := ingest.DecodeProfile(ingest.ProfileEncodingZstdJSON, junk); err != nil || p == nil {
		t.Errorf("null profile: got %v, %v", p, err)
	}

	// Now something that really is not JSON at all.
	garbage := compressForTest(t, []byte("{definitely not json"))
	if _, err := ingest.DecodeProfile(ingest.ProfileEncodingZstdJSON, garbage); err == nil {
		t.Error("expected an error decoding valid zstd holding invalid JSON")
	}
}
