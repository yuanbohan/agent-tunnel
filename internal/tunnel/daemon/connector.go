package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kballard/go-shellquote"
	"yuanbohan/tunnel/internal/protocol"
)

type deviceConnector struct {
	baseURL string
	token   string
	dialer  *websocket.Dialer
	state   *runtimeState
	recipe  LauncherRecipe
}

type launchHandler struct {
	baseURL   string
	authToken string
	paths     Paths
	state     *runtimeState
	recipe    LauncherRecipe
	inFlight  bool
}

type launchResult struct {
	Accepted bool
	Reason   string
}

func newDeviceConnector(baseURL, token string, state *runtimeState, recipe LauncherRecipe) *deviceConnector {
	return &deviceConnector{
		baseURL: baseURL,
		token:   token,
		dialer:  websocket.DefaultDialer,
		state:   state,
		recipe:  recipe,
	}
}

func (c *deviceConnector) Run(ctx context.Context, handler *launchHandler) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.serveOnce(ctx, handler); err != nil && ctx.Err() == nil {
			c.state.setRelayConnected(false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 15*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (c *deviceConnector) serveOnce(ctx context.Context, handler *launchHandler) error {
	wsURL, err := deviceWebSocketURL(c.baseURL)
	if err != nil {
		return err
	}
	headers := http.Header{
		"Authorization": {"Bearer " + c.token},
	}
	conn, _, err := c.dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	status := c.state.snapshot()
	info := protocol.DeviceInfo{
		DeviceID:       status.DeviceID,
		DisplayName:    status.DisplayName,
		PlatformFamily: status.PlatformFamily,
		PlatformID:     status.PlatformID,
	}
	if err := conn.WriteJSON(protocol.DeviceRegisterFrame(info)); err != nil {
		return err
	}
	c.state.setRelayConnected(true)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var frame protocol.DeviceFrame
		if err := conn.ReadJSON(&frame); err != nil {
			return err
		}
		if frame.Type != "launch_request" || frame.RequestID == "" {
			continue
		}
		result := handler.Handle(frame.Command)
		if err := conn.WriteJSON(protocol.DeviceLaunchResultFrame(frame.RequestID, result.Accepted, result.Reason)); err != nil {
			return err
		}
	}
}

func deviceWebSocketURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/device/ws"
	return parsed.String(), nil
}

func newLaunchHandler(baseURL, authToken string, paths Paths, state *runtimeState, recipe LauncherRecipe) *launchHandler {
	return &launchHandler{
		baseURL:   baseURL,
		authToken: authToken,
		paths:     paths,
		state:     state,
		recipe:    recipe,
	}
}

func (h *launchHandler) Handle(command string) launchResult {
	return h.handle(command)
}

func (h *launchHandler) handle(command string) launchResult {
	h.state.mu.Lock()
	if h.inFlight {
		h.state.mu.Unlock()
		return launchResult{Accepted: false, Reason: "busy"}
	}
	h.inFlight = true
	h.state.mu.Unlock()
	defer func() {
		h.state.mu.Lock()
		h.inFlight = false
		h.state.mu.Unlock()
	}()

	if !hasDesktopSession() {
		h.state.setLastFailure("desktop_unavailable", false)
		return launchResult{Accepted: false, Reason: "desktop_unavailable"}
	}

	args, err := shellquote.Split(command)
	if err != nil || len(args) == 0 {
		h.state.setLastFailure("command_not_allowed", false)
		return launchResult{Accepted: false, Reason: "command_not_allowed"}
	}
	config, err := LoadConfig(h.paths)
	if err != nil {
		h.state.setLastFailure("command_not_allowed", false)
		return launchResult{Accepted: false, Reason: "command_not_allowed"}
	}
	if !config.Allows(args[0]) {
		h.state.setLastFailure("command_not_allowed", false)
		return launchResult{Accepted: false, Reason: "command_not_allowed"}
	}

	if _, err := exec.LookPath("tunnel"); err != nil {
		h.state.setLastFailure("tunnel_not_found", false)
		return launchResult{Accepted: false, Reason: "tunnel_not_found"}
	}

	wrapper := buildShellWrapper(h.baseURL, h.authToken, args)
	if err := launchWithRecipe(h.recipe, wrapper); err != nil {
		h.state.setLastFailure("terminal_launch_failed", true)
		return launchResult{Accepted: false, Reason: "terminal_launch_failed"}
	}

	h.state.clearLastFailure()
	return launchResult{Accepted: true}
}

func buildShellWrapper(baseURL, authToken string, args []string) string {
	var parts []string
	parts = append(parts, `__tunnel_had_auth="${TUNNEL_AUTH_TOKEN+1}"`)
	parts = append(parts, `__tunnel_prev_auth="$TUNNEL_AUTH_TOKEN"`)
	parts = append(parts, `__tunnel_had_base="${TUNNEL_BASE_URL+1}"`)
	parts = append(parts, `__tunnel_prev_base="$TUNNEL_BASE_URL"`)
	parts = append(parts,
		"TUNNEL_BASE_URL="+shellquote.Join(baseURL)+
			" TUNNEL_AUTH_TOKEN="+shellquote.Join(authToken)+
			" "+shellquote.Join(append([]string{"tunnel", "run"}, args...)...))
	parts = append(parts, `if [ -n "$__tunnel_had_auth" ]; then export TUNNEL_AUTH_TOKEN="$__tunnel_prev_auth"; else unset TUNNEL_AUTH_TOKEN; fi`)
	parts = append(parts, `if [ -n "$__tunnel_had_base" ]; then export TUNNEL_BASE_URL="$__tunnel_prev_base"; else unset TUNNEL_BASE_URL; fi`)
	parts = append(parts, `unset __tunnel_had_auth __tunnel_prev_auth __tunnel_had_base __tunnel_prev_base`)
	parts = append(parts, `exec "${SHELL:-/bin/sh}" -l`)
	return strings.Join(parts, "; ")
}
