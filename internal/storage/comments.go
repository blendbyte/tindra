package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Comment struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issue_id"`
	UserID    string    `json:"user_id"`
	UserEmail string    `json:"user_email"`
	UserName  string    `json:"user_name"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ListComments(ctx context.Context, pool *pgxpool.Pool, issueID string) ([]*Comment, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.id, c.issue_id, c.user_id, u.email, u.name, c.body, c.created_at, c.updated_at
		FROM issue_comments c
		JOIN users u ON u.id = c.user_id
		WHERE c.issue_id = $1
		ORDER BY c.created_at ASC
		LIMIT 200
	`, issueID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var comments []*Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.IssueID, &c.UserID, &c.UserEmail, &c.UserName, &c.Body, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		comments = append(comments, &c)
	}
	return comments, rows.Err()
}

func CreateComment(ctx context.Context, pool *pgxpool.Pool, issueID, userID, body string) (*Comment, error) {
	var c Comment
	err := pool.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO issue_comments (issue_id, user_id, body)
			VALUES ($1, $2, $3)
			RETURNING id, issue_id, user_id, body, created_at, updated_at
		)
		SELECT ins.id, ins.issue_id, ins.user_id, u.email, u.name, ins.body, ins.created_at, ins.updated_at
		FROM ins JOIN users u ON u.id = ins.user_id
	`, issueID, userID, body).Scan(&c.ID, &c.IssueID, &c.UserID, &c.UserEmail, &c.UserName, &c.Body, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	return &c, nil
}

// GetComment fetches a single comment by ID (used for ownership checks).
func GetComment(ctx context.Context, pool *pgxpool.Pool, commentID string) (*Comment, error) {
	var c Comment
	err := pool.QueryRow(ctx, `
		SELECT c.id, c.issue_id, c.user_id, u.email, u.name, c.body, c.created_at, c.updated_at
		FROM issue_comments c
		JOIN users u ON u.id = c.user_id
		WHERE c.id = $1
	`, commentID).Scan(&c.ID, &c.IssueID, &c.UserID, &c.UserEmail, &c.UserName, &c.Body, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &c, nil
}

func UpdateComment(ctx context.Context, pool *pgxpool.Pool, commentID, body string) (*Comment, error) {
	var c Comment
	err := pool.QueryRow(ctx, `
		WITH upd AS (
			UPDATE issue_comments SET body = $2, updated_at = now()
			WHERE id = $1
			RETURNING id, issue_id, user_id, body, created_at, updated_at
		)
		SELECT upd.id, upd.issue_id, upd.user_id, u.email, u.name, upd.body, upd.created_at, upd.updated_at
		FROM upd JOIN users u ON u.id = upd.user_id
	`, commentID, body).Scan(&c.ID, &c.IssueID, &c.UserID, &c.UserEmail, &c.UserName, &c.Body, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return &c, nil
}

func DeleteComment(ctx context.Context, pool *pgxpool.Pool, commentID string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM issue_comments WHERE id = $1`, commentID)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
