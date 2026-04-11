package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) RegisterUser(ctx context.Context, params RegisterUserParams) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	inviteID, err := lockInviteCode(ctx, tx, params.InviteCodeDigest, params.Now)
	if err != nil {
		return User{}, err
	}

	var user User
	err = tx.QueryRowContext(ctx, `
		insert into users(username, username_norm, password_hash, created_at, updated_at)
		values ($1, $2, $3, $4, $4)
		returning id, username, username_norm, password_hash, created_at, updated_at
	`, params.UsernameNorm, params.UsernameNorm, params.PasswordHash, params.Now).Scan(
		&user.ID,
		&user.Username,
		&user.UsernameNorm,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		update invite_codes
		set consumed_at = $2, consumed_by_user_id = $3
		where id = $1
	`, inviteID, params.Now, user.ID); err != nil {
		return User{}, err
	}

	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) FindUserByUsername(ctx context.Context, usernameNorm string) (User, error) {
	return queryUser(ctx, s.db, `
		select id, username, username_norm, password_hash, created_at, updated_at
		from users
		where username_norm = $1
	`, usernameNorm)
}

func (s *PostgresStore) FindUserByID(ctx context.Context, userID int64) (User, error) {
	return queryUser(ctx, s.db, `
		select id, username, username_norm, password_hash, created_at, updated_at
		from users
		where id = $1
	`, userID)
}

func (s *PostgresStore) ChangeUserPassword(ctx context.Context, userID int64, passwordHash string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		update users
		set password_hash = $2, updated_at = $3
		where id = $1
	`, userID, passwordHash, now)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrUserNotFound
	}

	if _, err := tx.ExecContext(ctx, `
		update app_sessions
		set revoked_at = $2, revoke_reason = 'password_changed', updated_at = $2
		where user_id = $1 and revoked_at is null
	`, userID, now); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) CreateAppSession(ctx context.Context, params CreateAppSessionParams) (AppSession, error) {
	var session AppSession
	err := s.db.QueryRowContext(ctx, `
		insert into app_sessions(
			id, user_id, access_token_digest, access_expires_at,
			refresh_token_digest, refresh_expires_at, created_at, updated_at
		)
		values ($1, $2, $3, $4, $5, $6, $7, $7)
		returning id, user_id, access_token_digest, access_expires_at,
			refresh_token_digest, refresh_expires_at,
			revoked_at, revoke_reason, created_at, updated_at
	`, params.ID, params.UserID, params.AccessTokenDigest, params.AccessExpiresAt,
		params.RefreshTokenDigest, params.RefreshExpiresAt, params.Now).Scan(
		&session.ID,
		&session.UserID,
		&session.AccessTokenDigest,
		&session.AccessExpiresAt,
		&session.RefreshTokenDigest,
		&session.RefreshExpiresAt,
		nullTimeDest(&session.RevokedAt),
		&session.RevokeReason,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	return session, err
}

func (s *PostgresStore) FindAppSessionByAccessToken(ctx context.Context, accessTokenDigest string, now time.Time) (AppSession, error) {
	session, err := queryAppSession(ctx, s.db, `
		select id, user_id, access_token_digest, access_expires_at,
			refresh_token_digest, refresh_expires_at,
			revoked_at, revoke_reason, created_at, updated_at
		from app_sessions
		where access_token_digest = $1
	`, accessTokenDigest)
	if err != nil {
		return AppSession{}, err
	}
	return validateAccessSession(session, now)
}

func (s *PostgresStore) RotateAppSessionByRefreshToken(ctx context.Context, params RotateAppSessionParams) (AppSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AppSession{}, err
	}
	defer tx.Rollback()

	session, err := queryAppSession(ctx, tx, `
		select id, user_id, access_token_digest, access_expires_at,
			refresh_token_digest, refresh_expires_at,
			revoked_at, revoke_reason, created_at, updated_at
		from app_sessions
		where refresh_token_digest = $1
		for update
	`, params.RefreshTokenDigest)
	if err != nil {
		return AppSession{}, err
	}
	if session.RevokedAt != nil {
		return AppSession{}, ErrAppSessionRevoked
	}
	if !session.RefreshExpiresAt.After(params.Now) {
		return AppSession{}, ErrAppSessionExpired
	}

	err = tx.QueryRowContext(ctx, `
		update app_sessions
		set access_token_digest = $2,
			access_expires_at = $3,
			refresh_token_digest = $4,
			refresh_expires_at = $5,
			updated_at = $6
		where id = $1
		returning id, user_id, access_token_digest, access_expires_at,
			refresh_token_digest, refresh_expires_at,
			revoked_at, revoke_reason, created_at, updated_at
	`, session.ID, params.NewAccessTokenDigest, params.NewAccessExpiresAt,
		params.NewRefreshTokenDigest, params.NewRefreshExpiresAt, params.Now).Scan(
		&session.ID,
		&session.UserID,
		&session.AccessTokenDigest,
		&session.AccessExpiresAt,
		&session.RefreshTokenDigest,
		&session.RefreshExpiresAt,
		nullTimeDest(&session.RevokedAt),
		&session.RevokeReason,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		return AppSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return AppSession{}, err
	}
	return session, nil
}

