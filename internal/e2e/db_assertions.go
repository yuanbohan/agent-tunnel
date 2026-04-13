package e2e

import (
	"context"
	"database/sql"
	"time"
)

type UserRow struct {
	ID           int64
	Username     string
	UsernameNorm string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type InviteRow struct {
	ID               int64
	ConsumedAt       sql.NullTime
	ConsumedByUserID sql.NullInt64
	ConsumedByUsername string
}

type AppSessionRow struct {
	ID           string
	RevokedAt    sql.NullTime
	RevokeReason string
}

type AgentTokenRow struct {
	ID         string
	Name       string
	LastUsedAt sql.NullTime
	RevokedAt  sql.NullTime
}

func loadUserByUsername(ctx context.Context, db *sql.DB, username string) (UserRow, error) {
	var row UserRow
	err := db.QueryRowContext(ctx, `
		select id, username, username_norm, created_at, updated_at
		from users
		where username_norm = $1
	`, username).Scan(
		&row.ID,
		&row.Username,
		&row.UsernameNorm,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	return row, err
}

func loadInviteForUser(ctx context.Context, db *sql.DB, userID int64) (InviteRow, error) {
	var row InviteRow
	err := db.QueryRowContext(ctx, `
		select id, consumed_at, consumed_by_user_id, consumed_by_username
		from invite_codes
		where consumed_by_user_id = $1
		order by id desc
		limit 1
	`, userID).Scan(
		&row.ID,
		&row.ConsumedAt,
		&row.ConsumedByUserID,
		&row.ConsumedByUsername,
	)
	return row, err
}

func loadAppSessionsForUser(ctx context.Context, db *sql.DB, userID int64) ([]AppSessionRow, error) {
	rows, err := db.QueryContext(ctx, `
		select id, revoked_at, revoke_reason
		from app_sessions
		where user_id = $1
		order by created_at asc, id asc
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppSessionRow
	for rows.Next() {
		var row AppSessionRow
		if err := rows.Scan(&row.ID, &row.RevokedAt, &row.RevokeReason); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func loadAgentTokensForUser(ctx context.Context, db *sql.DB, userID int64) ([]AgentTokenRow, error) {
	rows, err := db.QueryContext(ctx, `
		select id, name, last_used_at, revoked_at
		from agent_tokens
		where user_id = $1
		order by created_at asc, id asc
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentTokenRow
	for rows.Next() {
		var row AgentTokenRow
		if err := rows.Scan(&row.ID, &row.Name, &row.LastUsedAt, &row.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
