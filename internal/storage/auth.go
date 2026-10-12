package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the work factor used when hashing passwords.
// Tests lower this to bcrypt.MinCost for speed.
var BcryptCost = 12

// dummyHash is pre-computed so we can run a constant-time bcrypt comparison
// when a user is not found or their account is locked, preventing user
// enumeration and lockout detection via response timing.
var dummyHash []byte

func init() {
	var err error
	if dummyHash, err = bcrypt.GenerateFromPassword([]byte("tindra-timing-dummy-v1"), 12); err != nil {
		panic(err)
	}
}

type UserPermissions struct {
	ManageProjects bool `json:"manage_projects"`
	ManageUsers    bool `json:"manage_users"`
	ManageAlerts   bool `json:"manage_alerts"`
	ManageIssues   bool `json:"manage_issues"`
}

type User struct {
	ID           string          `json:"id"`
	Email        string          `json:"email"`
	Name         string          `json:"name"`
	PasswordHash string          `json:"-"`
	HasPassword  bool            `json:"has_password"`
	MFAEnabled   bool            `json:"mfa_enabled"`
	WeeklyDigest bool            `json:"weekly_digest"`
	Timezone     string          `json:"timezone"`
	Permissions  UserPermissions `json:"permissions"`
	CreatedAt    time.Time       `json:"created_at"`
}

// ErrInvalidPassword is returned when a password verification fails.
var ErrInvalidPassword = errors.New("invalid password")

type Session struct {
	Token     string    `json:"-"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	minPasswordLen = 12
	// bcrypt silently truncates inputs beyond 72 bytes, so two passwords that
	// differ only after byte 72 would produce identical hashes.
	maxPasswordLen = 72
)

func createUser(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, email, password string) (*User, error) {
	if len(password) < minPasswordLen {
		return nil, fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return nil, fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	var u User
	// The first user created gets all permissions so the instance is usable without manual SQL.
	err = db.QueryRow(ctx, `
		WITH is_first AS (SELECT NOT EXISTS (SELECT 1 FROM users) AS v)
		INSERT INTO users (email, password_hash,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues)
		SELECT $1, $2, v, v, v, v FROM is_first
		RETURNING id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues, created_at
	`, strings.ToLower(email), string(hash)).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

// CreateAdminUser creates a user with all permissions granted. Used by the CLI
// where the operator already has database/server access and explicitly intends
// to create a fully privileged account.
func CreateAdminUser(ctx context.Context, pool *pgxpool.Pool, email, name, password string) (*User, error) {
	if len(password) < minPasswordLen {
		return nil, fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return nil, fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	var u User
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues)
		VALUES ($1, $2, $3, true, true, true, true)
		RETURNING id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues, created_at
	`, strings.ToLower(email), name, string(hash)).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

