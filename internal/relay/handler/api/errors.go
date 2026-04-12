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
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func isRegisterFailure(err error) bool {
	return errors.Is(err, auth.ErrInvalidUsername) ||
		errors.Is(err, auth.ErrInvalidPassword) ||
		errors.Is(err, auth.ErrInvalidInviteCode) ||
		errors.Is(err, auth.ErrInviteCodeNotFound) ||
		errors.Is(err, auth.ErrInviteCodeExpired) ||
		errors.Is(err, auth.ErrInviteCodeDisabled) ||
		errors.Is(err, auth.ErrInviteCodeConsumed) ||
		errors.Is(err, auth.ErrUsernameTaken)
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
