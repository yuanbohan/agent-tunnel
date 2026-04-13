package api

import (
	"errors"
	"net/http"
	"time"

	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/operator"
)

func WriteOperatorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, operator.ErrInvalidOperatorRequest),
		errors.Is(err, auth.ErrInvalidInviteCode),
		errors.Is(err, auth.ErrInvalidUsername):
		WriteJSONError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, auth.ErrInviteCodeNotFound):
		WriteJSONError(w, http.StatusNotFound, "invite_code_not_found")
	case errors.Is(err, auth.ErrInviteCodeDisabled):
		WriteJSONError(w, http.StatusConflict, "invite_code_disabled")
	case errors.Is(err, auth.ErrInviteCodeConsumed):
		WriteJSONError(w, http.StatusConflict, "invite_code_consumed")
	case errors.Is(err, auth.ErrInviteCodeExpired):
		WriteJSONError(w, http.StatusConflict, "invite_code_expired")
	case errors.Is(err, auth.ErrUserNotFound):
		WriteJSONError(w, http.StatusNotFound, "user_not_found")
	default:
		WriteJSONError(w, http.StatusInternalServerError, "internal_error")
	}
}

func isRegisterFailure(err error) bool {
	_, ok := registerFailureDetailsFromError(err)
	return ok
}

func registerFailureReasonFromError(err error) string {
	reason, ok := registerFailureDetailsFromError(err)
	if !ok {
		return ""
	}
	return reason
}

func registerFailureDetailsFromError(err error) (string, bool) {
	switch {
	case errors.Is(err, auth.ErrUsernameTaken):
		return "username_taken", true
	case errors.Is(err, auth.ErrInvalidPassword):
		return "password_too_short", true
	case errors.Is(err, auth.ErrInvalidInviteCode):
		return "invalid_access_code", true
	case errors.Is(err, auth.ErrInviteCodeNotFound):
		return "invite_code_not_found", true
	case errors.Is(err, auth.ErrInviteCodeExpired):
		return "invite_code_expired", true
	case errors.Is(err, auth.ErrInviteCodeDisabled):
		return "invite_code_disabled", true
	case errors.Is(err, auth.ErrInviteCodeConsumed):
		return "invite_code_consumed", true
	case errors.Is(err, auth.ErrInvalidUsername):
		return "invalid_username", true
	}
	return "", false
}

func isRefreshFailure(err error) bool {
	return errors.Is(err, auth.ErrAppSessionNotFound) ||
		errors.Is(err, auth.ErrAppSessionExpired) ||
		errors.Is(err, auth.ErrAppSessionRevoked)
}

func newAppSessionResponse(issued auth.IssuedAppSession) types.AppSessionResponse {
	expiresIn := issued.ExpiresIn
	if expiresIn < 0 {
		expiresIn = 0
	}
	return types.AppSessionResponse{
		AccessToken:  issued.AccessToken,
		RefreshToken: issued.RefreshToken,
		ExpiresIn:    int64(expiresIn / time.Second),
		TokenType:    "Bearer",
	}
}

func newAgentTokenResponse(record auth.AgentTokenRecord) types.AgentTokenResponse {
	return types.AgentTokenResponse{
		ID:         record.ID,
		Name:       record.Name,
		CreatedAt:  record.CreatedAt.Unix(),
		LastUsedAt: unixTime(record.LastUsedAt),
		RevokedAt:  unixTime(record.RevokedAt),
	}
}

func unixTime(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	value := t.Unix()
	return &value
}
