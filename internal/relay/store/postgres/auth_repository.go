package postgres

import (
	"context"
	"time"

	"yuanbohan/tunnel/internal/relay/auth"
)

func (s *PostgresStore) RegisterUser(ctx context.Context, params auth.RegisterUserParams) (auth.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.User{}, err
	}
	defer tx.Rollback()

	inviteID, err := lockInviteCode(ctx, tx, params.InviteCode, params.Now)
	if err != nil {
		return auth.User{}, err
	}

	var user auth.User
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
			return auth.User{}, auth.ErrUsernameTaken
		}
		return auth.User{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		update invite_codes
		set consumed_at = $2, consumed_by_user_id = $3, consumed_by_username = $4
		where id = $1
	`, inviteID, params.Now, user.ID, user.UsernameNorm); err != nil {
		return auth.User{}, err
	}

	if err := tx.Commit(); err != nil {
		return auth.User{}, err
	}
	return user, nil
}

func (s *PostgresStore) FindUserByUsername(ctx context.Context, usernameNorm string) (auth.User, error) {
	return queryUser(ctx, s.db, `
		select id, username, username_norm, password_hash, created_at, updated_at
		from users
		where username_norm = $1
	`, usernameNorm)
}

func (s *PostgresStore) FindUserByID(ctx context.Context, userID int64) (auth.User, error) {
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
		return auth.ErrUserNotFound
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

func (s *PostgresStore) CreateAppSession(ctx context.Context, params auth.CreateAppSessionParams) (auth.AppSession, error) {
	var session auth.AppSession
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

func (s *PostgresStore) FindAppSessionByAccessToken(ctx context.Context, accessTokenDigest string, now time.Time, absoluteTTL time.Duration) (auth.AppSession, error) {
	session, err := queryAppSession(ctx, s.db, `
		select id, user_id, access_token_digest, access_expires_at,
			refresh_token_digest, refresh_expires_at,
			revoked_at, revoke_reason, created_at, updated_at
		from app_sessions
		where access_token_digest = $1
	`, accessTokenDigest)
	if err != nil {
		return auth.AppSession{}, err
	}
	return validateAccessSession(session, now, absoluteTTL)
}

func (s *PostgresStore) RotateAppSessionByRefreshToken(ctx context.Context, params auth.RotateAppSessionParams) (auth.AppSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.AppSession{}, err
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
		return auth.AppSession{}, err
	}
	if session.RevokedAt != nil {
		return auth.AppSession{}, auth.ErrAppSessionRevoked
	}
	effectiveNow := maxTime(params.Now, s.now())
	if isAbsoluteSessionExpired(session, effectiveNow, params.AbsoluteTTL) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
	}
	if !session.RefreshExpiresAt.After(effectiveNow) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
	}

	accessTTL := params.NewAccessExpiresAt.Sub(params.Now)
	refreshTTL := params.NewRefreshExpiresAt.Sub(params.Now)
	newAccessExpiresAt := clampSessionExpiry(session, effectiveNow.Add(accessTTL), params.AbsoluteTTL)
	newRefreshExpiresAt := clampSessionExpiry(session, effectiveNow.Add(refreshTTL), params.AbsoluteTTL)

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
	`, session.ID, params.NewAccessTokenDigest, newAccessExpiresAt,
		params.NewRefreshTokenDigest, newRefreshExpiresAt, effectiveNow).Scan(
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
		return auth.AppSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return auth.AppSession{}, err
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
		return auth.ErrAppSessionNotFound
	}
	return nil
}

func (s *PostgresStore) CreateAgentToken(ctx context.Context, params auth.CreateAgentTokenParams) (auth.AgentTokenRecord, error) {
	var record auth.AgentTokenRecord
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

func (s *PostgresStore) ListAgentTokens(ctx context.Context, userID int64) ([]auth.AgentTokenRecord, error) {
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

	var out []auth.AgentTokenRecord
	for rows.Next() {
		var record auth.AgentTokenRecord
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

func (s *PostgresStore) AuthenticateAgentToken(ctx context.Context, tokenDigest string, now time.Time) (auth.AgentTokenRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.AgentTokenRecord{}, err
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
		return auth.AgentTokenRecord{}, err
	}
	if record.RevokedAt != nil {
		return auth.AgentTokenRecord{}, auth.ErrAgentTokenRevoked
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
		return auth.AgentTokenRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return auth.AgentTokenRecord{}, err
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
		return auth.ErrAgentTokenNotFound
	}

	if err := insertAuditEvent(ctx, tx, auth.AuditEvent{
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
