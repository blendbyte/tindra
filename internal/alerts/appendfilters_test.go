package alerts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestAppendIssueFilters_noFilters(t *testing.T) {
	rule := &storage.AlertRule{}
	where, args := appendIssueFilters(rule, "project_id = $1", []any{"proj-1"})

	if where != "project_id = $1" {
		t.Errorf("where unchanged: got %q", where)
	}
	if len(args) != 1 {
		t.Errorf("args unchanged: got %d", len(args))
	}
}

func TestAppendIssueFilters_levelOnly(t *testing.T) {
	rule := &storage.AlertRule{FilterLevel: new("warning")}
	where, args := appendIssueFilters(rule, "project_id = $1", []any{"proj-1"})

	if !strings.Contains(where, "level = ANY($2::text[])") {
		t.Errorf("level clause missing: %q", where)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	levels, ok := args[1].([]string)
	if !ok {
		t.Fatalf("args[1] should be []string, got %T", args[1])
	}
	// "warning" includes fatal, error, warning
	if len(levels) != 3 {
		t.Errorf("expected 3 levels for 'warning', got %v", levels)
	}
}

func TestAppendIssueFilters_environmentOnly(t *testing.T) {
	rule := &storage.AlertRule{FilterEnvironment: new("production")}
	where, args := appendIssueFilters(rule, "project_id = $1", []any{"proj-1"})

	expected := fmt.Sprintf("AND environment = $%d", len(args))
	if !strings.Contains(where, expected) {
		t.Errorf("environment clause missing: %q", where)
	}
	if args[len(args)-1] != "production" {
		t.Errorf("environment arg: got %v", args[len(args)-1])
	}
}

func TestAppendIssueFilters_minOccurrencesOnly(t *testing.T) {
	rule := &storage.AlertRule{MinOccurrences: new(5)}
	where, args := appendIssueFilters(rule, "project_id = $1", []any{"proj-1"})

	expected := fmt.Sprintf("AND event_count >= $%d", len(args))
	if !strings.Contains(where, expected) {
		t.Errorf("min_occurrences clause missing: %q", where)
	}
	if args[len(args)-1] != 5 {
		t.Errorf("min_occurrences arg: got %v", args[len(args)-1])
	}
}

func TestAppendIssueFilters_allThree(t *testing.T) {
	rule := &storage.AlertRule{
		FilterLevel:       new("error"),
		FilterEnvironment: new("staging"),
		MinOccurrences:    new(10),
	}
	where, args := appendIssueFilters(rule, "project_id = $1", []any{"proj-1"})

	// Should have 4 args total: original + level slice + env string + min int
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}
	if !strings.Contains(where, "level = ANY($2::text[])") {
		t.Errorf("level clause missing: %q", where)
	}
	if !strings.Contains(where, "environment = $3") {
		t.Errorf("environment clause missing: %q", where)
	}
	if !strings.Contains(where, "event_count >= $4") {
		t.Errorf("min_occurrences clause missing: %q", where)
	}
}

func TestAppendIssueFilters_levelPerformance(t *testing.T) {
	// "performance" is a separate category; levelsAtOrAbove returns only ["performance"]
	rule := &storage.AlertRule{FilterLevel: new("performance")}
	_, args := appendIssueFilters(rule, "project_id = $1", []any{"proj-1"})

	levels, ok := args[1].([]string)
	if !ok {
		t.Fatalf("args[1] should be []string, got %T", args[1])
	}
	if len(levels) != 1 || levels[0] != "performance" {
		t.Errorf("expected [performance], got %v", levels)
	}
}
