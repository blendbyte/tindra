package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForTokens(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "token-test", "Token Test")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func truncateTokens(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE api_tokens CASCADE"); err != nil {
		t.Fatalf("truncate api_tokens: %v", err)
	}
}

func TestCreateAPIToken(t *testing.T) {
	p := setupProjectForTokens(t)

	token, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, p.ID, "CI deploy", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token.ID == "" {
		t.Error("expected non-empty ID")
	}
	if token.Name != "CI deploy" {
		t.Errorf("name: got %q, want %q", token.Name, "CI deploy")
	}
	if token.ProjectID != p.ID {
		t.Errorf("project_id mismatch")
	}
	if token.LastUsedAt != nil {
		t.Error("last_used_at should be nil on creation")
	}
	if !strings.HasPrefix(plaintext, "tindra_") {
		t.Errorf("plaintext should have tindra_ prefix, got %q", plaintext[:min(20, len(plaintext))])
	}
	if len(plaintext) != 71 { // "tindra_" + 64 hex chars
		t.Errorf("plaintext length: got %d, want 71", len(plaintext))
	}
}

func TestCreateAPIToken_plaintextNotRepeated(t *testing.T) {
	p := setupProjectForTokens(t)

	_, pt1, _ := storage.CreateAPIToken(context.Background(), testPool, p.ID, "tok1", false)
	_, pt2, _ := storage.CreateAPIToken(context.Background(), testPool, p.ID, "tok2", false)
	if pt1 == pt2 {
		t.Error("two tokens must not have the same plaintext")
	}
}

func TestListAPITokens(t *testing.T) {
	p := setupProjectForTokens(t)
	truncateTokens(t)

	storage.CreateAPIToken(context.Background(), testPool, p.ID, "A", false)
	storage.CreateAPIToken(context.Background(), testPool, p.ID, "B", false)

	tokens, err := storage.ListAPITokens(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2, got %d", len(tokens))
	}
}

func TestListAPITokens_empty(t *testing.T) {
	p := setupProjectForTokens(t)
	truncateTokens(t)

	tokens, err := storage.ListAPITokens(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("expected 0, got %d", len(tokens))
	}
}

func TestDeleteAPIToken(t *testing.T) {
	p := setupProjectForTokens(t)

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, p.ID, "deleteme", false)

	deleted, err := storage.DeleteAPIToken(context.Background(), testPool, tok.ID, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	// Second delete: not found
	deleted, err = storage.DeleteAPIToken(context.Background(), testPool, tok.ID, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false on second delete")
	}
}

func TestDeleteAPIToken_wrongProject(t *testing.T) {
	setupProjectForTokens(t)
	truncateProjects(t)

	// Re-create p1 since truncateProjects cascades
	p1, _ := storage.CreateProject(context.Background(), testPool, "proj-a", "Proj A")
	p2, _ := storage.CreateProject(context.Background(), testPool, "proj-b", "Proj B")

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, p1.ID, "tok", false)

	// Try to delete p1's token while passing p2's project ID
	deleted, err := storage.DeleteAPIToken(context.Background(), testPool, tok.ID, p2.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Error("should not delete a token belonging to a different project")
	}
}

func TestGetAPITokenByHash(t *testing.T) {
	p := setupProjectForTokens(t)

	_, plaintext, _ := storage.CreateAPIToken(context.Background(), testPool, p.ID, "lookup", false)
	hash := storage.HashAPIToken(plaintext)

	tok, err := storage.GetAPITokenByHash(context.Background(), testPool, hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil {
		t.Fatal("expected token, got nil")
	}
	if tok.ProjectID != p.ID {
		t.Errorf("project_id mismatch")
	}
}

func TestGetAPITokenByHash_notFound(t *testing.T) {
	tok, err := storage.GetAPITokenByHash(context.Background(), testPool, "nonexistenthash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != nil {
		t.Errorf("expected nil, got %+v", tok)
	}
}

func TestTouchAPIToken(t *testing.T) {
	p := setupProjectForTokens(t)

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, p.ID, "touch-me", false)
	if tok.LastUsedAt != nil {
		t.Error("last_used_at should be nil before touch")
	}

	// TouchAPIToken fires-and-forgets (no error return), so just check it doesn't panic
	storage.TouchAPIToken(context.Background(), testPool, tok.ID)

	// Verify last_used_at was updated
	tokens, _ := storage.ListAPITokens(context.Background(), testPool, p.ID)
	var found bool
	for _, t2 := range tokens {
		if t2.ID == tok.ID {
			found = true
		}
	}
	if !found {
		t.Error("token not found after touch")
	}
}

