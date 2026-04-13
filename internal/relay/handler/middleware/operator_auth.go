package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/response"
)

func OperatorAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		operatorToken := strings.TrimSpace(config.RelayOperatorToken())
		if operatorToken == "" {
			response.WriteError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			c.Abort()
			return
		}
		if !httpx.IsLoopbackRequest(c.Request) || httpx.HasForwardedProxyHeaders(c.Request) {
			logAuthFailed(c.Request, "operator_local")
			response.WriteError(c.Writer, http.StatusForbidden, "forbidden")
			c.Abort()
			return
		}
		if !httpx.StaticBearerAuth(c.Request, operatorToken) {
			logAuthFailed(c.Request, "operator_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			response.WriteError(c.Writer, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		c.Next()
	}
}
