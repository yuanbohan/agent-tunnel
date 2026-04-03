package relay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
	"yuanbohan/tunnel/webui"
)

const (
	defaultWSSinkBufferSize      = 64
	defaultWSWriteTimeout        = 5 * time.Second
	defaultAgentReadTimeout      = 30 * time.Second
	defaultAgentPingInterval     = 10 * time.Second
	defaultAgentPingWriteTimeout = 5 * time.Second
)

var (
	errWSSinkClosed       = errors.New("websocket sink closed")
	errWSSinkBackpressure = errors.New("websocket sink backpressure")
	nextSinkID            uint64
)

type HandlerConfig struct {
	Registry              *Registry
	BrowserUser           string
	BrowserPassword       string
	AgentToken            string
	Logger                *Logger
	Files                 fs.FS
	AgentReadTimeout      time.Duration
	AgentPingInterval     time.Duration
	AgentPingWriteTimeout time.Duration
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

type wsSink struct {
	conn         wsConn
	writeTimeout time.Duration

	mu        sync.RWMutex
	closed    bool
	outbound  chan protocol.Message
	closeOnce sync.Once
}

func newWSSink(conn wsConn) *wsSink {
	return newWSSinkWithConfig(conn, defaultWSSinkBufferSize, defaultWSWriteTimeout)
}

func newWSSinkWithConfig(conn wsConn, bufferSize int, writeTimeout time.Duration) *wsSink {
	if bufferSize <= 0 {
		bufferSize = 1
	}

	sink := &wsSink{
		conn:         conn,
		writeTimeout: writeTimeout,
		outbound:     make(chan protocol.Message, bufferSize),
	}
	go sink.run()
	return sink
}

func (s *wsSink) WriteOutput(data []byte) error {
	return s.WriteOutputFrame(0, data)
}

func (s *wsSink) WriteOutputFrame(seq uint64, data []byte) error {
	msg := protocol.EncodeOutputWithSeq(seq, append([]byte(nil), data...))
	return s.enqueue(msg)
}

func (s *wsSink) WriteResize(cols, rows int) error {
	msg := protocol.Message{Type: "resize", Cols: cols, Rows: rows}
	return s.enqueue(msg)
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
		_ = s.Close()
		return errWSSinkBackpressure
	}
}

func (s *wsSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.outbound)
		s.mu.Unlock()
		_ = s.conn.Close()
	})
	return nil
}

func (s *wsSink) run() {
	defer s.Close()

	for msg := range s.outbound {
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
	if logger == nil {
		logger = NewDiscardLogger()
	}
	registry.SetLogger(logger)

	files := cfg.Files
	if files == nil {
		files = webui.Files()
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

	mux := http.NewServeMux()
	fileServer := http.FileServer(http.FS(files))
	agentUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	browserUpgrader := websocket.Upgrader{CheckOrigin: sameOriginOrEmpty}

	serveRelayShell := func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
			logAuthFailed(logger, r, "basic")
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		serveRelayShellAsset(w, r, files)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			serveRelayShell(w, r)
		case strings.HasPrefix(r.URL.Path, "/sessions/"):
			serveRelayShell(w, r)
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			fileServer.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})

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
			logger.Info("agent_disconnected",
				String("session_id", register.Session.SessionID),
				Int64("duration_ms", time.Since(connectedAt).Milliseconds()),
				String("reason", classifyDisconnectReason(loopErr)),
			)
		}()

		stopPings := make(chan struct{})
		defer close(stopPings)
		go func() {
			ticker := time.NewTicker(agentPingInterval)
			defer ticker.Stop()

			for {
				select {
				case <-stopPings:
					return
				case <-ticker.C:
					if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(agentPingWriteTimeout)); err != nil {
						return
					}
				}
			}
		}()

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
				registry.BroadcastResize(register.Session.SessionID, msg.Cols, msg.Rows)
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
			before, err := parseOptionalUintQuery(r, "before")
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			after, err := parseOptionalUintQuery(r, "after")
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			limit, err := parseOptionalIntQuery(r, "limit")
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			maxBytes, err := parseOptionalIntQuery(r, "max_bytes")
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			page, ok := registry.History(sessionID, before, after, limit, maxBytes)
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

		sinkID := "browser-" + strconv.FormatUint(atomic.AddUint64(&nextSinkID, 1), 10)
		sink := newWSSink(conn)
		if err := registry.AddSink(sessionID, sinkID, sink); err != nil {
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
			logger.Info("client_disconnected",
				String("session_id", sessionID),
				String("client_id", sinkID),
				Int64("duration_ms", time.Since(connectedAt).Milliseconds()),
				String("reason", classifyDisconnectReason(loopErr)),
			)
		}()

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
	if err == nil {
		return "closed"
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}

	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return "closed"
	}

	return "error"
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

func parseOptionalIntQuery(r *http.Request, key string) (int, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

func serveRelayShellAsset(w http.ResponseWriter, r *http.Request, files fs.FS) {
	if _, err := fs.Stat(files, "index.html"); err == nil {
		http.ServeFileFS(w, r, files, "index.html")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(relayFallbackHTML))
}

const relayFallbackHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Agentunnel Relay</title>
</head>
<body>
<main id="relay-root">relay-root</main>
</body>
</html>
`
