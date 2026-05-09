package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/device"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/types"
)

const deviceLaunchTimeout = 25 * time.Second

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

func ListComputers(registry *device.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		app := middleware.AuthenticatedApp(c)
		devices := registry.ListForUser(app.User.ID)
		computers := make([]types.ComputerInfo, 0, len(devices))
		for _, device := range devices {
			computers = append(computers, types.ComputerInfo{
				ComputerID:     device.DeviceID,
				DisplayName:    device.DisplayName,
				PlatformFamily: device.PlatformFamily,
				PlatformID:     device.PlatformID,
				LaunchHealth:   device.LaunchHealth,
			})
		}
		WriteJSON(c.Writer, http.StatusOK, computers)
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
		cwd := strings.TrimSpace(request.CWD)
		if command == "" || cwd == "" {
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		app := middleware.AuthenticatedApp(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), deviceLaunchTimeout)
		defer cancel()

		result := registry.Launch(ctx, pathParam(c, "deviceID", "computerID"), app.User.ID, command, cwd, strings.TrimSpace(request.Label))
		WriteJSON(c.Writer, http.StatusOK, types.DeviceLaunchResponse{
			RequestID: result.RequestID,
			Status:    result.Status,
			SessionID: result.SessionID,
			Reason:    result.Reason,
		})
	}
}

func pathParam(c *gin.Context, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(c.Param(key))
		if value != "" {
			return value
		}
	}
	return ""
}
