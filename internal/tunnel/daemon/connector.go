package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
}

type launchHandler struct {
	baseURL   string
	authToken string
	paths     Paths
	state     *runtimeState
	inFlight  bool
}

type launchResult struct {
	Status           string
	Reason           string
	WorkspaceSession string
}

const launchSessionTimeout = 15 * time.Second

func newDeviceConnector(baseURL, token string, state *runtimeState) *deviceConnector {
	return &deviceConnector{
		baseURL: baseURL,
		token:   token,
		dialer:  websocket.DefaultDialer,
		state:   state,
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

	info := c.currentDeviceInfo()
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
		switch frame.Type {
		case "launch_request":
			if frame.RequestID == "" {
				continue
			}
			result := handler.Handle(ctx, frame.RequestID, frame.Command, frame.CWD, frame.Label)
			if err := conn.WriteJSON(protocol.DeviceLaunchResultFrameWithWorkspace(frame.RequestID, result.Status, result.Reason, result.WorkspaceSession)); err != nil {
				return err
			}
			if err := conn.WriteJSON(protocol.DeviceUpdateFrame(c.currentDeviceInfo())); err != nil {
				return err
			}
		case "terminate_request":
			if frame.RequestID == "" {
				continue
			}
			result := handler.Terminate(ctx, frame.WorkspaceSession)
			if err := conn.WriteJSON(protocol.DeviceTerminateResultFrame(frame.RequestID, result.Status, result.Reason)); err != nil {
				return err
			}
		}
	}
}

func (c *deviceConnector) currentDeviceInfo() protocol.DeviceInfo {
	status := c.state.snapshot()
	return protocol.DeviceInfo{
		DeviceID:       status.DeviceID,
		DisplayName:    status.DisplayName,
		PlatformFamily: status.PlatformFamily,
		PlatformID:     status.PlatformID,
		LaunchHealth:   status.LaunchHealth,
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

func newLaunchHandler(baseURL, authToken string, paths Paths, state *runtimeState) *launchHandler {
	return &launchHandler{
		baseURL:   baseURL,
		authToken: authToken,
		paths:     paths,
		state:     state,
	}
}

func (h *launchHandler) Handle(ctx context.Context, requestID, command, cwd, label string) launchResult {
	return h.handle(ctx, requestID, command, cwd, label)
}

func (h *launchHandler) handle(ctx context.Context, requestID, command, cwd, label string) launchResult {
	h.state.mu.Lock()
	if h.inFlight {
		h.state.mu.Unlock()
		return launchResult{Status: "failed", Reason: "busy"}
	}
	h.inFlight = true
	h.state.mu.Unlock()
	defer func() {
		h.state.mu.Lock()
		h.inFlight = false
		h.state.mu.Unlock()
	}()

	if err := EnsureTmuxAvailable(); err != nil {
		h.state.setLastFailure("tmux_not_found", true)
		return launchResult{Status: "failed", Reason: "tmux_not_found"}
	}

	args, err := shellquote.Split(command)
	if err != nil || len(args) == 0 {
		h.state.setLastFailure("command_not_allowed", false)
		return launchResult{Status: "failed", Reason: "command_not_allowed"}
	}
	config, err := LoadConfig(h.paths)
	if err != nil {
		h.state.setLastFailure("command_not_allowed", false)
		return launchResult{Status: "failed", Reason: "command_not_allowed"}
	}
	if !config.Allows(args[0]) {
		h.state.setLastFailure("command_not_allowed", false)
		return launchResult{Status: "failed", Reason: "command_not_allowed"}
	}

	resolvedCWD, err := resolveLaunchCWD(cwd)
	if err != nil {
		h.state.setLastFailure("path_not_found", false)
		return launchResult{Status: "failed", Reason: "path_not_found"}
	}

	info, err := os.Stat(resolvedCWD)
	if err != nil || !info.IsDir() {
		h.state.setLastFailure("path_not_found", false)
		return launchResult{Status: "failed", Reason: "path_not_found"}
	}

	if _, err := exec.LookPath("tunnel"); err != nil {
		h.state.setLastFailure("tunnel_not_found", false)
		return launchResult{Status: "failed", Reason: "tunnel_not_found"}
	}

	wrapper := buildShellWrapper(h.baseURL, h.authToken, requestID, resolvedCWD, label, args)
	launchCtx, cancel := context.WithTimeout(ctx, launchSessionTimeout)
	defer cancel()
	workspaceSession, err := CreateLaunchSession(launchCtx, h.paths, resolvedCWD, wrapper)
	if err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			h.state.setLastFailure("tmux_not_found", true)
			return launchResult{Status: "failed", Reason: "tmux_not_found"}
		}
		h.state.setLastFailure("session_start_failed", true)
		return launchResult{Status: "failed", Reason: "session_start_failed"}
	}

	h.state.clearLastFailure()
	return launchResult{Status: "accepted", WorkspaceSession: workspaceSession}
}

