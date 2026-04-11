package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/session"
)

func ListSessions(registry *session.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		app := middleware.AuthenticatedApp(c)
		WriteJSON(c.Writer, http.StatusOK, registry.ListForUser(app.User.ID))
	}
}
