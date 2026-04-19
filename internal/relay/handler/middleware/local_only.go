package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/response"
)

func LocalOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Ensure the request is from loopback and has no proxy headers.
		// This prevents NGINX or other proxies from forwarding external traffic to this endpoint.
		if !httpx.IsLoopbackRequest(c.Request) || httpx.HasForwardedProxyHeaders(c.Request) {
			response.WriteError(c.Writer, http.StatusForbidden, "forbidden")
			c.Abort()
			return
		}
		c.Next()
	}
}
