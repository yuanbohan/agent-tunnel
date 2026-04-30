package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/auth"
	relayconnectivity "yuanbohan/tunnel/internal/relay/connectivity"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/session"
)

func Register(appAuth *auth.AppAuthService, throttle *RegisterThrottle) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
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
				WriteJSONError(c.Writer, http.StatusBadRequest, registerFailureReasonFromError(err))
				return
			}
			WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
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
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		var req types.LoginRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		issued, err := appAuth.LoginWithDeviceFingerprint(c.Request.Context(), req.Username, req.Password, req.DeviceFingerprint)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrInvalidCredentials):
				WriteJSONError(c.Writer, http.StatusUnauthorized, "invalid_credentials")
				return
			case errors.Is(err, auth.ErrInvalidDeviceFingerprint):
				WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_device_fingerprint")
				return
			}
			WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			return
		}

		WriteJSON(c.Writer, http.StatusOK, newAppSessionResponse(issued))
	}
}

func Refresh(appAuth *auth.AppAuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		var req types.RefreshRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		issued, err := appAuth.RefreshWithDeviceFingerprint(c.Request.Context(), req.RefreshToken, req.DeviceFingerprint)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidDeviceFingerprint) {
				WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_device_fingerprint")
				return
			}
			if isRefreshFailure(err) {
				WriteJSONError(c.Writer, http.StatusUnauthorized, "invalid_session")
				return
			}
			WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			return
		}

		WriteJSON(c.Writer, http.StatusOK, newAppSessionResponse(issued))
	}
}

func AccountPolicy() gin.HandlerFunc {
	return func(c *gin.Context) {
		app := middleware.AuthenticatedApp(c)
		tier, err := auth.NormalizeSubscriptionTier(app.User.SubscriptionTier)
		if err != nil {
			WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			return
		}
		WriteJSON(c.Writer, http.StatusOK, types.AccountPolicyResponse{
			AccountID: strconv.FormatInt(app.User.ID, 10),
			Tier:      tier,
		})
	}
}

func Logout(appAuth *auth.AppAuthService, registry *session.Registry, attachSessions *session.AttachSessionIndex, connectivityRegistry *relayconnectivity.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		app := middleware.AuthenticatedApp(c)
		if err := appAuth.Logout(c.Request.Context(), app); err != nil {
			if isRefreshFailure(err) {
				WriteJSONError(c.Writer, http.StatusUnauthorized, "invalid_session")
				return
			}
			WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			return
		}
		attachSessions.DisconnectAppSession(registry, app.Session.ID, "logged_out")
		if connectivityRegistry != nil {
			connectivityRegistry.DisconnectAppSession(app.Session.ID)
		}
		WriteJSON(c.Writer, http.StatusOK, nil)
	}
}

func ChangePassword(appAuth *auth.AppAuthService, registry *session.Registry, attachSessions *session.AttachSessionIndex, connectivityRegistry *relayconnectivity.Registry) gin.HandlerFunc {
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
				WriteJSONError(c.Writer, http.StatusBadRequest, "password_too_short")
			default:
				WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			}
			return
		}
		attachSessions.DisconnectUser(registry, app.User.ID, "password_changed")
		if connectivityRegistry != nil {
			connectivityRegistry.DisconnectUser(app.User.ID)
		}
		WriteJSON(c.Writer, http.StatusOK, nil)
	}
}
