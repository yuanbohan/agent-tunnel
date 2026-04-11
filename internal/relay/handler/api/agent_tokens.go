package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/session"
)

func ListAgentTokens(agentTokens *auth.AgentTokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if agentTokens == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		app := middleware.AuthenticatedApp(c)
		tokens, err := agentTokens.List(c.Request.Context(), app.User.ID)
		if err != nil {
			http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			return
		}

		out := make([]types.AgentTokenResponse, 0, len(tokens))
		for _, token := range tokens {
			out = append(out, newAgentTokenResponse(token))
		}
		WriteJSON(c.Writer, http.StatusOK, out)
	}
}

func CreateAgentToken(agentTokens *auth.AgentTokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if agentTokens == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		app := middleware.AuthenticatedApp(c)
		var req types.CreateAgentTokenRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}
		created, err := agentTokens.Create(c.Request.Context(), app.User.ID, req.Name)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidAgentTokenName) {
				WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
				return
			}
			http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			return
		}
		WriteJSON(c.Writer, http.StatusCreated, types.CreatedAgentTokenResponse{
			AgentTokenResponse: newAgentTokenResponse(created.Record),
			Token:              created.Plaintext,
		})
	}
}

func RevokeAgentToken(agentTokens *auth.AgentTokenService, registry *session.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if agentTokens == nil || registry == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		app := middleware.AuthenticatedApp(c)
		tokenID := c.Param("tokenID")
		if err := agentTokens.Revoke(c.Request.Context(), app.User.ID, tokenID, app.User.UsernameNorm); err != nil {
			if errors.Is(err, auth.ErrAgentTokenNotFound) {
				WriteJSONError(c.Writer, http.StatusNotFound, "agent_token_not_found")
				return
			}
			http.Error(c.Writer, "internal server error", http.StatusInternalServerError)
			return
		}
		registry.DisconnectAgentTokenSessions(tokenID, "agent_token_revoked")
		c.Status(http.StatusNoContent)
	}
}
