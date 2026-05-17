package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/protocol"
	relayauth "yuanbohan/tunnel/internal/relay/auth"
	relayconnectivity "yuanbohan/tunnel/internal/relay/connectivity"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
)

func SubmitPairingResponse(registry *relayconnectivity.Registry, throttle *RegisterThrottle) gin.HandlerFunc {
	return func(c *gin.Context) {
		if registry == nil {
			WriteJSONError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}

		remoteIP := httpx.RequestRemoteIP(c.Request)
		if throttle != nil {
			if allowed, retryAfter := throttle.Allow(remoteIP); !allowed {
				c.Writer.Header().Set("Retry-After", formatRetryAfter(retryAfter))
				WriteJSONError(c.Writer, http.StatusTooManyRequests, "rate_limited")
				return
			}
		}

		var pairingResponse protocol.ConnectivityPairingResponse
		if err := DecodeJSONBody(c.Writer, c.Request, &pairingResponse); err != nil {
			if throttle != nil {
				throttle.RecordFailure(remoteIP)
			}
			WriteJSONError(c.Writer, http.StatusBadRequest, "invalid_request")
			return
		}

		app := middleware.AuthenticatedApp(c)
		fingerprint, err := relayauth.NormalizeDeviceFingerprint(pairingResponse.AndroidFingerprint)
		if err != nil || fingerprint == "" || app.Session.DeviceFingerprint == "" || fingerprint != app.Session.DeviceFingerprint {
			if throttle != nil {
				throttle.RecordFailure(remoteIP)
			}
			WriteJSONError(c.Writer, http.StatusForbidden, "client_fingerprint_mismatch")
			return
		}
		if pairingResponse.AccountID != strconv.FormatInt(app.User.ID, 10) {
			if throttle != nil {
				throttle.RecordFailure(remoteIP)
			}
			WriteJSONError(c.Writer, http.StatusForbidden, "pairing_account_mismatch")
			return
		}
		if err := registry.ForwardPairingResponseFromApp(relayconnectivity.AppOwner{
			UserID:            app.User.ID,
			AppSessionID:      app.Session.ID,
			DeviceFingerprint: app.Session.DeviceFingerprint,
			SessionCreatedAt:  app.Session.CreatedAt,
		}, nil, pairingResponse.CorrelationID, pairingResponse); err != nil {
			if throttle != nil {
				throttle.RecordFailure(remoteIP)
			}
			WriteJSONError(c.Writer, http.StatusNotFound, "pairing_correlation_not_found")
			return
		}
		if throttle != nil {
			throttle.Reset(remoteIP)
		}
		WriteJSON(c.Writer, http.StatusOK, map[string]string{"status": "forwarded"})
	}
}
