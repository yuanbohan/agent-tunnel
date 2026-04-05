package relay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/session"
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
	errWSSinkClosed       = errors.New("websocket sink closed")
	errWSSinkBackpressure = errors.New("websocket sink backpressure")
	nextSinkID            uint64
	clientSinkFactory     = func(conn *websocket.Conn, onBackpressure func()) clientSink {
		return newWSSinkWithConfig(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout, onBackpressure)
	}
)

type HandlerConfig struct {
	Registry               *Registry
	BrowserUser            string
	BrowserPassword        string
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
	conn *websocket.Conn
	mu   sync.Mutex
}

type wsConn interface {
	WriteJSON(v any) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

type clientSink interface {
	session.OutputSink
	Close() error
	PreloadMessages([]protocol.Message) error
}

type wsSink struct {
	conn           wsConn
	writeTimeout   time.Duration
	onBackpressure func()

	mu               sync.RWMutex
	closed           bool
	disconnectReason string
	pending          []protocol.Message
	outbound         chan protocol.Message
	closeOnce        sync.Once
	backpressureOnce sync.Once
}

func newWSSink(conn wsConn) *wsSink {
	return newWSSinkWithConfig(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout, nil)
}

func newWSSinkWithConfig(conn wsConn, bufferSize int, writeTimeout time.Duration, onBackpressure func()) *wsSink {
	if bufferSize <= 0 {
		bufferSize = 1
	}

	sink := &wsSink{
		conn:           conn,
		writeTimeout:   writeTimeout,
		onBackpressure: onBackpressure,
		outbound:       make(chan protocol.Message, bufferSize),
	}
	go sink.run()
	return sink
}

func (s *wsSink) WriteOutput(data []byte) error {
	return s.WriteOutputFrame(0, data, 0, 0)
}

func (s *wsSink) WriteOutputFrame(seq uint64, data []byte, cols, rows int) error {
	msg := protocol.EncodeOutputWithSeqAndSize(seq, append([]byte(nil), data...), cols, rows)
	return s.enqueue(msg)
}

func (s *wsSink) WriteResize(cols, rows int) error {
	msg := protocol.Message{Type: "resize", Cols: cols, Rows: rows}
	return s.enqueue(msg)
}

func (s *wsSink) PreloadMessages(msgs []protocol.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errWSSinkClosed
	}
	s.pending = append(s.pending, msgs...)
	return nil
}

func (s *wsSink) enqueue(msg protocol.Message) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errWSSinkClosed
	}

	select {
	case s.outbound <- msg:
		s.mu.RUnlock()
		return nil
	default:
		s.mu.RUnlock()
		s.backpressureOnce.Do(func() {
			if s.onBackpressure != nil {
				s.onBackpressure()
			}
		})
		_ = s.Close()
		return errWSSinkBackpressure
	}
}

func (s *wsSink) Close() error {
	return s.CloseWithReason("")
}

func (s *wsSink) CloseWithReason(reason string) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		if reason != "" && s.disconnectReason == "" {
			s.disconnectReason = reason
		}
		s.closed = true
		close(s.outbound)
		s.mu.Unlock()
		_ = s.conn.Close()
	})
	return nil
}

func (s *wsSink) DisconnectReason() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disconnectReason
}

func (s *wsSink) run() {
	defer s.Close()

	for {
		s.mu.Lock()
		if len(s.pending) > 0 {
			msg := s.pending[0]
			s.pending = s.pending[1:]
			s.mu.Unlock()
			if s.writeTimeout > 0 {
				if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
					return
				}
			}
			if err := s.conn.WriteJSON(msg); err != nil {
				return
			}
			continue
		}
		s.mu.Unlock()

		msg, ok := <-s.outbound
		if !ok {
			return
		}
		if s.writeTimeout > 0 {
			if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
				return
			}
		}
		if err := s.conn.WriteJSON(msg); err != nil {
			return
		}
	}
}

func newWSAgentPeer(conn *websocket.Conn) *wsAgentPeer {
	return &wsAgentPeer{conn: conn}
}

