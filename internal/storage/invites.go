package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Invite struct {
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Name      string  `json:"name,omitempty"`
	InviterID *string `json:"inviter_id,omitempty"`
	// ProjectAccess is the access the person gets, and TeamIDs the teams they join,
	// when they accept.
	ProjectAccess string     `json:"project_access"`
	TeamIDs       []string   `json:"team_ids"`
	ExpiresAt     time.Time  `json:"expires_at"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ErrInviteNeedsTeam is returned for an invitation to a user who only sees their
// teams' projects but names no team: they would land on an empty instance.
var ErrInviteNeedsTeam = errors.New("an invitation with access to teams only needs at least one team")

// CreateInvite creates an invitation to a user who sees every project.
func CreateInvite(ctx context.Context, pool *pgxpool.Pool, inviterID, email, name string) (string, error) {
	return CreateInviteForTeams(ctx, pool, inviterID, email, name, AccessAll, nil)
}

// CreateInviteForTeams creates an invitation that sets the person's project access
// and adds them to the given teams when they accept. It returns ErrInvalidAccess for
// an unknown access, ErrInviteNeedsTeam for access to teams only without a team, and
// ErrUnknownReference when a team does not exist.
func CreateInviteForTeams(ctx context.Context, pool *pgxpool.Pool, inviterID, email, name, projectAccess string, teamIDs []string) (string, error) {
	if projectAccess != AccessAll && projectAccess != AccessTeams {
		return "", ErrInvalidAccess
	}
	teamIDs = uniqueIDs(teamIDs)
	if projectAccess == AccessTeams && len(teamIDs) == 0 {
		return "", ErrInviteNeedsTeam
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var inviteID string
	err = tx.QueryRow(ctx, `
		INSERT INTO user_invites (token_hash, email, name, inviter_id, expires_at, project_access)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, '')::uuid, $5, $6)
		RETURNING id
	`, tokenHash(token), email, name, inviterID, expiresAt, projectAccess).Scan(&inviteID)
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invite_teams (invite_id, team_id)
		SELECT $1, t FROM unnest($2::uuid[]) AS t
	`, inviteID, teamIDs); err != nil {
		if isForeignKeyViolation(err) {
			return "", ErrUnknownReference
		}
		return "", fmt.Errorf("insert invite teams: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return token, nil
}

const inviteTeamIDs = `COALESCE(ARRAY(SELECT team_id FROM invite_teams WHERE invite_id = user_invites.id ORDER BY team_id), '{}')`

func GetInvite(ctx context.Context, pool *pgxpool.Pool, token string) (*Invite, error) {
	var inv Invite
	var name *string
	err := pool.QueryRow(ctx, `
		SELECT id, email, name, inviter_id, project_access, `+inviteTeamIDs+`, expires_at, accepted_at, created_at
		FROM user_invites
		WHERE token_hash = $1 AND expires_at > NOW() AND accepted_at IS NULL
	`, tokenHash(token)).Scan(&inv.ID, &inv.Email, &name, &inv.InviterID, &inv.ProjectAccess, &inv.TeamIDs, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	if name != nil {
		inv.Name = *name
	}
	return &inv, nil
}

func ListPendingInvites(ctx context.Context, pool *pgxpool.Pool) ([]*Invite, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, email, name, inviter_id, project_access, `+inviteTeamIDs+`, expires_at, accepted_at, created_at
		FROM user_invites
		WHERE expires_at > NOW() AND accepted_at IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var invites []*Invite
	for rows.Next() {
		var inv Invite
		var name *string
		if err := rows.Scan(&inv.ID, &inv.Email, &name, &inv.InviterID, &inv.ProjectAccess, &inv.TeamIDs, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if name != nil {
			inv.Name = *name
		}
		invites = append(invites, &inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return invites, nil
}

// DeleteInvite removes an invite by its UUID (the stable admin identifier).
func DeleteInvite(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM user_invites WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

var (
	ErrInviteUserLimit        = errors.New("user limit reached")
	ErrInvitePasswordTooShort = fmt.Errorf("password must be at least %d characters", minPasswordLen)
	ErrInvitePasswordTooLong  = fmt.Errorf("password must be at most %d characters", maxPasswordLen)
)

// AcceptInviteWithSession claims a live invitation and creates the account and
// session atomically. A nil user means the invitation is no longer usable.
func AcceptInviteWithSession(ctx context.Context, pool *pgxpool.Pool, token, password, name string, userLimit int) (*User, *Session, error) {
	if len(password) < minPasswordLen {
		return nil, nil, ErrInvitePasswordTooShort
	}
	if len(password) > maxPasswordLen {
		return nil, nil, ErrInvitePasswordTooLong
	}
	// Reject arbitrary invalid tokens without taking the instance-wide admission
	// lock. The transactional claim below remains authoritative after this read.
	invite, err := GetInvite(ctx, pool, token)
	if err != nil {
		return nil, nil, fmt.Errorf("look up invitation: %w", err)
	}
	if invite == nil {
		return nil, nil, nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin invitation acceptance: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Match OAuth admission's lock order: users table, then invitation row. This
	// serializes quota checks with all user inserts, including concurrent SSO.
	if _, err := tx.Exec(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, nil, fmt.Errorf("lock invitation admission: %w", err)
	}
	var inviteID, email, invitedName, projectAccess string
	err = tx.QueryRow(ctx, `UPDATE user_invites SET accepted_at=clock_timestamp()
		WHERE token_hash=$1 AND accepted_at IS NULL AND expires_at>clock_timestamp()
		RETURNING id, email, COALESCE(name, ''), project_access`, tokenHash(token)).Scan(&inviteID, &email, &invitedName, &projectAccess)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("claim invitation: %w", err)
	}
	if userLimit > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
			return nil, nil, fmt.Errorf("count users: %w", err)
		}
		if count >= userLimit {
			return nil, nil, ErrInviteUserLimit
		}
	}
	user, err := createUser(ctx, tx, email, password)
	if err != nil {
		return nil, nil, err
	}
	if name == "" {
		name = invitedName
	}
	if name != "" {
		if _, err := tx.Exec(ctx, `UPDATE users SET name=$2 WHERE id=$1`, user.ID, name); err != nil {
			return nil, nil, fmt.Errorf("set invited user name: %w", err)
		}
		user.Name = name
	}
	if err := applyInviteAccess(ctx, tx, inviteID, projectAccess, user.ID); err != nil {
		return nil, nil, err
	}
	user.ProjectAccess = projectAccess
	// The new account is not visible outside this transaction yet. A later
	// password reset cannot race ahead of issuance and will revoke this session.
	session, err := createSession(ctx, tx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	return user, session, nil
}

// applyInviteAccess gives a newly created user the project access their invitation
// named and adds them to its teams. A team deleted in the meantime is simply gone from
// the invitation, so the account is still created.
func applyInviteAccess(ctx context.Context, tx pgx.Tx, inviteID, projectAccess, userID string) error {
	if projectAccess == AccessTeams {
		if _, err := tx.Exec(ctx, `UPDATE users SET project_access = $2 WHERE id = $1`, userID, AccessTeams); err != nil {
			if isCheckViolation(err) {
				return ErrRestrictedUserPermissions
			}
			return fmt.Errorf("set invited user access: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_members (team_id, user_id)
		SELECT team_id, $2 FROM invite_teams WHERE invite_id = $1
		ON CONFLICT DO NOTHING
	`, inviteID, userID); err != nil {
		return fmt.Errorf("join invited teams: %w", err)
	}
	return nil
}

// HasPendingInvite reports whether an unaccepted, unexpired invite exists for
// email. A user must not take over an address someone has been invited with,
// since that person's first SSO login would then attach to the wrong account.
func HasPendingInvite(ctx context.Context, pool *pgxpool.Pool, email string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_invites
			WHERE lower(email) = lower($1) AND accepted_at IS NULL AND expires_at > NOW()
		)
	`, email).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query pending invite: %w", err)
	}
	return exists, nil
}
