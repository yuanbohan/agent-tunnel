package relay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"path"
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
	outbound  chan []byte
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
		outbound:     make(chan []byte, bufferSize),
	}
	go sink.run()
	return sink
}

func (s *wsSink) WriteOutput(data []byte) error {
	cp := append([]byte(nil), data...)

	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errWSSinkClosed
	}

	select {
	case s.outbound <- cp:
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

	for data := range s.outbound {
		if s.writeTimeout > 0 {
			if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
				return
			}
		}
		if err := s.conn.WriteJSON(protocol.EncodeOutput(data)); err != nil {
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

func (p *wsAgentPeer) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn.WriteJSON(protocol.Message{Type: "resize", Cols: cols, Rows: rows})
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
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := agentUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
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
				return
			}
			if msg.Type != "output" {
				continue
			}

			data, err := protocol.DecodeData(msg)
			if err != nil {
				continue
			}
			registry.TouchOutputIfOwner(register.Session.SessionID, peer, data, time.Now().UTC())
		}
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBasicAuth(r, cfg.BrowserUser, cfg.BrowserPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="agentunnel relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if path.Base(r.URL.Path) != "ws" {
			http.NotFound(w, r)
			return
		}

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "sessions" || parts[3] != "ws" {
			http.NotFound(w, r)
			return
		}
		sessionID := parts[2]
		if !registry.HasSession(sessionID) {
			http.NotFound(w, r)
			return
		}

		conn, err := browserUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		sinkID := "browser-" + strconv.FormatUint(atomic.AddUint64(&nextSinkID, 1), 10)
		sink := newWSSink(conn)
		if err := registry.AddSink(sessionID, sinkID, sink); err != nil {
			_ = sink.Close()
			return
		}
		defer func() {
			registry.RemoveSink(sessionID, sinkID)
			_ = sink.Close()
		}()

		for {
			var msg protocol.Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}

			switch msg.Type {
			case "input":
				data, err := protocol.DecodeData(msg)
				if err == nil {
					_ = registry.WriteInput(sessionID, data)
				}
			case "resize":
				_ = registry.Resize(sessionID, msg.Cols, msg.Rows)
			}
		}
	})

	return mux
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