func TestHashAPIToken_deterministic(t *testing.T) {
	h1 := storage.HashAPIToken("tindra_abc123")
	h2 := storage.HashAPIToken("tindra_abc123")
	if h1 != h2 {
		t.Error("HashAPIToken must be deterministic")
	}
}

func TestListAllAPITokens_empty(t *testing.T) {
	setupProjectForTokens(t)
	truncateTokens(t)

	tokens, err := storage.ListAllAPITokens(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("expected 0, got %d", len(tokens))
	}
}

func TestListAllAPITokens_acrossProjects(t *testing.T) {
	truncateProjects(t)
	truncateTokens(t)

	p1, _ := storage.CreateProject(context.Background(), testPool, "all-tok-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "all-tok-p2", "P2")

	storage.CreateAPIToken(context.Background(), testPool, p1.ID, "tok-a", false)
	storage.CreateAPIToken(context.Background(), testPool, p2.ID, "tok-b", false)
	storage.CreateAPIToken(context.Background(), testPool, p2.ID, "tok-c", false)

	tokens, err := storage.ListAllAPITokens(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens across both projects, got %d", len(tokens))
	}
	// Verify tokens from both projects are present
	seen := map[string]bool{}
	for _, tok := range tokens {
		seen[tok.ProjectID] = true
	}
	if !seen[p1.ID] || !seen[p2.ID] {
		t.Error("expected tokens from both projects in ListAllAPITokens")
	}
}

func TestDeleteAPITokenByID_found(t *testing.T) {
	p := setupProjectForTokens(t)

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, p.ID, "del-by-id", false)

	deleted, err := storage.DeleteAPITokenByID(context.Background(), testPool, tok.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	// Second delete: not found
	deleted, err = storage.DeleteAPITokenByID(context.Background(), testPool, tok.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false on second delete")
	}
}

func TestDeleteAPITokenByID_notFound(t *testing.T) {
	deleted, err := storage.DeleteAPITokenByID(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false for nonexistent ID")
	}
}

func TestDeleteAPITokenByID_ignoredProjectBoundary(t *testing.T) {
	// DeleteAPITokenByID (unlike DeleteAPIToken) deletes by ID only, ignoring project.
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "dbi-p1", "P1")

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, p1.ID, "cross-del", false)

	deleted, err := storage.DeleteAPITokenByID(context.Background(), testPool, tok.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true regardless of project")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Writable field ---

func TestCreateAPIToken_writable_true(t *testing.T) {
	p := setupProjectForTokens(t)
	tok, _, err := storage.CreateAPIToken(context.Background(), testPool, p.ID, "rw", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tok.Writable {
		t.Error("expected Writable=true")
	}
}

func TestCreateAPIToken_writable_false(t *testing.T) {
	p := setupProjectForTokens(t)
	tok, _, err := storage.CreateAPIToken(context.Background(), testPool, p.ID, "ro", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.Writable {
		t.Error("expected Writable=false")
	}
}

func TestGetAPITokenByHash_writable_preserved(t *testing.T) {
	p := setupProjectForTokens(t)

	_, plaintext, _ := storage.CreateAPIToken(context.Background(), testPool, p.ID, "rw-lookup", true)
	tok, err := storage.GetAPITokenByHash(context.Background(), testPool, storage.HashAPIToken(plaintext))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil {
		t.Fatal("expected token, got nil")
	}
	if !tok.Writable {
		t.Error("GetAPITokenByHash: expected Writable=true")
	}
}

func TestListAPITokens_writable_field(t *testing.T) {
	p := setupProjectForTokens(t)
	truncateTokens(t)

	storage.CreateAPIToken(context.Background(), testPool, p.ID, "read-only", false)
	storage.CreateAPIToken(context.Background(), testPool, p.ID, "read-write", true)

	tokens, err := storage.ListAPITokens(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2, got %d", len(tokens))
	}
	writableCount := 0
	for _, tok := range tokens {
		if tok.Writable {
			writableCount++
		}
	}
	if writableCount != 1 {
		t.Errorf("expected exactly 1 writable token, got %d", writableCount)
	}
}
