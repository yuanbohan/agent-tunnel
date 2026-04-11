package operator

import (
	"context"
	"time"

	"yuanbohan/tunnel/internal/relay/auth"
)

type Repository interface {
	CreateInviteCode(ctx context.Context, params auth.CreateInviteCodeParams) (auth.InviteCodeRecord, error)
	CreateInviteCodes(ctx context.Context, params []auth.CreateInviteCodeParams) error
	DisableInviteCode(ctx context.Context, codeDigest string, actor string, now time.Time) error
	DeleteUser(ctx context.Context, usernameNorm string, actor string, now time.Time) (auth.DeleteUserResult, error)
}