func (h *launchHandler) Terminate(ctx context.Context, workspaceSession string) launchResult {
	terminateCtx, cancel := context.WithTimeout(ctx, launchSessionTimeout)
	defer cancel()
	if err := TerminateWorkspaceSession(terminateCtx, h.paths, workspaceSession); err != nil {
		switch {
		case errors.Is(err, ErrTmuxNotFound):
			h.state.setLastFailure("tmux_not_found", true)
			return launchResult{Status: "failed", Reason: "tmux_not_found"}
		case errors.Is(err, ErrWorkspaceSessionNotFound):
			h.state.setLastFailure("session_not_found", false)
			return launchResult{Status: "failed", Reason: "session_not_found"}
		default:
			h.state.setLastFailure("session_terminate_failed", true)
			return launchResult{Status: "failed", Reason: "session_terminate_failed"}
		}
	}
	return launchResult{Status: "terminated"}
}

func resolveLaunchCWD(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", os.ErrNotExist
	}
	return filepath.Abs(cwd)
}

func buildShellWrapper(baseURL, authToken, launchRequestID, cwd, label string, args []string) string {
	var parts []string
	parts = append(parts, `__tunnel_had_auth="${TUNNEL_AUTH_TOKEN+1}"`)
	parts = append(parts, `__tunnel_prev_auth="$TUNNEL_AUTH_TOKEN"`)
	parts = append(parts, `__tunnel_had_base="${TUNNEL_BASE_URL+1}"`)
	parts = append(parts, `__tunnel_prev_base="$TUNNEL_BASE_URL"`)
	parts = append(parts, `__tunnel_had_launch_request="${TUNNEL_LAUNCH_REQUEST_ID+1}"`)
	parts = append(parts, `__tunnel_prev_launch_request="$TUNNEL_LAUNCH_REQUEST_ID"`)

	runArgs := []string{"tunnel", "run"}
	if label != "" {
		runArgs = append(runArgs, "--label", label)
	}
	runArgs = append(runArgs, args...)

	parts = append(parts,
		"cd "+shellquote.Join(cwd)+
			" && TUNNEL_BASE_URL="+shellquote.Join(baseURL)+
			" TUNNEL_AUTH_TOKEN="+shellquote.Join(authToken)+
			" TUNNEL_LAUNCH_REQUEST_ID="+shellquote.Join(launchRequestID)+
			" "+shellquote.Join(runArgs...))
	parts = append(parts, `if [ -n "$__tunnel_had_auth" ]; then export TUNNEL_AUTH_TOKEN="$__tunnel_prev_auth"; else unset TUNNEL_AUTH_TOKEN; fi`)
	parts = append(parts, `if [ -n "$__tunnel_had_base" ]; then export TUNNEL_BASE_URL="$__tunnel_prev_base"; else unset TUNNEL_BASE_URL; fi`)
	parts = append(parts, `if [ -n "$__tunnel_had_launch_request" ]; then export TUNNEL_LAUNCH_REQUEST_ID="$__tunnel_prev_launch_request"; else unset TUNNEL_LAUNCH_REQUEST_ID; fi`)
	parts = append(parts, `unset __tunnel_had_auth __tunnel_prev_auth __tunnel_had_base __tunnel_prev_base __tunnel_had_launch_request __tunnel_prev_launch_request`)
	parts = append(parts, `exec "${SHELL:-/bin/sh}" -l`)
	return strings.Join(parts, "; ")
}