func GetUserByID(ctx context.Context, pool *pgxpool.Pool, id string) (*User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		SELECT id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues,
			created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

func GetUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (*User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		SELECT id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues,
			created_at
		FROM users WHERE email = $1
	`, strings.ToLower(email)).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

func AuthenticateUser(ctx context.Context, pool *pgxpool.Pool, email, password string) (*User, error) {
	var u User
	var failedAttempts int
	var lockedUntil *time.Time

	err := pool.QueryRow(ctx, `
		SELECT id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues,
			created_at, failed_attempts, locked_until
		FROM users WHERE email = $1
	`, strings.ToLower(email)).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues,
		&u.CreatedAt, &failedAttempts, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	if lockedUntil != nil && time.Now().Before(*lockedUntil) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		_, _ = pool.Exec(ctx, `
			UPDATE users SET
				failed_attempts = failed_attempts + 1,
				locked_until = CASE WHEN failed_attempts + 1 >= 5
					THEN NOW() + INTERVAL '15 minutes'
					ELSE locked_until
				END
			WHERE id = $1
		`, u.ID)
		return nil, nil
	}

	// Reset lockout state on successful authentication.
	_, _ = pool.Exec(ctx, `
		UPDATE users SET failed_attempts = 0, locked_until = NULL WHERE id = $1
	`, u.ID)

	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

func ListUsers(ctx context.Context, pool *pgxpool.Pool) ([]*User, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues,
			created_at
		FROM users ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
			&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
			&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		u.HasPassword = u.PasswordHash != ""
		users = append(users, &u)
	}
	return users, rows.Err()
}

func CountUsers(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

func DeleteUser(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func UpdateUserPermissions(ctx context.Context, pool *pgxpool.Pool, id string, perms UserPermissions) (*User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		UPDATE users
		SET perm_manage_projects = $2,
		    perm_manage_users    = $3,
		    perm_manage_alerts   = $4,
		    perm_manage_issues   = $5
		WHERE id = $1
		RETURNING id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues,
			created_at
	`, id, perms.ManageProjects, perms.ManageUsers, perms.ManageAlerts, perms.ManageIssues).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update permissions: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

func UpdateUserProfile(ctx context.Context, pool *pgxpool.Pool, id, name, email, timezone string) (*User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		UPDATE users SET name = $1, email = $2, timezone = $3 WHERE id = $4
		RETURNING id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues,
			created_at
	`, name, strings.ToLower(email), timezone, id).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("update profile: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

// VerifyUserPassword reports whether password matches the user's current
// password. Users without a password (SSO-only) never match.
func VerifyUserPassword(ctx context.Context, pool *pgxpool.Pool, userID, password string) (bool, error) {
	var hash string
	err := pool.QueryRow(ctx, `SELECT COALESCE(password_hash, '') FROM users WHERE id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query password: %w", err)
	}
	if hash == "" || password == "" {
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}

// ErrEmailTaken means another user already has the requested email address.
var ErrEmailTaken = errors.New("email already in use")

// ChangeUserPasswordWithSession rotates the requesting session in the same transaction.
func ChangeUserPasswordWithSession(ctx context.Context, pool *pgxpool.Pool, userID, currentPassword, newPassword, currentSession string) (*Session, error) {
	if currentSession == "" {
		return nil, ErrInvalidPassword
	}
	return changeUserPassword(ctx, pool, userID, currentPassword, newPassword, currentSession)
}

func changeUserPassword(ctx context.Context, pool *pgxpool.Pool, userID, currentPassword, newPassword, currentSession string) (*Session, error) {
	if len(newPassword) < minPasswordLen {
		return nil, fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(newPassword) > maxPasswordLen {
		return nil, fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var hash string
	err = tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	if hash == "" {
		return nil, fmt.Errorf("account has no password set")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(currentPassword)) != nil {
		return nil, ErrInvalidPassword
	}

	if currentSession != "" {
		var valid bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=$1 AND user_id=$2 AND expires_at>NOW())`, tokenHash(currentSession), userID).Scan(&valid)
		if err != nil {
			return nil, fmt.Errorf("get current session: %w", err)
		}
		if !valid {
			return nil, ErrInvalidPassword
		}
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	_, err = tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, failed_attempts = 0, locked_until = NULL, mfa_pending_secret = NULL, mfa_pending_expires_at = NULL WHERE id = $2`,
		string(newHash), userID,
	)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	if err := revokeCredentialArtifacts(ctx, tx, userID); err != nil {
		return nil, err
	}
	var session *Session
	if currentSession != "" {
		session, err = createSession(ctx, tx, userID)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit password change: %w", err)
	}
	return session, nil
}

func createSession(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID string) (*Session, error) {
	token, err := generateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	s := Session{Token: token} // plaintext token goes to cookie; only the hash is stored
	err = db.QueryRow(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)
		RETURNING user_id, expires_at, created_at
	`, tokenHash(token), userID, expiresAt).Scan(&s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	return &s, nil
}

// SessionIdentity contains only the fields required by authentication middleware.
type SessionIdentity struct {
	MFAEnabled  bool
	UserID      string
	Permissions UserPermissions
}

func GetSessionIdentity(ctx context.Context, pool *pgxpool.Pool, token string) (*SessionIdentity, error) {
	var identity SessionIdentity
	err := pool.QueryRow(ctx, `
		SELECT s.user_id, u.mfa_enabled, u.perm_manage_projects, u.perm_manage_users,
			u.perm_manage_alerts, u.perm_manage_issues
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > NOW()
	`, tokenHash(token)).Scan(&identity.UserID, &identity.MFAEnabled, &identity.Permissions.ManageProjects,
		&identity.Permissions.ManageUsers, &identity.Permissions.ManageAlerts, &identity.Permissions.ManageIssues)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query session identity: %w", err)
	}
	return &identity, nil
}

// DeleteSessionReturningUserID deletes the session and returns the owning user
// ID in a single round-trip. Returns an empty string if the session didn't exist.
func DeleteSessionReturningUserID(ctx context.Context, pool *pgxpool.Pool, token string) (string, error) {
	var userID string
	err := pool.QueryRow(ctx, `DELETE FROM sessions WHERE token_hash = $1 RETURNING user_id`, tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("delete: %w", err)
	}
	return userID, nil
}

func UpdateUserWeeklyDigest(ctx context.Context, pool *pgxpool.Pool, userID string, enabled bool) error {
	_, err := pool.Exec(ctx, `UPDATE users SET weekly_digest = $1 WHERE id = $2`, enabled, userID)
	if err != nil {
		return fmt.Errorf("update weekly_digest: %w", err)
	}
	return nil
}

// DigestUser is a minimal view of a user needed by the digest worker.
type DigestUser struct {
	ID    string
	Email string
	Name  string
}

// ListDigestDueUsers returns all opted-in users who have not received a
// digest this ISO week (since Monday 00:00 UTC). A calendar week rather than a
// 7-day cooldown, because send times drift by up to the window's length and a
// strict cooldown would then skip whole weeks. Pass force=true to ignore the
// time filter and return all opted-in users regardless.
func ListDigestDueUsers(ctx context.Context, pool *pgxpool.Pool, force bool) ([]DigestUser, error) {
	q := `SELECT id, email, name FROM users WHERE weekly_digest = TRUE AND email <> ''`
	if !force {
		q += ` AND (digest_last_sent_at IS NULL
			OR digest_last_sent_at < date_trunc('week', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`
	}
	q += ` ORDER BY created_at ASC`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var users []DigestUser
	for rows.Next() {
		var u DigestUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func MarkDigestSent(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET digest_last_sent_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("mark digest sent: %w", err)
	}
	return nil
}

func generateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
