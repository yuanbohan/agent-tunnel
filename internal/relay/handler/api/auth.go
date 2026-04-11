package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/session"
)

func Register(appAuth *auth.AppAuthService, throttle *RegisterThrottle) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		remoteIP := httpx.RequestRemoteIP(c.Request)
		if allowed, retryAfter := throttle.Allow(remoteIP); !allowed {
			c.Writer.Header().Set("Retry-After", formatRetryAfter(retryAfter))
			WriteJSONError(c.Writer, http.StatusTooManyRequests, "rate_limited")
			return
		}

		var req types.RegisterRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			throttle.RecordFailure(remoteIP)
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		user, err := appAuth.Register(c.Request.Context(), req.Username, req.Password, req.InviteCode)
		if err != nil {
			if isRegisterFailure(err) {
				throttle.RecordFailure(remoteIP)
				WriteJSONError(c.Writer, http.StatusBadRequest, "registration_failed")
				return
			}
			http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			return
		}

		throttle.Reset(remoteIP)
		WriteJSON(c.Writer, http.StatusCreated, map[string]any{
			"user_id":  user.ID,
			"username": user.UsernameNorm,
		})
	}
}

func Login(appAuth *auth.AppAuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req types.LoginRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		issued, err := appAuth.Login(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				WriteJSONError(c.Writer, http.StatusUnauthorized, "invalid_credentials")
				return
			}
			http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			return
		}

		WriteJSON(c.Writer, http.StatusOK, newAppSessionResponse(issued))
	}
}

func Refresh(appAuth *auth.AppAuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req types.RefreshRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		issued, err := appAuth.Refresh(c.Request.Context(), req.RefreshToken)
		if err != nil {
			if isRefreshFailure(err) {
				WriteJSONError(c.Writer, http.StatusUnauthorized, "invalid_session")
				return
			}
			http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			return
		}

		WriteJSON(c.Writer, http.StatusOK, newAppSessionResponse(issued))
	}
}

func Logout(appAuth *auth.AppAuthService, registry *session.Registry, attachSessions *session.AttachSessionIndex) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		app := middleware.AuthenticatedApp(c)
		if err := appAuth.Logout(c.Request.Context(), app); err != nil {
			if isRefreshFailure(err) {
				WriteJSONError(c.Writer, http.StatusUnauthorized, "invalid_session")
				return
			}
			http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			return
		}
		attachSessions.DisconnectAppSession(registry, app.Session.ID, "logged_out")
		c.Status(http.StatusNoContent)
	}
}

func ChangePassword(appAuth *auth.AppAuthService, registry *session.Registry, attachSessions *session.AttachSessionIndex) gin.HandlerFunc {
	return func(c *gin.Context) {
		app := middleware.AuthenticatedApp(c)

		var req types.ChangePasswordRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		if err := appAuth.ChangePassword(c.Request.Context(), app, req.CurrentPassword, req.NewPassword); err != nil {
			switch {
			case errors.Is(err, auth.ErrInvalidCredentials):
				WriteJSONError(c.Writer, http.StatusUnauthorized, "invalid_credentials")
			case errors.Is(err, auth.ErrInvalidPassword):
				WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			default:
				http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			}
			return
		}
		attachSessions.DisconnectUser(registry, app.User.ID, "password_changed")
		c.Status(http.StatusNoContent)
	}
}
