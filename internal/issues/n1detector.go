package issues

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// n1Threshold is the minimum number of repeated db spans per transaction.
const n1Threshold = 5

// n1MinTotalMs is the minimum combined duration of the repeated spans.
// Matches Sentry's N_PLUS_ONE_DB_DURATION_THRESHOLD — fast queries that
// happen to repeat are not worth surfacing as issues.
const n1MinTotalMs = 50

// sqlLiterals replaces single-quoted string literals and bare numeric
// literals with ? so that queries differing only in bound values are
// treated as the same query (e.g. WHERE id = 1 and WHERE id = 2 group
// together instead of being counted separately).
var (
	reSQLString  = regexp.MustCompile(`'(?:[^'\\]|\\.)*'`)
	reSQLNumber  = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	reWhitespace = regexp.MustCompile(`\s+`)
)

func normalizeSQL(s string) string {
	s = reSQLString.ReplaceAllString(s, "?")
	s = reSQLNumber.ReplaceAllString(s, "?")
	s = strings.ToLower(strings.TrimSpace(s))
	return reWhitespace.ReplaceAllString(s, " ")
}

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
		count       int
		totalMs     int
		exampleDesc string // un-normalized form for the issue title
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
		key := normalizeSQL(desc)
		if g, ok := groups[key]; ok {
			g.count++
			g.totalMs += sp.DurationMs
		} else {
			groups[key] = &group{count: 1, totalMs: sp.DurationMs, exampleDesc: desc}
		}
	}

	for _, g := range groups {
		if g.count < n1Threshold {
			continue
		}
		if g.totalMs < n1MinTotalMs {
			continue
		}

		fp := n1Fingerprint(tx.ProjectID, tx.Transaction, g.exampleDesc)
		title := fmt.Sprintf("N+1 Query: %s in %s", truncate(g.exampleDesc, 120), truncate(tx.Transaction, 80))

		issue, _, _, err := storage.UpsertIssue(ctx, d.pool,
			tx.ProjectID, fp, title, "performance", "n1_query",
			tx.Environment, "", tx.Timestamp)
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
	h := sha256.Sum256([]byte("n1:" + projectID + ":" + transactionName + ":" + normalizeSQL(queryDesc)))
	return hex.EncodeToString(h[:])
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
