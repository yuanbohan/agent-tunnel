package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/device"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/types"
)

func ListDevices(registry *device.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		app := middleware.AuthenticatedApp(c)
		WriteJSON(c.Writer, http.StatusOK, registry.ListForUser(app.User.ID))
	}
}

func LaunchDevice(registry *device.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		var request types.DeviceLaunchRequest
		if err := DecodeJSONBody(c.Writer, c.Request, &request); err != nil {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}
		command := strings.TrimSpace(request.Command)
		if command == "" {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		app := middleware.AuthenticatedApp(c)
		result := registry.Launch(c.Request.Context(), c.Param("deviceID"), app.User.ID, command)
		WriteJSON(c.Writer, http.StatusOK, types.DeviceLaunchResponse{
			Accepted: result.Accepted,
			Reason:   result.Reason,
		})
	}
}
