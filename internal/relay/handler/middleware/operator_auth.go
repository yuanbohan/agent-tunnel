package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
)

func OperatorAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		operatorToken := strings.TrimSpace(config.RelayOperatorToken())
		if operatorToken == "" {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			c.Abort()
			return
		}
		if !httpx.IsLoopbackRequest(c.Request) || httpx.HasForwardedProxyHeaders(c.Request) {
			logAuthFailed(c.Request, "operator_local")
			http.Error(c.Writer, "forbidden", http.StatusForbidden)
			c.Abort()
			return
		}
		if !httpx.StaticBearerAuth(c.Request, operatorToken) {
			logAuthFailed(c.Request, "operator_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			http.Error(c.Writer, "unauthorized", http.StatusUnauthorized)
			c.Abort()
			return
		}

		c.Next()
	}
}