func (p *wsAgentPeer) SendInput(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.conn.WriteJSON(protocol.Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString(data),
	})
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

	mux := http.NewServeMux()
	agentUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	browserUpgrader := websocket.Upgrader{CheckOrigin: sameOriginOrEmpty}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
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

	mux.HandleFunc("/api/session-events/ws", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
			logAuthFailed(logger, r, "basic")
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := browserUpgrader.Upgrade(w, r, nil)
		if err != nil {
			logWSUpgradeFailed(logger, r, "client")
			return
		}
		defer conn.Close()
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		})

		sinkID := "session-events-" + strconv.FormatUint(atomic.AddUint64(&nextSinkID, 1), 10)
		sink := newWSSessionStateSink(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout)
		registry.AddStateSink(sinkID, sink)
		defer func() {
			registry.RemoveStateSink(sinkID)
			_ = sink.Close()
		}()

		stopPings := startWSPingLoop(conn, clientPingInterval, clientPingWriteTimeout)
		defer close(stopPings)

		for {
			var discard json.RawMessage
			if err := conn.ReadJSON(&discard); err != nil {
				return
			}
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
		logger.Info("agent_ws_connected",
			String("path", r.URL.Path),
			String("remote_addr", r.RemoteAddr),
		)
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(agentReadTimeout))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(agentReadTimeout))
		})

		var register protocol.AgentFrame
		if err := conn.ReadJSON(&register); err != nil || register.Type != "register" || register.Session == nil {
			return
		}

		peer := newWSAgentPeer(conn)
		registry.Register(*register.Session, peer)
		defer registry.RemoveIfOwner(register.Session.SessionID, peer)
		logger.Info("agent_registered",
			String("session_id", register.Session.SessionID),
			String("launcher", register.Session.Launcher),
			String("label", register.Session.Label),
			String("cwd", register.Session.CWD),
		)
		connectedAt := time.Now()
		var loopErr error
		defer func() {
			fields := []Field{
				String("session_id", register.Session.SessionID),
				Int64("duration_ms", time.Since(connectedAt).Milliseconds()),
			}
			fields = append(fields, disconnectLogFields(loopErr)...)
			logger.Info("agent_disconnected", fields...)
		}()

		stopPings := startWSPingLoop(conn, agentPingInterval, agentPingWriteTimeout)
		defer close(stopPings)

		for {
			var msg protocol.Message
			if err := conn.ReadJSON(&msg); err != nil {
				loopErr = err
				return
			}
			switch msg.Type {
			case "output":
				data, err := protocol.DecodeData(msg)
				if err != nil {
					continue
				}
				registry.TouchOutputIfOwner(register.Session.SessionID, peer, data, time.Now().UTC())
			case "resize":
				registry.UpdateSizeIfOwner(register.Session.SessionID, peer, msg.Cols, msg.Rows)
			case "session_state":
				changedAt := time.Now().UTC()
				if msg.ChangedAt != nil {
					changedAt = msg.ChangedAt.UTC()
				}
				registry.UpdateSessionStateIfOwner(register.Session.SessionID, peer, msg.State, changedAt, msg.ActionRequiredSince)
			}
		}
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
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
		case "history":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if !registry.HasSession(sessionID) {
				http.NotFound(w, r)
				return
			}
			after, err := parseOptionalUintQuery(r, "after")
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			page, ok := registry.History(sessionID, after)
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(page)
			return
		case "read":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if !registry.HasSession(sessionID) {
				http.NotFound(w, r)
				return
			}
			var req struct {
				Seq uint64 `json:"seq"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			info, ok := registry.MarkRead(sessionID, req.Seq)
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(info)
			return
		case "ws":
			// continue below
		default:
			http.NotFound(w, r)
			return
		}

		if !registry.HasSession(sessionID) {
			http.NotFound(w, r)
			return
		}

		conn, err := browserUpgrader.Upgrade(w, r, nil)
		if err != nil {
			logWSUpgradeFailed(logger, r, "client")
			return
		}
		defer conn.Close()
		conn.SetReadLimit(1 << 20)
		_ = conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(clientReadTimeout))
		})

		after, err := parseOptionalUintQuery(r, "after")
		if err != nil {
			_ = conn.Close()
			return
		}

		sinkID := "browser-" + strconv.FormatUint(atomic.AddUint64(&nextSinkID, 1), 10)
		var sinkBackpressure atomic.Bool
		sink := clientSinkFactory(conn, func() {
			if !sinkBackpressure.CompareAndSwap(false, true) {
				return
			}
			logger.Warn("sink_backpressure",
				String("session_id", sessionID),
				String("client_id", sinkID),
			)
			_ = conn.Close()
		})
		if err := registry.AttachSink(sessionID, sinkID, sink, after); err != nil {
			_ = sink.Close()
			return
		}
		logger.Info("client_ws_connected",
			String("session_id", sessionID),
			String("client_id", sinkID),
			String("remote_addr", r.RemoteAddr),
			String("user_agent", r.UserAgent()),
		)
		connectedAt := time.Now()
		var loopErr error
		defer func() {
			registry.RemoveSink(sessionID, sinkID)
			_ = sink.Close()
			fields := disconnectLogFields(loopErr)
			if sinkBackpressure.Load() {
				fields = []Field{String("reason", "backpressure")}
			} else if aware, ok := sink.(disconnectAwareSink); ok {
				if reason := aware.DisconnectReason(); reason != "" {
					fields = []Field{String("reason", reason)}
				}
			}
			logFields := []Field{
				String("session_id", sessionID),
				String("client_id", sinkID),
				Int64("duration_ms", time.Since(connectedAt).Milliseconds()),
			}
			logFields = append(logFields, fields...)
			logger.Info("client_disconnected", logFields...)
		}()

		stopPings := startWSPingLoop(conn, clientPingInterval, clientPingWriteTimeout)
		defer close(stopPings)

		for {
			var msg protocol.Message
			if err := conn.ReadJSON(&msg); err != nil {
				loopErr = err
				return
			}

			if msg.Type == "input" {
				data, err := protocol.DecodeData(msg)
				if err == nil {
					_ = registry.WriteInput(sessionID, data)
				}
			}
		}
	})

	return mux
}

func logAuthFailed(logger *Logger, r *http.Request, authType string) {
	logger.Warn("auth_failed",
		String("path", r.URL.Path),
		String("remote_addr", r.RemoteAddr),
		String("auth_type", authType),
	)
}

func logWSUpgradeFailed(logger *Logger, r *http.Request, role string) {
	logger.Warn("ws_upgrade_failed",
		String("path", r.URL.Path),
		String("remote_addr", r.RemoteAddr),
		String("role", role),
	)
}

func classifyDisconnectReason(err error) string {
	return disconnectLogFields(err)[0].Value.String()
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

func sameOriginOrEmpty(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return originURL.Host == r.Host
}

func parseOptionalUintQuery(r *http.Request, key string) (uint64, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}
