package profiles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
)

// maxChunkDuration bounds how far back the chunk scan has to look. Upstream
// caps a continuous chunk at 66 seconds, so a chunk starting more than this
// before a transaction cannot still be running when it begins. Bounding
// start_ts this way lets the (project_id, profiler_id, start_ts) btree do the
// work instead of needing a range index.
const maxChunkDuration = 70 * time.Second

// maxChunksPerTransaction caps how many chunks one request decompresses. A
// transaction spanning more than this many 66s chunks would be over an hour
// long, which is not a request we should be serving from a blob scan.
const maxChunksPerTransaction = 64

// ErrNoProfile means the transaction has no profile: either none was sent, or
// it aged out, or profiling is off for the project.
var ErrNoProfile = errors.New("no profile for transaction")

// transactionRef is what the read path needs from a transaction row.
type transactionRef struct {
	ProjectID  string
	EventID    string
	ProfilerID string
	ThreadID   string
	Start      time.Time
	End        time.Time
}

// FlameGraphForTransaction resolves a transaction to its profile and folds it.
//
// The two formats are reached from opposite directions. A v1 profile names the
// transaction it belongs to, so it is a direct lookup. A v2 chunk knows nothing
// about transactions, so the transaction's profiler id and window select the
// chunks that were running while it ran, and the samples are then cut down to
// the transaction's own slice of them.
func FlameGraphForTransaction(ctx context.Context, pool *pgxpool.Pool, txID string) (*FlameGraph, error) {
	ref, err := loadTransactionRef(ctx, pool, txID)
	if err != nil {
		return nil, err
	}

	if ref.EventID != "" {
		if g, err := foldV1(ctx, pool, ref); err == nil {
			return g, nil
		} else if !errors.Is(err, ErrNoProfile) {
			return nil, err
		}
	}
	if ref.ProfilerID != "" {
		return foldV2(ctx, pool, ref)
	}
	return nil, ErrNoProfile
}

func loadTransactionRef(ctx context.Context, pool *pgxpool.Pool, txID string) (transactionRef, error) {
	var (
		ref                           transactionRef
		eventID, profilerID, threadID *string
	)
	err := pool.QueryRow(ctx, `
		SELECT project_id, event_id, profiler_id, thread_id, start_timestamp, timestamp
		FROM transactions WHERE id = $1`, txID,
	).Scan(&ref.ProjectID, &eventID, &profilerID, &threadID, &ref.Start, &ref.End)
	if errors.Is(err, pgx.ErrNoRows) {
		return ref, ErrNoProfile
	}
	if err != nil {
		return ref, fmt.Errorf("load transaction: %w", err)
	}
	if eventID != nil {
		ref.EventID = *eventID
	}
	if profilerID != nil {
		ref.ProfilerID = *profilerID
	}
	if threadID != nil {
		ref.ThreadID = *threadID
	}
	return ref, nil
}

// foldV1 handles a transaction-based profile, which covers exactly this
// transaction and needs no windowing.
func foldV1(ctx context.Context, pool *pgxpool.Pool, ref transactionRef) (*FlameGraph, error) {
	rows, err := pool.Query(ctx, `
		SELECT encoding, data FROM profile_chunks
		WHERE project_id = $1 AND transaction_event_id = $2
		LIMIT 1`, ref.ProjectID, ref.EventID)
	if err != nil {
		return nil, fmt.Errorf("query v1 profile: %w", err)
	}
	profs, err := decodeRows(rows)
	if err != nil {
		return nil, err
	}
	if len(profs) == 0 {
		return nil, ErrNoProfile
	}

	// The profile records which thread the transaction ran on; falling back to
	// every thread is better than showing nothing when it does not.
	threadID := profs[0].ActiveThreadID
	if threadID != "" && !hasThread(profs[0], threadID) {
		threadID = ""
	}
	return Fold(profs, FoldOptions{ThreadID: threadID}), nil
}

// foldV2 handles continuous profiling, where chunks are found by overlap and
// then cut down to the transaction's window.
func foldV2(ctx context.Context, pool *pgxpool.Pool, ref transactionRef) (*FlameGraph, error) {
	// The casts are load bearing: without them Postgres cannot infer the type
	// of an untyped parameter used in interval arithmetic and rejects the query.
	rows, err := pool.Query(ctx, `
		SELECT encoding, data FROM profile_chunks
		WHERE project_id = $1 AND profiler_id = $2
		  AND start_ts >= $3::timestamptz - $5::interval
		  AND start_ts <= $4::timestamptz
		  AND end_ts   >= $3::timestamptz
		ORDER BY start_ts
		LIMIT $6`,
		ref.ProjectID, ref.ProfilerID, ref.Start, ref.End,
		maxChunkDuration, maxChunksPerTransaction)
	if err != nil {
		return nil, fmt.Errorf("query profile chunks: %w", err)
	}
	profs, err := decodeRows(rows)
	if err != nil {
		return nil, err
	}
	if len(profs) == 0 {
		return nil, ErrNoProfile
	}

	return Fold(profs, FoldOptions{
		ThreadID: ref.ThreadID,
		StartNs:  ref.Start.UnixNano(),
		EndNs:    ref.End.UnixNano(),
	}), nil
}

func decodeRows(rows pgx.Rows) ([]*ingest.Profile, error) {
	defer rows.Close()

	var profs []*ingest.Profile
	for rows.Next() {
		var (
			encoding int16
			data     []byte
		)
		if err := rows.Scan(&encoding, &data); err != nil {
			return nil, fmt.Errorf("scan profile row: %w", err)
		}
		p, err := ingest.DecodeProfile(encoding, data)
		if err != nil {
			return nil, fmt.Errorf("decode profile: %w", err)
		}
		profs = append(profs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read profile rows: %w", err)
	}
	return profs, nil
}

func hasThread(p *ingest.Profile, threadID string) bool {
	for _, s := range p.Samples {
		if s.ThreadID == threadID {
			return true
		}
	}
	return false
}
