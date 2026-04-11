package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
)

const (
	defaultWSSinkBufferSize       = 64
	defaultWSWriteTimeout         = 5 * time.Second
	defaultAgentReadTimeout       = 30 * time.Second
	defaultAgentPingInterval      = 10 * time.Second
	defaultAgentPingWriteTimeout  = 5 * time.Second
	defaultClientReadTimeout      = 30 * time.Second
	defaultClientPingInterval     = 10 * time.Second
	defaultClientPingWriteTimeout = 5 * time.Second
)

var (
	errWSSinkClosed         = errors.New("websocket sink closed")
	errWSSinkBackpressure   = errors.New("websocket sink backpressure")
	errInvalidAgentRegister = errors.New("invalid agent register frame")
	errAgentPeerInactive    = errors.New("agent peer inactive")
)

type HandlerConfig struct {
	Registry               *Registry
	AppAuth                *AppAuthService
	AgentTokens            *AgentTokenService
	Operator               *OperatorService
	OperatorToken          string
	RegisterThrottle       *RegisterThrottle
	Logger                 *Logger
	AgentReadTimeout       time.Duration
	AgentPingInterval      time.Duration
	AgentPingWriteTimeout  time.Duration
	ClientReadTimeout      time.Duration
	ClientPingInterval     time.Duration
	ClientPingWriteTimeout time.Duration
}

type wsAgentPeer struct {
	conn         wsConn
	tracker      *wsTrafficTracker
	writeTimeout time.Duration
	mu           sync.Mutex
	active       bool
}

