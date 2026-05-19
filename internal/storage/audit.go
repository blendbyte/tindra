package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditEntry struct {
	EventType string
	ActorID   *string // user UUID, nil if unknown
	ProjectID *string // nil for non-project-scoped events
	TargetID  *string // issue/token ID being acted on
	IP        string
	Details   map[string]any // stored as JSONB
}

type AuditRow struct {
	ID         string          `json:"id"`
	EventType  string          `json:"event_type"`
	ActorID    *string         `json:"actor_id"`
	ActorEmail *string         `json:"actor_email"`
	ProjectID  *string         `json:"project_id"`
	TargetID   *string         `json:"target_id"`
	IP         string          `json:"ip"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}

type AuditFilter struct {
	Kind   string
	Search string
	Limit  int
}

func ListAuditLog(ctx context.Context, pool *pgxpool.Pool, filter AuditFilter) ([]*AuditRow, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	args := []any{}
	q := `SELECT a.id, a.event_type, a.actor_id, u.email, a.project_id, a.target_id, a.ip,
		COALESCE(a.details, '{}'::jsonb), a.created_at
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_id
		WHERE TRUE`

	if filter.Kind != "" && filter.Kind != "All" {
		args = append(args, filter.Kind+"%")
		q += fmt.Sprintf(" AND a.event_type LIKE $%d", len(args))
	}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		n := len(args)
		q += fmt.Sprintf(" AND (a.event_type ILIKE $%d OR a.ip ILIKE $%d OR u.email ILIKE $%d)", n, n, n)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY a.created_at DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*AuditRow
	for rows.Next() {
		var row AuditRow
		if err := rows.Scan(
			&row.ID, &row.EventType, &row.ActorID, &row.ActorEmail,
			&row.ProjectID, &row.TargetID, &row.IP, &row.Details, &row.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// WriteAuditLog persists an audit event asynchronously. Failures are logged
// but never surfaced to callers - audit writes must not block or fail requests.
func WriteAuditLog(pool *pgxpool.Pool, entry AuditEntry) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var details []byte
		if len(entry.Details) > 0 {
			details, _ = json.Marshal(entry.Details)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO audit_log (event_type, actor_id, project_id, target_id, ip, details)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, entry.EventType, entry.ActorID, entry.ProjectID, entry.TargetID, entry.IP, details); err != nil {
			slog.Error("audit log write", "event", entry.EventType, "err", err)
		}
	}()
}
