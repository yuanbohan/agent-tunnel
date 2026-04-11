package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/operator"
	"yuanbohan/tunnel/internal/relay/session"
)

func CreateInvites(operatorSvc *operator.OperatorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if operatorSvc == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req types.OperatorCreateInvitesRequest
		if err := DecodeJSONBody(c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		codes, err := operatorSvc.CreateInviteCodes(c.Request.Context(), req.Count, req.ExpiresInDays)
		if err != nil {
			WriteOperatorError(c.Writer, err)
			return
		}
		WriteJSON(c.Writer, http.StatusCreated, types.OperatorCreateInvitesResponse{Codes: codes})
	}
}

func DisableInvite(operatorSvc *operator.OperatorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if operatorSvc == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req types.OperatorDisableInviteRequest
		if err := DecodeJSONBody(c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := operatorSvc.DisableInviteCode(c.Request.Context(), req.Code); err != nil {
			WriteOperatorError(c.Writer, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func DeleteUser(operatorSvc *operator.OperatorService, registry *session.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if operatorSvc == nil {
			http.Error(c.Writer, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req types.OperatorDeleteUserRequest
		if err := DecodeJSONBody(c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		result, err := operatorSvc.DeleteUser(c.Request.Context(), req.Username)
		if err != nil {
			WriteOperatorError(c.Writer, err)
			return
		}
		registry.DisconnectUserSessions(result.UserID, "account_deleted")
		c.Status(http.StatusNoContent)
	}
}
