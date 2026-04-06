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
	nextSinkID              uint64
	clientUpdateSinkFactory = func(conn *websocket.Conn) clientUpdateSink {
		return newWSClientUpdateSink(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout)
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

	mux.HandleFunc("/api/updates/ws", func(w http.ResponseWriter, r *http.Request) {
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

		sinkID := "updates-" + strconv.FormatUint(atomic.AddUint64(&nextSinkID, 1), 10)
		sink := clientUpdateSinkFactory(conn)
		registry.AddUpdateSink(sinkID, sink)
		defer func() {
			registry.RemoveUpdateSink(sinkID)
			_ = sink.Close()
		}()

		stopPings := startWSPingLoop(conn, clientPingInterval, clientPingWriteTimeout)
		defer close(stopPings)

		for {
			var msg protocol.ClientUpdateMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			if msg.Type != "input" || msg.SessionID == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(msg.Data)
			if err != nil {
				continue
			}
			_ = registry.WriteInput(msg.SessionID, data)
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
		// The relay treats the agent websocket as the owner of this live session.
		// All later output/resize mutations are validated against this
		// owner so stale connections cannot keep mutating a replaced session.
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
				// Output flows onto the global multiplexed client stream under
				// /api/updates/ws. The relay tracks seq/unread metadata for replay, but
				// it does not interpret terminal content.
				registry.TouchOutputIfOwner(register.Session.SessionID, peer, data, time.Now().UTC())
			case "resize":
				registry.UpdateSizeIfOwner(register.Session.SessionID, peer, msg.Cols, msg.Rows)
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
		default:
			http.NotFound(w, r)
			return
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
