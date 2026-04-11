package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
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
	User                   string
	Password               string
	AgentToken             string
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

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.User, cfg.Password) {
			logAuthFailed(logger, r, "basic")
			w.Header().Set("WWW-Authenticate", `Basic realm="tunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(registry.List())
	})

	mux.HandleFunc("/agent/ws", func(w http.ResponseWriter, r *http.Request) {
		if !checkBearer(r, cfg.AgentToken) {
			logAuthFailed(logger, r, "bearer")
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
		registry.Register(*register.Session, peer)
		defer registry.DisconnectIfOwner(register.Session.SessionID, peer)
		tracker.SetSessionID(register.Session.SessionID)
		fields = []Field{
			String("session_id", register.Session.SessionID),
			String("launcher", register.Session.Launcher),
			String("label", register.Session.Label),
			String("cwd", register.Session.CWD),
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
		if !checkBasicAuth(r, cfg.User, cfg.Password) {
			logAuthFailed(logger, r, "basic")
			w.Header().Set("WWW-Authenticate", `Basic realm="tunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
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
		if _, ok := registry.Session(sessionID); !ok {
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
		owner, err := registry.StartAttach(sessionID, clientID, attachPeer)
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

func writeJSONError(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"reason": reason})
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
	fields = append(fields, String("error", err.Error()))
	return fields
}

func startWSPingLoop(conn *websocket.Conn, interval, writeTimeout time.Duration) chan struct{} {
	stop := make(chan struct{})
	if interval <= 0 {
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
				_ = conn.WriteControl(websocket.PingMessage, nil, deadline)
			}
		}
	}()

	return stop
}
