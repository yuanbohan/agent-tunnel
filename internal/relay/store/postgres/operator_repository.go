package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"yuanbohan/tunnel/internal/relay/auth"
)

func (s *PostgresStore) CreateInviteCode(ctx context.Context, params auth.CreateInviteCodeParams) (auth.InviteCodeRecord, error) {
	return insertInviteCode(ctx, s.db, params)
}

func (s *PostgresStore) CreateInviteCodes(ctx context.Context, params []auth.CreateInviteCodeParams) error {
	if len(params) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, param := range params {
		if _, err := insertInviteCode(ctx, tx, param); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func insertInviteCode(ctx context.Context, db queryer, params auth.CreateInviteCodeParams) (auth.InviteCodeRecord, error) {
	var record auth.InviteCodeRecord
	err := db.QueryRowContext(ctx, `
		insert into invite_codes(code, created_by, created_at, expires_at)
		values ($1, $2, $3, $4)
		returning id, code, created_by, created_at, expires_at,
			disabled_at, disabled_by, consumed_at, consumed_by_user_id, consumed_by_username
	`, params.Code, params.CreatedBy, params.Now, params.ExpiresAt).Scan(
		&record.ID,
		&record.Code,
		&record.CreatedBy,
		&record.CreatedAt,
		&record.ExpiresAt,
		nullTimeDest(&record.DisabledAt),
		nullStringDest(&record.DisabledBy),
		nullTimeDest(&record.ConsumedAt),
		nullInt64Dest(&record.ConsumedByUserID),
		nullStringDest(&record.ConsumedByUsername),
	)
	return record, err
}

func (s *PostgresStore) ListInviteCodes(ctx context.Context) ([]auth.InviteCodeRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		select ic.id, ic.code, ic.created_by, ic.created_at, ic.expires_at,
			ic.disabled_at, ic.disabled_by, ic.consumed_at, ic.consumed_by_user_id, ic.consumed_by_username
		from invite_codes ic
		order by ic.created_at desc, ic.id desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []auth.InviteCodeRecord
	for rows.Next() {
		var row auth.InviteCodeRecord
		if err := rows.Scan(
			&row.ID,
			&row.Code,
			&row.CreatedBy,
			&row.CreatedAt,
			&row.ExpiresAt,
			nullTimeDest(&row.DisabledAt),
			nullStringDest(&row.DisabledBy),
			nullTimeDest(&row.ConsumedAt),
			nullInt64Dest(&row.ConsumedByUserID),
			nullStringDest(&row.ConsumedByUsername),
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PostgresStore) DisableInviteCode(ctx context.Context, code string, actor string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	inviteID, err := lockInviteCode(ctx, tx, code, now)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		update invite_codes
		set disabled_at = $2, disabled_by = $3
		where id = $1
	`, inviteID, now, actor); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) DeleteUser(ctx context.Context, usernameNorm string, actor string, now time.Time) (auth.DeleteUserResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.DeleteUserResult{}, err
	}
	defer tx.Rollback()

	user, err := queryUser(ctx, tx, `
		select id, username, username_norm, password_hash, created_at, updated_at
		from users
		where username_norm = $1
		for update
	`, usernameNorm)
	if err != nil {
		return auth.DeleteUserResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		delete from users
		where id = $1
	`, user.ID); err != nil {
		return auth.DeleteUserResult{}, err
	}
	if err := insertAuditEvent(ctx, tx, auth.AuditEvent{
		EventType:      "user_deleted",
		Actor:          actor,
		TargetUserID:   &user.ID,
		TargetUsername: user.UsernameNorm,
		MetadataJSON:   `{"reason":"operator_delete"}`,
		CreatedAt:      now,
	}); err != nil {
		return auth.DeleteUserResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return auth.DeleteUserResult{}, err
	}
	return auth.DeleteUserResult{UserID: user.ID, UsernameNorm: user.UsernameNorm}, nil
}

func insertAuditEvent(ctx context.Context, tx *sql.Tx, event auth.AuditEvent) error {
	metadata := event.MetadataJSON
	if metadata == "" {
		metadata = "{}"
	}
	if !json.Valid([]byte(metadata)) {
		return errors.New("invalid audit metadata")
	}
	_, err := tx.ExecContext(ctx, `
		insert into operator_audit_events(
			event_type, actor, target_user_id, target_username,
			target_agent_token_id, metadata_json, created_at
		)
		values ($1, $2, $3, $4, $5, $6::jsonb, $7)
	`, event.EventType, event.Actor, nullableInt64(event.TargetUserID), event.TargetUsername,
		event.TargetAgentTokenID, metadata, event.CreatedAt)
	return err
}
