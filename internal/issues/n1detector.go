package issues

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// n1Threshold is the minimum number of identical db spans in a single transaction
// before it is classified as an N+1 query.
const n1Threshold = 5

type N1Detector struct {
	pool *pgxpool.Pool
}

func NewN1Detector(pool *pgxpool.Pool) *N1Detector {
	return &N1Detector{pool: pool}
}

// ProcessBatch is the hook called by TransactionBuffer after each write batch.
func (d *N1Detector) ProcessBatch(ctx context.Context, pool *pgxpool.Pool, txs []ingest.BufferedTransaction, txIDs []string) {
	for i, tx := range txs {
		if txIDs[i] == "" {
			continue
		}
		d.detectTx(ctx, tx, txIDs[i])
	}
}

func (d *N1Detector) detectTx(ctx context.Context, tx ingest.BufferedTransaction, txID string) {
	type group struct {
		count   int
		totalMs int
	}
	groups := make(map[string]*group)
	for _, sp := range tx.Spans {
		if !strings.HasPrefix(sp.Op, "db") {
			continue
		}
		desc := strings.TrimSpace(sp.Description)
		if desc == "" {
			continue
		}
		if g, ok := groups[desc]; ok {
			g.count++
			g.totalMs += sp.DurationMs
		} else {
			groups[desc] = &group{count: 1, totalMs: sp.DurationMs}
		}
	}

	for desc, g := range groups {
		if g.count < n1Threshold {
			continue
		}

		fp := n1Fingerprint(tx.ProjectID, tx.Transaction, desc)
		title := fmt.Sprintf("N+1 Query: %s in %s", truncate(desc, 120), truncate(tx.Transaction, 80))

		issue, _, _, err := storage.UpsertIssue(ctx, d.pool,
			tx.ProjectID, fp, title, "performance", "n1_query",
			tx.Environment, "", time.Now())
		if err != nil {
			slog.Error("n1 upsert issue", "err", err)
			continue
		}

		if err := storage.InsertPerfEvent(ctx, d.pool, issue.ID, txID, g.count, g.totalMs); err != nil {
			slog.Error("n1 insert perf event", "err", err)
		}
	}
}

func n1Fingerprint(projectID, transactionName, queryDesc string) string {
	h := sha256.Sum256([]byte("n1:" + projectID + ":" + transactionName + ":" + queryDesc))
	return hex.EncodeToString(h[:])
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
