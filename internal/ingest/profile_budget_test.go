package ingest_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestProfileDecodeBudgetIncludesFramesAndStacks(t *testing.T) {
	p := &ingest.Profile{Frames: []ingest.ProfileFrame{{Function: strings.Repeat("frame", 10000)}}, Stacks: [][]int32{make([]int32, 10000)}}
	data, encoding, err := ingest.EncodeProfile(p)
	require.NoError(t, err)
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	got, size, err := ingest.DecodeProfileLimited(context.Background(), encoding, data, len(raw))
	require.NoError(t, err)
	require.Equal(t, len(raw), size)
	require.Equal(t, p, got)
	got, _, err = ingest.DecodeProfileLimited(context.Background(), encoding, data, len(raw)-1)
	require.ErrorIs(t, err, ingest.ErrProfileDecodeBudget)
	require.Nil(t, got)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = ingest.DecodeProfileLimited(ctx, encoding, data, len(raw))
	require.ErrorIs(t, err, context.Canceled)
}

func BenchmarkProfileDecodeBudget(b *testing.B) {
	p := &ingest.Profile{Frames: []ingest.ProfileFrame{{Function: strings.Repeat("frame", 10000)}}, Stacks: [][]int32{make([]int32, 10000)}, Samples: make([]ingest.ProfileSample, 200000)}
	data, encoding, err := ingest.EncodeProfile(p)
	if err != nil {
		b.Fatal(err)
	}
	for _, limited := range []bool{false, true} {
		name := "full"
		if limited {
			name = "budget_exhausted"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _, err := ingest.DecodeProfileLimited(context.Background(), encoding, data, func() int {
					if limited {
						return 1024
					}
					return 64 << 20
				}())
				if limited {
					if err != ingest.ErrProfileDecodeBudget {
						b.Fatal(err)
					}
				} else if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestExhaustedProfileBudgetRejectsBeforeDecoding(t *testing.T) {
	for _, budget := range []int{0, -1} {
		profile, size, err := ingest.DecodeProfileLimited(context.Background(), ingest.ProfileEncodingZstdJSON, []byte("invalid compressed data"), budget)
		require.ErrorIs(t, err, ingest.ErrProfileDecodeBudget)
		require.Nil(t, profile)
		require.Zero(t, size)
	}
}