func (s *PostgresStore) RevokeAppSession(ctx context.Context, sessionID string, now time.Time, reason string) error {
	result, err := s.db.ExecContext(ctx, `
		update app_sessions
		set revoked_at = $2, revoke_reason = $3, updated_at = $2
		where id = $1 and revoked_at is null
	`, sessionID, now, reason)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrAppSessionNotFound
	}
	return nil
}

func (s *PostgresStore) CreateInviteCode(ctx context.Context, params CreateInviteCodeParams) (InviteCodeRecord, error) {
	var record InviteCodeRecord
	err := s.db.QueryRowContext(ctx, `
		insert into invite_codes(code_digest, code_hint, created_by, created_at, expires_at)
		values ($1, $2, $3, $4, $5)
		returning id, code_digest, code_hint, created_by, created_at, expires_at,
			disabled_at, disabled_by, consumed_at, consumed_by_user_id
	`, params.CodeDigest, params.CodeHint, params.CreatedBy, params.Now, params.ExpiresAt).Scan(
		&record.ID,
		&record.CodeDigest,
		&record.CodeHint,
		&record.CreatedBy,
		&record.CreatedAt,
		&record.ExpiresAt,
		nullTimeDest(&record.DisabledAt),
		&record.DisabledBy,
		nullTimeDest(&record.ConsumedAt),
		nullInt64Dest(&record.ConsumedByUserID),
	)
	return record, err
}

