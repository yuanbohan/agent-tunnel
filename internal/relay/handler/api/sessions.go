package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/session"
)

func ListSessions(registry *session.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		authenticated := middleware.AuthenticatedSession(c)
		WriteJSON(c.Writer, http.StatusOK, registry.ListForUser(authenticated.User.ID))
	}
}

func StopSession(sessionRegistry *session.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sessionRegistry == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		authenticated := middleware.AuthenticatedSession(c)
		sessionID := c.Param("sessionID")
		err := sessionRegistry.StopForUser(sessionID, authenticated.User.ID)
		if err != nil {
			switch {
			case errors.Is(err, session.ErrSessionNotFound):
				WriteJSONError(c.Writer, http.StatusNotFound, "session_not_found")
			case errors.Is(err, session.ErrSessionOffline):
				WriteJSONError(c.Writer, http.StatusNotFound, "session_not_found")
			default:
				WriteJSONError(c.Writer, http.StatusInternalServerError, "internal_error")
			}
			return
		}

		WriteJSON(c.Writer, http.StatusOK, types.StopSessionResponse{
			SessionID: sessionID,
			Status:    "stopped",
		})
	}
}
