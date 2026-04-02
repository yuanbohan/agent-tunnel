package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/session"
	"yuanbohan/tunnel/internal/webui"
)

type LiveSession interface {
	AddSink(string, session.OutputSink)
	RemoveSink(string)
	WriteInput([]byte) error
	CurrentSize() (int, int)
	OnResize(func(int, int))
}

type Running struct {
	URL      string
	server   *http.Server
	listener net.Listener
}

const (
	defaultWSSinkBufferSize = 64
	defaultWSWriteTimeout   = 5 * time.Second
)

var (
	errWSSinkClosed       = errors.New("websocket sink closed")
	errWSSinkBackpressure = errors.New("websocket sink backpressure")
)

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

var upgrader = websocket.Upgrader{
	CheckOrigin: sameOriginLoopbackOnly,
}

var nextSinkID uint64

func sameOriginLoopbackOnly(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if originURL.Host != r.Host {
		return false
	}

	host := originURL.Hostname()
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func NewHandler(sess LiveSession) http.Handler {
	fileServer := http.FileServer(http.FS(webui.Files()))

	var wsMu sync.Mutex
	wsConns := make(map[string]*websocket.Conn)

	sess.OnResize(func(cols, rows int) {
		msg := protocol.Message{Type: "resize", Cols: cols, Rows: rows}
		wsMu.Lock()
		conns := make([]*websocket.Conn, 0, len(wsConns))
		for _, c := range wsConns {
			conns = append(conns, c)
		}
		wsMu.Unlock()

		for _, c := range conns {
			_ = c.WriteJSON(msg)
		}
	})

	mux := http.NewServeMux()
	mux.Handle("/", fileServer)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		sinkID := fmt.Sprintf("ws-%d", atomic.AddUint64(&nextSinkID, 1))
		sink := newWSSink(conn)
		sess.AddSink(sinkID, sink)

		wsMu.Lock()
		wsConns[sinkID] = conn
		wsMu.Unlock()

		defer func() {
			sess.RemoveSink(sinkID)
			wsMu.Lock()
			delete(wsConns, sinkID)
			wsMu.Unlock()
			_ = sink.Close()
		}()

		// Send current PTY size to browser on connect
		cols, rows := sess.CurrentSize()
		if cols > 0 && rows > 0 {
			_ = conn.WriteJSON(protocol.Message{
				Type: "resize",
				Cols: cols,
				Rows: rows,
			})
		}

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}

			switch msg.Type {
			case "input":
				data, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					continue
				}
				_ = sess.WriteInput(data)
			}
		}
	})
	return mux
}

func StartLocal(sess LiveSession) (*Running, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		Handler: NewHandler(sess),
	}
	go func() {
		_ = srv.Serve(listener)
	}()

	return &Running{
		URL:      "http://" + listener.Addr().String(),
		server:   srv,
		listener: listener,
	}, nil
}

func (r *Running) Close(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}
