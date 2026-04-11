package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
)

const authenticatedAgentContextKey = "relay.authenticated_agent"

func AgentAuth(agentTokens *auth.AgentTokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if agentTokens == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			c.Abort()
			return
		}

		token, ok := httpx.BearerTokenFromRequest(c.Request)
		if !ok {
			logAuthFailed(c.Request, "agent_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			http.Error(c.Writer, "unauthorized", http.StatusUnauthorized)
			c.Abort()
			return
		}

		authenticated, err := agentTokens.Authenticate(c.Request.Context(), token)
		if err != nil {
			logAuthFailed(c.Request, "agent_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			http.Error(c.Writer, "unauthorized", http.StatusUnauthorized)
			c.Abort()
			return
		}

		c.Set(authenticatedAgentContextKey, authenticated)
		c.Next()
	}
}

func AuthenticatedAgent(c *gin.Context) auth.AuthenticatedAgentToken {
	value, ok := c.Get(authenticatedAgentContextKey)
	if !ok {
		return auth.AuthenticatedAgentToken{}
	}
	authenticated, _ := value.(auth.AuthenticatedAgentToken)
	return authenticated
}
