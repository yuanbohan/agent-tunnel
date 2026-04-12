package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"yuanbohan/tunnel/internal/relay/auth"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
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
			return 0, auth.ErrInviteCodeNotFound
		}
		return 0, err
	}
	if status.disabledAt.Valid {
		return 0, auth.ErrInviteCodeDisabled
	}
	if status.consumedAt.Valid {
		return 0, auth.ErrInviteCodeConsumed
	}
	if !status.expiresAt.After(now) {
		return 0, auth.ErrInviteCodeExpired
	}
	return status.id, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func queryUser(ctx context.Context, db queryer, query string, args ...any) (auth.User, error) {
	var user auth.User
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
			return auth.User{}, auth.ErrUserNotFound
		}
		return auth.User{}, err
	}
	return user, nil
}

func queryAppSession(ctx context.Context, db queryer, query string, args ...any) (auth.AppSession, error) {
	var session auth.AppSession
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
			return auth.AppSession{}, auth.ErrAppSessionNotFound
		}
		return auth.AppSession{}, err
	}
	return session, nil
}

func validateAccessSession(session auth.AppSession, now time.Time) (auth.AppSession, error) {
	if session.RevokedAt != nil {
		return auth.AppSession{}, auth.ErrAppSessionRevoked
	}
	if !session.AccessExpiresAt.After(now) {
		return auth.AppSession{}, auth.ErrAppSessionExpired
	}
	return session, nil
}

func queryAgentToken(ctx context.Context, db queryer, query string, args ...any) (auth.AgentTokenRecord, error) {
	var record auth.AgentTokenRecord
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
			return auth.AgentTokenRecord{}, auth.ErrAgentTokenNotFound
		}
		return auth.AgentTokenRecord{}, err
	}
	return record, nil
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

func nullStringDest(dest *string) any {
	return &nullStringScanner{dest: dest}
}

type nullStringScanner struct {
	dest *string
}

func (s *nullStringScanner) Scan(value any) error {
	var ns sql.NullString
	if err := ns.Scan(value); err != nil {
		return err
	}
	if !ns.Valid {
		*s.dest = ""
		return nil
	}
	*s.dest = ns.String
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}
