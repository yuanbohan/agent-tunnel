package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/response"
)

const authenticatedSessionContextKey = "relay.authenticated_session"

type AuthenticatedSessionActor struct {
	User         auth.User
	AgentTokenID string
	Source       string
}

func SessionAuth(appAuth *auth.AppAuthService, agentTokens *auth.AgentTokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil && agentTokens == nil {
			response.WriteError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			c.Abort()
			return
		}

		token, ok := httpx.BearerTokenFromRequest(c.Request)
		if !ok {
			logAuthFailed(c.Request, "session_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			response.WriteError(c.Writer, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		if appAuth != nil {
			authenticated, err := appAuth.AuthenticateAccessToken(c.Request.Context(), token)
			if err == nil {
				c.Set(authenticatedSessionContextKey, AuthenticatedSessionActor{
					User:   authenticated.User,
					Source: "app",
				})
				c.Next()
				return
			}
		}

		if agentTokens != nil {
			authenticated, err := agentTokens.Authenticate(c.Request.Context(), token)
			if err == nil {
				c.Set(authenticatedSessionContextKey, AuthenticatedSessionActor{
					User:         authenticated.User,
					AgentTokenID: authenticated.Token.ID,
					Source:       "agent",
				})
				c.Next()
				return
			}
		}

		logAuthFailed(c.Request, "session_bearer")
		c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
		response.WriteError(c.Writer, http.StatusUnauthorized, "unauthorized")
		c.Abort()
	}
}

func AuthenticatedSession(c *gin.Context) AuthenticatedSessionActor {
	value, ok := c.Get(authenticatedSessionContextKey)
	if !ok {
		return AuthenticatedSessionActor{}
	}
	authenticated, _ := value.(AuthenticatedSessionActor)
	return authenticated
}