type wsConn interface {
	WriteMessage(messageType int, data []byte) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

type registerRequest struct {
	InviteCode string `json:"invite_code"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type createAgentTokenRequest struct {
	Name string `json:"name"`
}

type appSessionResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type agentTokenResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type createdAgentTokenResponse struct {
	agentTokenResponse
	Token string `json:"token"`
}

func newWSAgentPeer(conn *websocket.Conn, tracker *wsTrafficTracker) *wsAgentPeer {
	return &wsAgentPeer{
		conn:         conn,
		tracker:      tracker,
		writeTimeout: defaultWSWriteTimeout,
		active:       true,
	}
}

func (p *wsAgentPeer) SendJSON(msg any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return errAgentPeerInactive
	}

	if p.writeTimeout > 0 {
		if err := p.conn.SetWriteDeadline(time.Now().Add(p.writeTimeout)); err != nil {
			if p.tracker != nil {
				p.tracker.NoteDisconnectError(err)
			}
			return err
		}
	}
	payload, err := writeWSJSON(p.conn, msg)
	if err != nil {
		if p.tracker != nil {
			p.tracker.NoteDisconnectError(err)
		}
		return err
	}
	if p.tracker != nil {
		p.tracker.RecordOutbound(len(payload))
	}
	return nil
}

func (p *wsAgentPeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = false
	return p.conn.Close()
}

func (p *wsAgentPeer) Deactivate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = false
}

func NewHandler(cfg HandlerConfig) http.Handler {
	registry := cfg.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	logger := cfg.Logger
	if logger != nil {
		registry.SetLogger(logger)
	} else {
		registry.mu.RLock()
		logger = registry.logger
		registry.mu.RUnlock()
		if logger == nil {
			logger = NewDiscardLogger()
			registry.SetLogger(logger)
		}
	}

	agentReadTimeout := cfg.AgentReadTimeout
	if agentReadTimeout <= 0 {
		agentReadTimeout = defaultAgentReadTimeout
	}
	agentPingInterval := cfg.AgentPingInterval
	if agentPingInterval <= 0 {
		agentPingInterval = defaultAgentPingInterval
	}
	agentPingWriteTimeout := cfg.AgentPingWriteTimeout
	if agentPingWriteTimeout <= 0 {
		agentPingWriteTimeout = defaultAgentPingWriteTimeout
	}
	clientReadTimeout := cfg.ClientReadTimeout
	if clientReadTimeout <= 0 {
		clientReadTimeout = defaultClientReadTimeout
	}
	clientPingInterval := cfg.ClientPingInterval
	if clientPingInterval <= 0 {
		clientPingInterval = defaultClientPingInterval
	}
	clientPingWriteTimeout := cfg.ClientPingWriteTimeout
	if clientPingWriteTimeout <= 0 {
		clientPingWriteTimeout = defaultClientPingWriteTimeout
	}

	mux := http.NewServeMux()
	agentUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	clientUpgrader := websocket.Upgrader{CheckOrigin: checkAttachOrigin}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc(OperatorInviteCodesPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authenticateOperatorRequest(w, r, cfg.OperatorToken, logger) {
			return
		}
		if cfg.Operator == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req OperatorCreateInvitesRequest
		if err := decodeJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		codes, err := cfg.Operator.CreateInviteCodes(r.Context(), req.Count, req.ExpiresInDays)
		if err != nil {
			writeOperatorError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, OperatorCreateInvitesResponse{Codes: codes})
	})

	mux.HandleFunc(OperatorInviteDisablePath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authenticateOperatorRequest(w, r, cfg.OperatorToken, logger) {
			return
		}
		if cfg.Operator == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req OperatorDisableInviteRequest
		if err := decodeJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := cfg.Operator.DisableInviteCode(r.Context(), req.Code); err != nil {
			writeOperatorError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc(OperatorDeleteUserPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authenticateOperatorRequest(w, r, cfg.OperatorToken, logger) {
			return
		}
		if cfg.Operator == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req OperatorDeleteUserRequest
		if err := decodeJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		result, err := cfg.Operator.DeleteUser(r.Context(), req.Username)
		if err != nil {
			writeOperatorError(w, err)
			return
		}
		registry.DisconnectUserSessions(result.UserID, "account_deleted")
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if cfg.AppAuth == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		remoteIP := requestRemoteIP(r)
		if allowed, retryAfter := cfg.RegisterThrottle.Allow(remoteIP); !allowed {
			w.Header().Set("Retry-After", formatRetryAfter(retryAfter))
			writeJSONError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}

		var req registerRequest
		if err := decodeJSONBody(r, &req); err != nil {
			cfg.RegisterThrottle.RecordFailure(remoteIP)
			writeJSONError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		user, err := cfg.AppAuth.Register(r.Context(), req.Username, req.Password, req.InviteCode)
		if err != nil {
			if isRegisterFailure(err) {
				cfg.RegisterThrottle.RecordFailure(remoteIP)
				writeJSONError(w, http.StatusBadRequest, "registration_failed")
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		cfg.RegisterThrottle.Reset(remoteIP)
		writeJSON(w, http.StatusCreated, map[string]any{
			"user_id":  user.ID,
			"username": user.UsernameNorm,
		})
	})

	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if cfg.AppAuth == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req loginRequest
		if err := decodeJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		issued, err := cfg.AppAuth.Login(r.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, ErrInvalidCredentials) {
				writeJSONError(w, http.StatusUnauthorized, "invalid_credentials")
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, newAppSessionResponse(issued))
	})

	mux.HandleFunc("/api/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if cfg.AppAuth == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		var req refreshRequest
		if err := decodeJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		issued, err := cfg.AppAuth.Refresh(r.Context(), req.RefreshToken)
		if err != nil {
			if isRefreshFailure(err) {
				writeJSONError(w, http.StatusUnauthorized, "invalid_session")
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, newAppSessionResponse(issued))
	})

	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		auth, ok := authenticateAppRequest(w, r, cfg.AppAuth, logger)
		if !ok {
			return
		}
		if err := cfg.AppAuth.Logout(r.Context(), auth); err != nil {
			if isRefreshFailure(err) {
				writeJSONError(w, http.StatusUnauthorized, "invalid_session")
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/api/auth/password/change", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		auth, ok := authenticateAppRequest(w, r, cfg.AppAuth, logger)
		if !ok {
			return
		}

		var req changePasswordRequest
		if err := decodeJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request")
			return
		}

		if err := cfg.AppAuth.ChangePassword(r.Context(), auth, req.CurrentPassword, req.NewPassword); err != nil {
			switch {
			case errors.Is(err, ErrInvalidCredentials):
				writeJSONError(w, http.StatusUnauthorized, "invalid_credentials")
			case errors.Is(err, ErrInvalidPassword):
				writeJSONError(w, http.StatusBadRequest, "invalid_request")
			default:
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/api/agent-tokens", func(w http.ResponseWriter, r *http.Request) {
		auth, ok := authenticateAppRequest(w, r, cfg.AppAuth, logger)
		if !ok {
			return
		}
		if cfg.AgentTokens == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		switch r.Method {
		case http.MethodGet:
			tokens, err := cfg.AgentTokens.List(r.Context(), auth.User.ID)
			if err != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			out := make([]agentTokenResponse, 0, len(tokens))
			for _, token := range tokens {
				out = append(out, newAgentTokenResponse(token))
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			var req createAgentTokenRequest
			if err := decodeJSONBody(r, &req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			created, err := cfg.AgentTokens.Create(r.Context(), auth.User.ID, req.Name)
			if err != nil {
				if errors.Is(err, ErrInvalidAgentTokenName) {
					writeJSONError(w, http.StatusBadRequest, "invalid_request")
					return
				}
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusCreated, createdAgentTokenResponse{
				agentTokenResponse: newAgentTokenResponse(created.Record),
				Token:              created.Plaintext,
			})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/agent-tokens/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		auth, ok := authenticateAppRequest(w, r, cfg.AppAuth, logger)
		if !ok {
			return
		}
		if cfg.AgentTokens == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		tokenID := strings.TrimPrefix(r.URL.Path, "/api/agent-tokens/")
		if tokenID == "" || strings.Contains(tokenID, "/") {
			http.NotFound(w, r)
			return
		}
		if err := cfg.AgentTokens.Revoke(r.Context(), auth.User.ID, tokenID, auth.User.UsernameNorm); err != nil {
			if errors.Is(err, ErrAgentTokenNotFound) {
				writeJSONError(w, http.StatusNotFound, "agent_token_not_found")
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		registry.DisconnectAgentTokenSessions(tokenID, "agent_token_revoked")
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		auth, ok := authenticateAppRequest(w, r, cfg.AppAuth, logger)
		if !ok {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, http.StatusOK, registry.ListForUser(auth.User.ID))
	})

	mux.HandleFunc("/agent/ws", func(w http.ResponseWriter, r *http.Request) {
		if cfg.AgentTokens == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		token, ok := bearerTokenFromRequest(r)
		if !ok {
			logAuthFailed(logger, r, "agent_bearer")
			w.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		authenticated, err := cfg.AgentTokens.Authenticate(r.Context(), token)
		if err != nil {
			logAuthFailed(logger, r, "agent_bearer")
			w.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := agentUpgrader.Upgrade(w, r, nil)
		if err != nil {
			logWSUpgradeFailed(logger, r, "agent")
			return
		}
		defer conn.Close()
		tracker := newWSTrafficTracker(r.URL.Path, r.RemoteAddr, requestIDFromRequest(r))
		fields := []Field{
			String("path", r.URL.Path),
			String("remote_addr", r.RemoteAddr),
			Int64("user_id", authenticated.User.ID),
			String("agent_token_id", authenticated.Token.ID),
		}
		if requestID := requestIDFromRequest(r); requestID != "" {
			fields = append(fields, String("request_id", requestID))
		}
		logger.Info("agent_ws_connected", fields...)
		var loopErr error
		defer func() {
			fields := tracker.SummaryFields(time.Now())
			fields = append(fields, disconnectLogFields(tracker.DisconnectError(loopErr))...)
			logger.Info("agent_disconnected", fields...)
		}()
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(agentReadTimeout))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(agentReadTimeout))
		})

		var register protocol.AgentFrame
		payload, err := readWSJSON(conn, &register)
		if err != nil {
			loopErr = err
			return
		}
		tracker.RecordInbound(len(payload))
		if register.Type != "register" || register.Session == nil {
			loopErr = errInvalidAgentRegister
			return
		}

		peer := newWSAgentPeer(conn, tracker)
		registry.RegisterOwned(*register.Session, SessionOwner{
			UserID:       authenticated.User.ID,
			AgentTokenID: authenticated.Token.ID,
		}, peer)
		defer registry.DisconnectIfOwner(register.Session.SessionID, peer)
		tracker.SetSessionID(register.Session.SessionID)
		fields = []Field{
			String("session_id", register.Session.SessionID),
			String("launcher", register.Session.Launcher),
			String("label", register.Session.Label),
			String("cwd", register.Session.CWD),
			Int64("user_id", authenticated.User.ID),
			String("agent_token_id", authenticated.Token.ID),
		}
		if requestID := requestIDFromRequest(r); requestID != "" {
			fields = append(fields, String("request_id", requestID))
		}
		logger.Info("agent_registered", fields...)

		stopPings := startWSPingLoop(conn, agentPingInterval, agentPingWriteTimeout)
		defer close(stopPings)

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				loopErr = err
				return
			}
			tracker.RecordInbound(len(payload))

			switch messageType {
			case websocket.BinaryMessage:
				packet, err := protocol.DecodeAttachPacket(payload)
				if err != nil {
					continue
				}
				registry.RouteTerminalBytesIfOwner(register.Session.SessionID, peer, packet)
			case websocket.TextMessage:
				var frame protocol.AgentFrame
				if err := json.Unmarshal(payload, &frame); err != nil {
					continue
				}
				switch frame.Type {
				case "resize":
					if frame.Cols > 0 && frame.Rows > 0 {
						registry.RouteResizeIfOwner(register.Session.SessionID, peer, frame.Cols, frame.Rows)
					}
				case "attach_ready":
					if frame.ClientID != "" && frame.Cols > 0 && frame.Rows > 0 {
						registry.RouteAttachReadyIfOwner(register.Session.SessionID, peer, frame.ClientID, frame.Cols, frame.Rows)
					}
				case "snapshot_done":
					if frame.ClientID != "" {
						registry.RouteSnapshotDoneIfOwner(register.Session.SessionID, peer, frame.ClientID)
					}
				case "attach_close":
					if frame.ClientID != "" {
						registry.RouteAttachCloseIfOwner(register.Session.SessionID, peer, frame.ClientID, frame.Reason)
					}
				}
			}
		}
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		auth, ok := authenticateAppRequest(w, r, cfg.AppAuth, logger)
		if !ok {
			return
		}

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 5 || parts[0] != "api" || parts[1] != "sessions" || parts[3] != "attach" || parts[4] != "ws" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sessionID := parts[2]
		if _, ok := registry.SessionForUser(sessionID, auth.User.ID); !ok {
			writeJSONError(w, http.StatusNotFound, "session_not_found")
			return
		}

		clientID, err := newClientID()
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		conn, err := clientUpgrader.Upgrade(w, r, nil)
		if err != nil {
			logWSUpgradeFailed(logger, r, "attach")
			return
		}
		defer conn.Close()
		tracker := newWSTrafficTracker(r.URL.Path, r.RemoteAddr, requestIDFromRequest(r))
		tracker.SetSessionID(sessionID)
		fields := []Field{
			String("path", r.URL.Path),
			String("remote_addr", r.RemoteAddr),
			String("session_id", sessionID),
			Int64("user_id", auth.User.ID),
		}
		if requestID := requestIDFromRequest(r); requestID != "" {
			fields = append(fields, String("request_id", requestID))
		}
		logger.Info("attach_ws_connected", fields...)
		var loopErr error
		defer func() {
			fields := tracker.SummaryFields(time.Now())
			fields = append(fields, disconnectLogFields(tracker.DisconnectError(loopErr))...)
			logger.Info("attach_ws_disconnected", fields...)
		}()
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		})

		attachPeer := newWSAttachPeer(conn, tracker, defaultWSSinkBufferSize, defaultWSWriteTimeout)
		owner, err := registry.StartAttachForUser(sessionID, clientID, auth.User.ID, attachPeer)
		if err != nil {
			loopErr = err
			_ = attachPeer.Close(reasonForAttachStartError(err))
			return
		}
		defer registry.DetachClient(sessionID, clientID, "client_closed")

		if err := owner.SendJSON(protocol.AttachOpenFrame(clientID)); err != nil {
			loopErr = err
			_ = registry.DetachClient(sessionID, clientID, "session_offline")
			return
		}

		stopPings := startWSPingLoop(conn, clientPingInterval, clientPingWriteTimeout)
		defer close(stopPings)

		for {
			var msg protocol.ClientInputMessage
			payload, err := readWSJSON(conn, &msg)
			if err != nil {
				loopErr = err
				return
			}
			tracker.RecordInbound(len(payload))
			switch msg.Type {
			case "input_text":
			case "input_key":
				if msg.Key == "" && msg.Type == "input_key" {
					continue
				}
			default:
				continue
			}
			if err := registry.WriteAttachInput(sessionID, msg.AgentFrame(clientID)); err != nil {
				loopErr = err
				return
			}
		}
	})

	return logRequests(logger, mux)
}

func authenticateAppRequest(w http.ResponseWriter, r *http.Request, appAuth *AppAuthService, logger *Logger) (AuthenticatedApp, bool) {
	if appAuth == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return AuthenticatedApp{}, false
	}

	token, ok := bearerTokenFromRequest(r)
	if !ok {
		logAuthFailed(logger, r, "app_bearer")
		w.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return AuthenticatedApp{}, false
	}

	auth, err := appAuth.AuthenticateAccessToken(r.Context(), token)
	if err != nil {
		logAuthFailed(logger, r, "app_bearer")
		w.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return AuthenticatedApp{}, false
	}
	return auth, true
}

func authenticateOperatorRequest(w http.ResponseWriter, r *http.Request, operatorToken string, logger *Logger) bool {
	if strings.TrimSpace(operatorToken) == "" {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return false
	}
	if !isLoopbackRequest(r) || hasForwardedProxyHeaders(r) {
		logAuthFailed(logger, r, "operator_local")
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if !staticBearerAuth(r, operatorToken) {
		logAuthFailed(logger, r, "operator_bearer")
		w.Header().Set("WWW-Authenticate", `Bearer realm="tunnel relay"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func decodeJSONBody(r *http.Request, dest any) error {
	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, map[string]string{"reason": reason})
}

func writeOperatorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidOperatorRequest),
		errors.Is(err, ErrInvalidInviteCode),
		errors.Is(err, ErrInvalidUsername):
		writeJSONError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, ErrInviteCodeNotFound):
		writeJSONError(w, http.StatusNotFound, "invite_code_not_found")
	case errors.Is(err, ErrInviteCodeDisabled):
		writeJSONError(w, http.StatusConflict, "invite_code_disabled")
	case errors.Is(err, ErrInviteCodeConsumed):
		writeJSONError(w, http.StatusConflict, "invite_code_consumed")
	case errors.Is(err, ErrInviteCodeExpired):
		writeJSONError(w, http.StatusConflict, "invite_code_expired")
	case errors.Is(err, ErrUserNotFound):
		writeJSONError(w, http.StatusNotFound, "user_not_found")
	default:
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func isRegisterFailure(err error) bool {
	return errors.Is(err, ErrInvalidUsername) ||
		errors.Is(err, ErrInvalidPassword) ||
		errors.Is(err, ErrInvalidInviteCode) ||
		errors.Is(err, ErrInviteCodeNotFound) ||
		errors.Is(err, ErrInviteCodeExpired) ||
		errors.Is(err, ErrInviteCodeDisabled) ||
		errors.Is(err, ErrInviteCodeConsumed) ||
		errors.Is(err, ErrUsernameTaken)
}

