package ingest

import (
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// queueInserts groups rows into bounded statements so usage triggers aggregate
// once per flush chunk. All chunks retain SendBatch's transaction semantics.
func queueInserts(batch *pgx.Batch, prefix, suffix string, rows [][]any, projectColumn int) int {
	// Each statement locks usage buckets in project order. Preserve that order
	// across every statement in a large flush, including shutdown drains. IDs
	// come from project lookup and are canonical UUID strings. Stable sorting
	// preserves duplicate-event precedence within each project.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][projectColumn].(string) < rows[j][projectColumn].(string) })
	const chunkSize = 1000 // safely below PostgreSQL's parameter limit
	statements := 0
	for start := 0; start < len(rows); start += chunkSize {
		chunk := rows[start:min(start+chunkSize, len(rows))]
		var sql strings.Builder
		sql.WriteString(prefix)
		args := make([]any, 0, len(chunk)*len(chunk[0]))
		for i, row := range chunk {
			if i > 0 {
				sql.WriteByte(',')
			}
			sql.WriteByte('(')
			for j, arg := range row {
				if j > 0 {
					sql.WriteByte(',')
				}
				args = append(args, arg)
				sql.WriteByte('$')
				sql.WriteString(strconv.Itoa(len(args)))
			}
			sql.WriteByte(')')
		}
		sql.WriteString(suffix)
		batch.Queue(sql.String(), args...)
		statements++
	}
	return statements
}
