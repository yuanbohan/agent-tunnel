package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"yuanbohan/tunnel/internal/relay/auth"
	relayconnectivity "yuanbohan/tunnel/internal/relay/connectivity"
	"yuanbohan/tunnel/internal/relay/device"
	"yuanbohan/tunnel/internal/relay/handler/agent"
	"yuanbohan/tunnel/internal/relay/handler/api"
	connectivityhandler "yuanbohan/tunnel/internal/relay/handler/connectivity"
	devicehandler "yuanbohan/tunnel/internal/relay/handler/device"
	"yuanbohan/tunnel/internal/relay/handler/middleware"
	"yuanbohan/tunnel/internal/relay/handler/response"
	"yuanbohan/tunnel/internal/relay/handler/types"
	"yuanbohan/tunnel/internal/relay/operator"
	"yuanbohan/tunnel/internal/relay/session"
)

func New(
	registry *session.Registry,
	deviceRegistry *device.Registry,
	appAuth *auth.AppAuthService,
	agentTokens *auth.AgentTokenService,
	operatorSvc *operator.OperatorService,
) http.Handler {
	return newRouter(registry, deviceRegistry, appAuth, agentTokens, operatorSvc, api.NewRegisterThrottle(5, 10*time.Minute))
}

func newRouter(
	registry *session.Registry,
	deviceRegistry *device.Registry,
	appAuth *auth.AppAuthService,
	agentTokens *auth.AgentTokenService,
	operatorSvc *operator.OperatorService,
	throttle *api.RegisterThrottle,
) http.Handler {
	gin.SetMode(gin.ReleaseMode)

	if registry == nil {
		registry = session.NewRegistry()
	}
	if deviceRegistry == nil {
		deviceRegistry = device.NewRegistry()
	}
	if throttle == nil {
		throttle = api.NewRegisterThrottle(5, 10*time.Minute)
	}
	pairingThrottle := api.NewRegisterThrottle(10, time.Minute)

	connectivityRegistry := relayconnectivity.NewRegistry()
	connectivityTunnelHub := connectivityhandler.NewTunnelHub()

	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.Use(gin.CustomRecoveryWithWriter(gin.DefaultErrorWriter, func(c *gin.Context, recovered any) {
		response.WriteError(c.Writer, http.StatusInternalServerError, "internal_error")
		c.Abort()
	}))
	router.Use(middleware.AccessLog())
	router.NoRoute(func(c *gin.Context) {
		response.WriteError(c.Writer, http.StatusNotFound, "not_found")
	})
	router.NoMethod(func(c *gin.Context) {
		response.WriteError(c.Writer, http.StatusMethodNotAllowed, "method_not_allowed")
	})

	router.GET("/healthz", api.Healthz())
	router.GET("/internal/version", middleware.LocalOnly(), api.Version())

	router.POST(types.OperatorInviteCodesPath, middleware.OperatorAuth(), api.CreateInvites(operatorSvc))
	router.POST(types.OperatorInviteListPath, middleware.OperatorAuth(), api.ListInvites(operatorSvc))
	router.POST(types.OperatorInviteDisablePath, middleware.OperatorAuth(), api.DisableInvite(operatorSvc))
	router.POST(types.OperatorDeleteUserPath, middleware.OperatorAuth(), api.DeleteUser(operatorSvc, registry, connectivityRegistry))
	router.POST(types.OperatorUserTierPath, middleware.OperatorAuth(), api.SetUserTier(operatorSvc))

	router.POST("/api/auth/register", api.Register(appAuth, throttle))
	router.POST("/api/auth/login", api.Login(appAuth))
	router.POST("/api/auth/refresh", api.Refresh(appAuth))

	appRoutes := router.Group("/")
	appRoutes.Use(middleware.AppAuth(appAuth))
	appRoutes.POST("/api/auth/logout", api.Logout(appAuth, connectivityRegistry))
	appRoutes.POST("/api/auth/password/change", api.ChangePassword(appAuth, connectivityRegistry))
	appRoutes.GET("/api/account/policy", api.AccountPolicy())
	appRoutes.GET("/api/connectivity/app/ws", connectivityhandler.AppLegacy(connectivityRegistry, appAuth))
	appRoutes.GET("/api/connectivity/ws", connectivityhandler.App(connectivityRegistry, appAuth))
	appRoutes.GET("/api/agent-tokens", api.ListAgentTokens(agentTokens))
	appRoutes.POST("/api/agent-tokens", api.CreateAgentToken(agentTokens))
	appRoutes.DELETE("/api/agent-tokens/:tokenID", api.RevokeAgentToken(agentTokens, registry, deviceRegistry, connectivityRegistry))
	appRoutes.GET("/api/devices", api.ListDevices(deviceRegistry))
	appRoutes.GET("/api/computers", api.ListComputers(deviceRegistry))
	appRoutes.POST("/api/devices/:deviceID/launch", api.LaunchDevice(deviceRegistry))
	appRoutes.POST("/api/computers/:computerID/sessions", api.LaunchDevice(deviceRegistry))
	appRoutes.POST("/api/pairing/responses", api.SubmitPairingResponse(connectivityRegistry, pairingThrottle))

	router.GET("/agent/ws", middleware.AgentAuth(agentTokens), agent.Handle(registry, deviceRegistry, agentTokens))
	router.GET("/device/ws", middleware.AgentAuth(agentTokens), devicehandler.Handle(deviceRegistry, registry, agentTokens))
	router.GET("/connectivity/daemon/ws", middleware.AgentAuth(agentTokens), connectivityhandler.Daemon(connectivityRegistry, agentTokens))
	router.GET("/connectivity/computer/ws", middleware.AgentAuth(agentTokens), connectivityhandler.Daemon(connectivityRegistry, agentTokens))
	router.GET("/connectivity/tunnel/ws", connectivityhandler.Tunnel(connectivityRegistry, connectivityTunnelHub))

	return router
}
