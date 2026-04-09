package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
)

const (
	defaultWSSinkBufferSize       = 64
	defaultWSWriteTimeout         = 5 * time.Second
	defaultHistoryRequestTimeout  = 5 * time.Second
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
	errHistoryResponseProto = errors.New("invalid history response frame")
	nextSinkID              uint64
	nextHistoryRequestID    uint64
	clientUpdateSinkFactory = func(conn *websocket.Conn, tracker *wsTrafficTracker) clientUpdateSink {
		return newWSClientUpdateSink(conn, tracker, defaultWSSinkBufferSize, defaultWSWriteTimeout)
	}
)

type HandlerConfig struct {
	Registry               *Registry
	User                   string
	Password               string
	AgentToken             string
	Logger                 *Logger
	HistoryRequestTimeout  time.Duration
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
}

type historyProxyErrorBody struct {
	Reason string `json:"reason"`
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
	}
}

func (p *wsAgentPeer) Send(msg protocol.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

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
	return p.conn.Close()
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
	historyRequestTimeout := cfg.HistoryRequestTimeout
	if historyRequestTimeout <= 0 {
		historyRequestTimeout = defaultHistoryRequestTimeout
	}

	mux := http.NewServeMux()
	agentUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	clientUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.User, cfg.Password) {
			logAuthFailed(logger, r, "basic")
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
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

	mux.HandleFunc("/api/updates/ws", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.User, cfg.Password) {
			logAuthFailed(logger, r, "basic")
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := clientUpgrader.Upgrade(w, r, nil)
		if err != nil {
			logWSUpgradeFailed(logger, r, "client")
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
		logger.Info("updates_ws_connected", fields...)
		var loopErr error
		defer func() {
			fields := tracker.SummaryFields(time.Now())
			fields = append(fields, disconnectLogFields(tracker.DisconnectError(loopErr))...)
			logger.Info("updates_ws_disconnected", fields...)
		}()
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		})

		sinkID := "updates-" + strconv.FormatUint(atomic.AddUint64(&nextSinkID, 1), 10)
		sink := clientUpdateSinkFactory(conn, tracker)
		registry.AddUpdateSink(sinkID, sink)
		defer func() {
			registry.RemoveUpdateSink(sinkID)
			_ = sink.Close()
		}()

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
			if msg.SessionID == "" {
				continue
			}
			switch msg.Type {
			case "input_text":
			case "input_key":
				if msg.Key == "" {
					continue
				}
			default:
				continue
			}
			_ = registry.WriteInput(msg.SessionID, msg.AgentMessage())
		}
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
		// The relay treats the agent websocket as the owner of this live session.
		// All later output mutations are validated against this
		// owner so stale connections cannot keep mutating a replaced session.
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
			var msg protocol.Message
			payload, err := readWSJSON(conn, &msg)
			if err != nil {
				loopErr = err
				return
			}
			tracker.RecordInbound(len(payload))
			switch msg.Type {
			case "output":
				frame, ok := protocol.ReplayFrameFromOutputMessage(msg)
				if !ok {
					continue
				}
				registry.TouchOutputIfOwner(register.Session.SessionID, peer, frame)
			case "history_response":
				if msg.RequestID == "" {
					if registry.PendingHistoryCountIfOwner(register.Session.SessionID, peer) == 1 {
						registry.FailPendingHistoryRequestsIfOwner(register.Session.SessionID, peer, errHistoryResponseProto)
					}
					continue
				}
				if !validHistoryResponse(msg) {
					registry.FailHistoryRequest(register.Session.SessionID, msg.RequestID, errHistoryResponseProto)
					continue
				}
				if !registry.ResolveHistoryRequest(register.Session.SessionID, peer, msg.RequestID, msg.Frames) &&
					registry.PendingHistoryCountIfOwner(register.Session.SessionID, peer) == 1 {
					registry.FailPendingHistoryRequestsIfOwner(register.Session.SessionID, peer, errHistoryResponseProto)
				}
			}
		}
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.User, cfg.Password) {
			logAuthFailed(logger, r, "basic")
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "sessions" {
			http.NotFound(w, r)
			return
		}

		sessionID := parts[2]
		switch parts[3] {
		case "frames":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			from, hasFrom, err := parseOptionalUintQuery(r, "from")
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			to, hasTo, err := parseOptionalUintQuery(r, "to")
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if hasFrom && hasTo && from > to {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			requestID := "history-" + strconv.FormatUint(atomic.AddUint64(&nextHistoryRequestID, 1), 10)
			peer, resultCh, err := registry.StartHistoryRequest(sessionID, requestID)
			if err != nil {
				writeHistoryProxyError(w, err)
				return
			}
			if err := peer.Send(protocol.EncodeHistoryRequest(requestID, optionalUint64Ptr(from, hasFrom), optionalUint64Ptr(to, hasTo))); err != nil {
				_ = registry.FailHistoryRequest(sessionID, requestID, ErrSessionReconnecting)
				writeHistoryProxyError(w, ErrSessionReconnecting)
				return
			}

			timer := time.NewTimer(historyRequestTimeout)
			defer timer.Stop()

			select {
			case <-r.Context().Done():
				_ = registry.FailHistoryRequest(sessionID, requestID, r.Context().Err())
				return
			case result, ok := <-resultCh:
				if !ok {
					writeHistoryProxyError(w, errHistoryResponseProto)
					return
				}
				if result.err != nil {
					writeHistoryProxyError(w, result.err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(result.frames)
				return
			case <-timer.C:
				_ = registry.FailHistoryRequest(sessionID, requestID, context.DeadlineExceeded)
				writeHistoryProxyError(w, context.DeadlineExceeded)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
	})

	return logRequests(logger, mux)
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

func optionalUint64Ptr(v uint64, ok bool) *uint64 {
	if !ok {
		return nil
	}
	cp := v
	return &cp
}

func validHistoryResponse(msg protocol.Message) bool {
	if msg.Type != "history_response" || msg.RequestID == "" {
		return false
	}
	for _, frame := range msg.Frames {
		if frame.Seq == 0 || frame.DataB64 == "" || frame.TS.IsZero() {
			return false
		}
		if _, err := base64.StdEncoding.DecodeString(frame.DataB64); err != nil {
			return false
		}
	}
	return true
}

func writeHistoryProxyError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	reason := "invalid_agent_response"

	switch {
	case errors.Is(err, ErrSessionNotFound):
		status = http.StatusNotFound
		reason = "session_not_found"
	case errors.Is(err, ErrSessionReconnecting):
		status = http.StatusConflict
		reason = "session_reconnecting"
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		reason = "upstream_timeout"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(historyProxyErrorBody{Reason: reason})
}

func startWSPingLoop(conn *websocket.Conn, interval, writeTimeout time.Duration) chan struct{} {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(writeTimeout)); err != nil {
					return
				}
			}
		}
	}()
	return stop
}

func parseOptionalUintQuery(r *http.Request, key string) (uint64, bool, error) {
	if !r.URL.Query().Has(key) {
		return 0, false, nil
	}
	value := r.URL.Query().Get(key)
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, err
	}
	return parsed, true, nil
}
