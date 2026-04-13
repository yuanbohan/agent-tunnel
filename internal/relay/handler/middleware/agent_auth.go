package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/response"
)

const authenticatedAgentContextKey = "relay.authenticated_agent"

func AgentAuth(agentTokens *auth.AgentTokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if agentTokens == nil {
			response.WriteError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			c.Abort()
			return
		}

		token, ok := httpx.BearerTokenFromRequest(c.Request)
		if !ok {
			logAuthFailed(c.Request, "agent_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			response.WriteError(c.Writer, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		authenticated, err := agentTokens.Authenticate(c.Request.Context(), token)
		if err != nil {
			logAuthFailed(c.Request, "agent_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			response.WriteError(c.Writer, http.StatusUnauthorized, "unauthorized")
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
