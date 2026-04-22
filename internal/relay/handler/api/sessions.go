package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	relaydevice "yuanbohan/tunnel/internal/relay/device"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/session"
)

const sessionTerminateTimeout = 10 * time.Second

func ListSessions(registry *session.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		app := middleware.AuthenticatedApp(c)
		WriteJSON(c.Writer, http.StatusOK, registry.ListForUser(app.User.ID))
	}
}

func TerminateSession(sessionRegistry *session.Registry, deviceRegistry *relaydevice.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sessionRegistry == nil || deviceRegistry == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		app := middleware.AuthenticatedApp(c)
		sessionID := c.Param("sessionID")
		target, err := sessionRegistry.TerminateTargetForUser(sessionID, app.User.ID)
		if err != nil {
			switch {
			case errors.Is(err, session.ErrSessionNotFound):
				WriteJSONError(c.Writer, http.StatusNotFound, "session_not_found")
			case errors.Is(err, session.ErrSessionTerminateUnsupported):
				WriteJSONError(c.Writer, http.StatusConflict, "session_terminate_unsupported")
			default:
				WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			}
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), sessionTerminateTimeout)
		defer cancel()

		result := deviceRegistry.Terminate(ctx, app.User.ID, sessionID, relaydevice.TerminateTarget{
			DeviceID:         target.DeviceID,
			WorkspaceSession: target.WorkspaceSession,
		})
		if result.Status == relaydevice.TerminateStatusTerminated {
			sessionRegistry.RemoveForUser(sessionID, app.User.ID)
		}
		WriteJSON(c.Writer, http.StatusOK, types.TerminateSessionResponse{
			RequestID: result.RequestID,
			Status:    result.Status,
			Reason:    result.Reason,
		})
	}
}
