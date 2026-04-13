package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/operator"
	"yuanbohan/tunnel/internal/relay/session"
)

func CreateInvites(operatorSvc *operator.OperatorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if operatorSvc == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		var req types.OperatorCreateInvitesRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
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

func ListInvites(operatorSvc *operator.OperatorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if operatorSvc == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		invites, err := operatorSvc.ListInviteCodes(c.Request.Context())
		if err != nil {
			WriteOperatorError(c.Writer, err)
			return
		}

		now := time.Now().UTC()
		resp := make([]types.OperatorInviteCodeListEntry, 0, len(invites))
		for _, invite := range invites {
			disabledAt := unixTime(invite.DisabledAt)
			consumedAt := unixTime(invite.ConsumedAt)
			expired := !invite.ExpiresAt.After(now)
			disabled := invite.DisabledAt != nil
			consumed := invite.ConsumedAt != nil
			resp = append(resp, types.OperatorInviteCodeListEntry{
				Code:               invite.Code,
				CreatedBy:          invite.CreatedBy,
				CreatedAt:          invite.CreatedAt.Unix(),
				ExpiresAt:          invite.ExpiresAt.Unix(),
				Expired:            expired,
				Available:          !disabled && !consumed && !expired,
				Disabled:           disabled,
				DisabledAt:         disabledAt,
				DisabledBy:         invite.DisabledBy,
				Consumed:           consumed,
				ConsumedAt:         consumedAt,
				ConsumedByUserID:   invite.ConsumedByUserID,
				ConsumedByUsername: invite.ConsumedByUsername,
			})
		}
		WriteJSON(c.Writer, http.StatusOK, types.OperatorInviteCodesListResponse{
			Invites: resp,
		})
	}
}

func DisableInvite(operatorSvc *operator.OperatorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if operatorSvc == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		var req types.OperatorDisableInviteRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := operatorSvc.DisableInviteCode(c.Request.Context(), req.Code); err != nil {
			WriteOperatorError(c.Writer, err)
			return
		}
		WriteJSON(c.Writer, http.StatusOK, nil)
	}
}

func DeleteUser(operatorSvc *operator.OperatorService, registry *session.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if operatorSvc == nil || registry == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		var req types.OperatorDeleteUserRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &req); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		result, err := operatorSvc.DeleteUser(c.Request.Context(), req.Username)
		if err != nil {
			WriteOperatorError(c.Writer, err)
			return
		}
		registry.DisconnectUserSessions(result.UserID, "account_deleted")
		WriteJSON(c.Writer, http.StatusOK, nil)
	}
}
