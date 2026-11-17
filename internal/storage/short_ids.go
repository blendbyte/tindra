package storage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// shortIDRe matches a short issue ID such as BACKEND-42 or MY-APP-7: a project
// prefix (an upper-cased slug, possibly numbered) and an issue number.
var shortIDRe = regexp.MustCompile(`^([A-Z0-9]+(?:-[A-Z0-9]+)*)-([1-9][0-9]{0,17})$`)

// ParseShortID splits a short issue ID into its project prefix and issue
// number. It ignores case and surrounding space.
func ParseShortID(s string) (prefix string, number int64, ok bool) {
	m := shortIDRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// ResolveShortID returns the ID of the issue a short ID names, following
// merges to the issue it was merged into. It returns "" when the string is
// not a short ID or no issue has it.
func ResolveShortID(ctx context.Context, pool *pgxpool.Pool, shortID string) (string, error) {
	prefix, number, ok := ParseShortID(shortID)
	if !ok {
		return "", nil
	}
	var id *string
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			(SELECT i.id FROM issues i WHERE i.project_id = p.id AND i.short_number = $2),
			(SELECT a.issue_id FROM issue_short_aliases a WHERE a.project_id = p.id AND a.short_number = $2))
		FROM projects p WHERE p.issue_prefix = $1
	`, prefix, number).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && id == nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve short ID: %w", err)
	}
	return *id, nil
}
