package profiles

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestProfileReadAdmissionHonorsCancellation(t *testing.T) {
	for i := 0; i < cap(profileReadSlots); i++ {
		profileReadSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(profileReadSlots); i++ {
			<-profileReadSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A nil pool proves that waiting cancellation happens before querying.
	if _, err := FlameGraphForTransaction(ctx, nil, "unused"); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

// Rows with two frame-heavy profiles and no samples exercise the aggregate
// budget without constructing hundred-megabyte test fixtures.
type budgetRows struct {
	pgx.Rows
	data   []byte
	next   int
	closed bool
}

func (r *budgetRows) Next() bool { r.next++; return r.next <= 2 }
func (r *budgetRows) Scan(dest ...any) error {
	*dest[0].(*int16) = ingest.ProfileEncodingZstdJSON
	*dest[1].(*[]byte) = r.data
	return nil
}
func (r *budgetRows) Close()     { r.closed = true }
func (r *budgetRows) Err() error { return nil }

func TestProfileAggregateBudgetStopsBeforeNextObject(t *testing.T) {
	p := &ingest.Profile{Frames: []ingest.ProfileFrame{{Function: strings.Repeat("frame", 1000)}}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := ingest.EncodeProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	rows := &budgetRows{data: data}
	got, err := decodeRows(context.Background(), rows, len(raw)+1)
	if err != nil || len(got) != 1 || !rows.closed {
		t.Fatalf("profiles=%d closed=%v err=%v", len(got), rows.closed, err)
	}
}