func isRefreshFailure(err error) bool {
	return errors.Is(err, ErrAppSessionNotFound) ||
		errors.Is(err, ErrAppSessionExpired) ||
		errors.Is(err, ErrAppSessionRevoked)
}

func formatRetryAfter(delay time.Duration) string {
	if delay <= 0 {
		return "0"
	}
	seconds := int(delay.Round(time.Second) / time.Second)
	if seconds <= 0 {
		return "1"
	}
	return strconv.Itoa(seconds)
}

func newAppSessionResponse(issued IssuedAppSession) appSessionResponse {
	return appSessionResponse{
		AccessToken:  issued.AccessToken,
		RefreshToken: issued.RefreshToken,
		ExpiresIn:    int64(DefaultAccessTokenTTL / time.Second),
		TokenType:    "Bearer",
	}
}

func newAgentTokenResponse(record AgentTokenRecord) agentTokenResponse {
	return agentTokenResponse{
		ID:         record.ID,
		Name:       record.Name,
		CreatedAt:  record.CreatedAt,
		LastUsedAt: record.LastUsedAt,
		RevokedAt:  record.RevokedAt,
	}
}

func startWSPingLoop(conn *websocket.Conn, interval, writeTimeout time.Duration) chan struct{} {
	stop := make(chan struct{})
	if conn == nil || interval <= 0 {
		return stop
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				deadline := time.Now().Add(writeTimeout)
				if writeTimeout <= 0 {
					deadline = time.Time{}
				}
				if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					return
				}
			}
		}
	}()

	return stop
}

func checkAttachOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return false
	}
	return sameAttachOrigin(parsed.Scheme, parsed.Host, attachRequestScheme(r), attachRequestHost(r))
}

func attachRequestHost(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.TrimSpace(forwarded)
	}
	return strings.TrimSpace(r.Host)
}

func attachRequestScheme(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.ToLower(strings.TrimSpace(forwarded))
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func sameAttachOrigin(originScheme, originHost, requestScheme, requestHost string) bool {
	if !strings.EqualFold(strings.TrimSpace(originScheme), strings.TrimSpace(requestScheme)) {
		return false
	}
	return strings.EqualFold(normalizeAttachHost(originHost, originScheme), normalizeAttachHost(requestHost, requestScheme))
}

func normalizeAttachHost(host, scheme string) string {
	host = strings.TrimSpace(host)
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	parsed := &url.URL{Scheme: scheme, Host: host}
	name := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" || isDefaultPortForScheme(port, scheme) {
		return name
	}
	return net.JoinHostPort(name, port)
}

func isDefaultPortForScheme(port, scheme string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

func reasonForAttachStartError(err error) string {
	switch {
	case errors.Is(err, ErrSessionNotFound):
		return "session_offline"
	case errors.Is(err, ErrSessionOffline):
		return "session_offline"
	default:
		return "session_offline"
	}
}

func newClientID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	var raw [36]byte
	hex.Encode(raw[0:8], buf[0:4])
	raw[8] = '-'
	hex.Encode(raw[9:13], buf[4:6])
	raw[13] = '-'
	hex.Encode(raw[14:18], buf[6:8])
	raw[18] = '-'
	hex.Encode(raw[19:23], buf[8:10])
	raw[23] = '-'
	hex.Encode(raw[24:36], buf[10:16])
	return string(raw[:]), nil
}

func logAuthFailed(logger *Logger, r *http.Request, authType string) {
	fields := []Field{
		String("path", r.URL.Path),
		String("auth_type", authType),
	}
	fields = append(fields, requestLogFields(r)...)
	logger.Warn("auth_failed", fields...)
}

func logWSUpgradeFailed(logger *Logger, r *http.Request, role string) {
	fields := []Field{
		String("path", r.URL.Path),
		String("role", role),
	}
	fields = append(fields, requestLogFields(r)...)
	logger.Warn("ws_upgrade_failed", fields...)
}

func disconnectLogFields(err error) []Field {
	if errors.Is(err, errWSSinkBackpressure) {
		return []Field{String("reason", "backpressure")}
	}
	if err == nil {
		return []Field{String("reason", "client_closed")}
	}
	fields := make([]Field, 0, 4)
	reason := "read_error"
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		reason = "client_closed"
	}
	fields = append(fields, String("reason", reason))

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		fields = append(fields, Int("close_code", closeErr.Code))
		if closeErr.Text != "" {
			fields = append(fields, String("close_text", closeErr.Text))
		}
	}

	return fields
}
