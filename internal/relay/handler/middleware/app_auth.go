package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/relay/auth"
	"yuanbohan/tunnel/internal/relay/handler/httpx"
	"yuanbohan/tunnel/internal/relay/handler/response"
)

const authenticatedAppContextKey = "relay.authenticated_app"

func AppAuth(appAuth *auth.AppAuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appAuth == nil {
			response.WriteError(c.Writer, http.StatusServiceUnavailable, "service_unavailable")
			c.Abort()
			return
		}

		token, ok := httpx.BearerTokenFromRequest(c.Request)
		if !ok {
			logAuthFailed(c.Request, "app_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			response.WriteError(c.Writer, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		authenticated, err := appAuth.AuthenticateAccessToken(c.Request.Context(), token)
		if err != nil {
			logAuthFailed(c.Request, "app_bearer")
			c.Writer.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			response.WriteError(c.Writer, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}

		c.Set(authenticatedAppContextKey, authenticated)
		c.Next()
	}
}

func AuthenticatedApp(c *gin.Context) auth.AuthenticatedApp {
	value, ok := c.Get(authenticatedAppContextKey)
	if !ok {
		return auth.AuthenticatedApp{}
	}
	authenticated, _ := value.(auth.AuthenticatedApp)
	return authenticated
}

func logAuthFailed(r *http.Request, authType string) {
	fields := []logx.Field{
		logx.String("path", r.URL.Path),
		logx.String("auth_type", authType),
	}
	fields = append(fields, httpx.RequestLogFields(r)...)
	logx.Warn("auth_failed", fields...)
}