func (s *PostgresStore) DisableInviteCode(ctx context.Context, codeDigest string, actor string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	inviteID, err := lockInviteCode(ctx, tx, codeDigest, now)
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

func (s *PostgresStore) CreateAgentToken(ctx context.Context, params CreateAgentTokenParams) (AgentTokenRecord, error) {
	var record AgentTokenRecord
	err := s.db.QueryRowContext(ctx, `
		insert into agent_tokens(id, user_id, name, token_digest, created_at, updated_at)
		values ($1, $2, $3, $4, $5, $5)
		returning id, user_id, name, token_digest, created_at, updated_at,
			last_used_at, revoked_at, revoke_reason
	`, params.ID, params.UserID, params.Name, params.TokenDigest, params.Now).Scan(
		&record.ID,
		&record.UserID,
		&record.Name,
		&record.TokenDigest,
		&record.CreatedAt,
		&record.UpdatedAt,
		nullTimeDest(&record.LastUsedAt),
		nullTimeDest(&record.RevokedAt),
		&record.RevokeReason,
	)
	return record, err
}

func (s *PostgresStore) ListAgentTokens(ctx context.Context, userID int64) ([]AgentTokenRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, user_id, name, token_digest, created_at, updated_at,
			last_used_at, revoked_at, revoke_reason
		from agent_tokens
		where user_id = $1
		order by created_at desc, id asc
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentTokenRecord
	for rows.Next() {
		var record AgentTokenRecord
		if err := rows.Scan(
			&record.ID,
			&record.UserID,
			&record.Name,
			&record.TokenDigest,
			&record.CreatedAt,
			&record.UpdatedAt,
			nullTimeDest(&record.LastUsedAt),
			nullTimeDest(&record.RevokedAt),
			&record.RevokeReason,
		); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PostgresStore) AuthenticateAgentToken(ctx context.Context, tokenDigest string, now time.Time) (AgentTokenRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentTokenRecord{}, err
	}
	defer tx.Rollback()

	record, err := queryAgentToken(ctx, tx, `
		select id, user_id, name, token_digest, created_at, updated_at,
			last_used_at, revoked_at, revoke_reason
		from agent_tokens
		where token_digest = $1
		for update
	`, tokenDigest)
	if err != nil {
		return AgentTokenRecord{}, err
	}
	if record.RevokedAt != nil {
		return AgentTokenRecord{}, ErrAgentTokenRevoked
	}

	err = tx.QueryRowContext(ctx, `
		update agent_tokens
		set last_used_at = $2, updated_at = $2
		where id = $1
		returning id, user_id, name, token_digest, created_at, updated_at,
			last_used_at, revoked_at, revoke_reason
	`, record.ID, now).Scan(
		&record.ID,
		&record.UserID,
		&record.Name,
		&record.TokenDigest,
		&record.CreatedAt,
		&record.UpdatedAt,
		nullTimeDest(&record.LastUsedAt),
		nullTimeDest(&record.RevokedAt),
		&record.RevokeReason,
	)
	if err != nil {
		return AgentTokenRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentTokenRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) RevokeAgentToken(ctx context.Context, userID int64, tokenID string, actor string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		update agent_tokens
		set revoked_at = $3, revoke_reason = 'revoked_by_user', updated_at = $3
		where id = $1 and user_id = $2 and revoked_at is null
	`, tokenID, userID, now)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrAgentTokenNotFound
	}

	if err := insertAuditEvent(ctx, tx, AuditEvent{
		EventType:          "agent_token_revoked",
		Actor:              actor,
		TargetUserID:       &userID,
		TargetAgentTokenID: tokenID,
		MetadataJSON:       `{"reason":"revoked_by_user"}`,
		CreatedAt:          now,
	}); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) DeleteUser(ctx context.Context, usernameNorm string, actor string, now time.Time) (DeleteUserResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeleteUserResult{}, err
	}
	defer tx.Rollback()

	user, err := queryUser(ctx, tx, `
		select id, username, username_norm, password_hash, created_at, updated_at
		from users
		where username_norm = $1
		for update
	`, usernameNorm)
	if err != nil {
		return DeleteUserResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		delete from users
		where id = $1
	`, user.ID); err != nil {
		return DeleteUserResult{}, err
	}
	if err := insertAuditEvent(ctx, tx, AuditEvent{
		EventType:      "user_deleted",
		Actor:          actor,
		TargetUserID:   &user.ID,
		TargetUsername: user.UsernameNorm,
		MetadataJSON:   `{"reason":"operator_delete"}`,
		CreatedAt:      now,
	}); err != nil {
		return DeleteUserResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return DeleteUserResult{}, err
	}
	return DeleteUserResult{UserID: user.ID, UsernameNorm: user.UsernameNorm}, nil
}

type inviteStatus struct {
	id         int64
	expiresAt  time.Time
	disabledAt sql.NullTime
	consumedAt sql.NullTime
}

func lockInviteCode(ctx context.Context, tx *sql.Tx, codeDigest string, now time.Time) (int64, error) {
	var status inviteStatus
	err := tx.QueryRowContext(ctx, `
		select id, expires_at, disabled_at, consumed_at
		from invite_codes
		where code_digest = $1
		for update
	`, codeDigest).Scan(&status.id, &status.expiresAt, &status.disabledAt, &status.consumedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrInviteCodeNotFound
		}
		return 0, err
	}
	if status.disabledAt.Valid {
		return 0, ErrInviteCodeDisabled
	}
	if status.consumedAt.Valid {
		return 0, ErrInviteCodeConsumed
	}
	if !status.expiresAt.After(now) {
		return 0, ErrInviteCodeExpired
	}
	return status.id, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func queryUser(ctx context.Context, db queryer, query string, args ...any) (User, error) {
	var user User
	err := db.QueryRowContext(ctx, query, args...).Scan(
		&user.ID,
		&user.Username,
		&user.UsernameNorm,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	return user, nil
}

func queryAppSession(ctx context.Context, db queryer, query string, args ...any) (AppSession, error) {
	var session AppSession
	err := db.QueryRowContext(ctx, query, args...).Scan(
		&session.ID,
		&session.UserID,
		&session.AccessTokenDigest,
		&session.AccessExpiresAt,
		&session.RefreshTokenDigest,
		&session.RefreshExpiresAt,
		nullTimeDest(&session.RevokedAt),
		&session.RevokeReason,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AppSession{}, ErrAppSessionNotFound
		}
		return AppSession{}, err
	}
	return session, nil
}

func validateAccessSession(session AppSession, now time.Time) (AppSession, error) {
	if session.RevokedAt != nil {
		return AppSession{}, ErrAppSessionRevoked
	}
	if !session.AccessExpiresAt.After(now) {
		return AppSession{}, ErrAppSessionExpired
	}
	return session, nil
}

func queryAgentToken(ctx context.Context, db queryer, query string, args ...any) (AgentTokenRecord, error) {
	var record AgentTokenRecord
	err := db.QueryRowContext(ctx, query, args...).Scan(
		&record.ID,
		&record.UserID,
		&record.Name,
		&record.TokenDigest,
		&record.CreatedAt,
		&record.UpdatedAt,
		nullTimeDest(&record.LastUsedAt),
		nullTimeDest(&record.RevokedAt),
		&record.RevokeReason,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentTokenRecord{}, ErrAgentTokenNotFound
		}
		return AgentTokenRecord{}, err
	}
	return record, nil
}

func insertAuditEvent(ctx context.Context, tx *sql.Tx, event AuditEvent) error {
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

func nullTimeDest(dest **time.Time) any {
	return &nullTimeScanner{dest: dest}
}

type nullTimeScanner struct {
	dest **time.Time
}

func (s *nullTimeScanner) Scan(value any) error {
	var nt sql.NullTime
	if err := nt.Scan(value); err != nil {
		return err
	}
	if !nt.Valid {
		*s.dest = nil
		return nil
	}
	t := nt.Time
	*s.dest = &t
	return nil
}

func nullInt64Dest(dest **int64) any {
	return &nullInt64Scanner{dest: dest}
}

type nullInt64Scanner struct {
	dest **int64
}

func (s *nullInt64Scanner) Scan(value any) error {
	var ni sql.NullInt64
	if err := ni.Scan(value); err != nil {
		return err
	}
	if !ni.Valid {
		*s.dest = nil
		return nil
	}
	v := ni.Int64
	*s.dest = &v
	return nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}
