package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	tunnelsession "yuanbohan/tunnel/internal/tunnel/session"
)

const (
	brokerFrameRegisterSession = "register_session"
	brokerFrameSessionUpdate   = "session_update"
	brokerFramePreviewUpdate   = "preview_update"
	brokerFrameSessionGone     = "session_gone"
)

var brokerRegistrationTimeout = 5 * time.Second

type BrokerSession struct {
	SessionID      string `json:"session_id"`
	DeviceID       string `json:"device_id,omitempty"`
	Launcher       string `json:"launcher,omitempty"`
	Label          string `json:"label,omitempty"`
	CWD            string `json:"cwd"`
	CommandPreview string `json:"command_preview"`
	GitBranch      string `json:"git_branch"`
	StartedAt      int    `json:"started_at"`
	UpdatedAt      int    `json:"updated_at"`
	Online         bool   `json:"online"`
	PlatformFamily string `json:"platform_family,omitempty"`
	PlatformID     string `json:"platform_id,omitempty"`
	ComputerName   string `json:"computer_name,omitempty"`
	LaunchSource   string `json:"launch_source,omitempty"`
}

type BrokerSessionSnapshot struct {
	BrokerSession
	LatestPreview string `json:"latest_preview"`
}

type BrokerFrame struct {
	Type      string         `json:"type"`
	SessionID string         `json:"session_id,omitempty"`
	Session   *BrokerSession `json:"session,omitempty"`
	Preview   string         `json:"preview,omitempty"`
	UpdatedAt int            `json:"updated_at,omitempty"`
}

type Broker struct {
	mu       sync.RWMutex
	sessions map[string]brokerSessionState
}

type brokerSessionState struct {
	session       BrokerSession
	latestPreview string
	owner         *brokerConnection
}

type brokerConnection struct {
	conn net.Conn
}

func NewBroker() *Broker {
	return &Broker{sessions: make(map[string]brokerSessionState)}
}

func (b *Broker) Snapshot() []BrokerSessionSnapshot {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BrokerSessionSnapshot, 0, len(b.sessions))
	for _, state := range b.sessions {
		out = append(out, BrokerSessionSnapshot{
			BrokerSession: state.session,
			LatestPreview: state.latestPreview,
		})
	}
	return out
}

func (b *Broker) register(session BrokerSession, owner *brokerConnection) {
	if b == nil || owner == nil {
		return
	}
	session = normalizeBrokerSession(session)
	if session.SessionID == "" {
		return
	}

	var previous *brokerConnection
	b.mu.Lock()
	if existing, ok := b.sessions[session.SessionID]; ok && existing.owner != owner {
		previous = existing.owner
	}
	state := b.sessions[session.SessionID]
	state.session = session
	state.owner = owner
	b.sessions[session.SessionID] = state
	b.mu.Unlock()

	if previous != nil && previous.conn != nil {
		_ = previous.conn.Close()
	}
}

func (b *Broker) updateSession(session BrokerSession, owner *brokerConnection) {
	if b == nil || owner == nil {
		return
	}
	session = normalizeBrokerSession(session)
	if session.SessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.sessions[session.SessionID]
	if !ok || state.owner != owner {
		return
	}
	state.session = session
	b.sessions[session.SessionID] = state
}

func (b *Broker) updatePreview(sessionID, preview string, updatedAt int, owner *brokerConnection) {
	if b == nil || owner == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.sessions[sessionID]
	if !ok || state.owner != owner {
		return
	}
	if updatedAt > 0 {
		state.session.UpdatedAt = updatedAt
	}
	state.latestPreview = tunnelsession.NormalizePreviewText(preview, tunnelsession.DefaultPreviewMaxChars)
	b.sessions[sessionID] = state
}

func (b *Broker) remove(sessionID string, owner *brokerConnection) {
	if b == nil || owner == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.sessions[sessionID]
	if !ok || state.owner != owner {
		return
	}
	delete(b.sessions, sessionID)
}

func normalizeBrokerSession(session BrokerSession) BrokerSession {
	session.SessionID = strings.TrimSpace(session.SessionID)
	session.DeviceID = strings.TrimSpace(session.DeviceID)
	session.Launcher = strings.TrimSpace(session.Launcher)
	session.Label = strings.TrimSpace(session.Label)
	session.CWD = strings.TrimSpace(session.CWD)
	session.CommandPreview = strings.TrimSpace(session.CommandPreview)
	session.GitBranch = strings.TrimSpace(session.GitBranch)
	session.PlatformFamily = strings.TrimSpace(session.PlatformFamily)
	session.PlatformID = strings.TrimSpace(session.PlatformID)
	session.ComputerName = strings.TrimSpace(session.ComputerName)
	session.LaunchSource = strings.TrimSpace(session.LaunchSource)
	session.Online = true
	if session.UpdatedAt <= 0 {
		session.UpdatedAt = int(time.Now().UTC().Unix())
	}
	return session
}

type BrokerServer struct {
	listener net.Listener
	broker   *Broker
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
}

func NewBrokerServer(socketPath string, broker *Broker) (*BrokerServer, error) {
	info, err := os.Lstat(socketPath)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("broker socket path exists and is not a unix socket")
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, err
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	if broker == nil {
		broker = NewBroker()
	}
	return &BrokerServer{listener: listener, broker: broker, conns: make(map[net.Conn]struct{})}, nil
}

func (s *BrokerServer) Serve(ctx context.Context) error {
	if s == nil || s.listener == nil {
		return nil
	}

	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				return err
			}
		}
		go s.serveConn(conn)
	}
}

func (s *BrokerServer) Close() error {
	if s == nil || s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	s.mu.Lock()
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()
	return err
}

func (s *BrokerServer) serveConn(conn net.Conn) {
	s.mu.Lock()
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
	owner := &brokerConnection{conn: conn}
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	var registeredSessionID string
	_ = conn.SetReadDeadline(time.Now().Add(brokerRegistrationTimeout))
	defer func() {
		if registeredSessionID != "" {
			s.broker.remove(registeredSessionID, owner)
		}
	}()

	decoder := json.NewDecoder(conn)
	for {
		var frame BrokerFrame
		if err := decoder.Decode(&frame); err != nil {
			return
		}
		switch frame.Type {
		case brokerFrameRegisterSession:
			if frame.Session == nil {
				continue
			}
			sessionID := strings.TrimSpace(frame.Session.SessionID)
			if sessionID == "" {
				continue
			}
			if registeredSessionID != "" && registeredSessionID != sessionID {
				s.broker.remove(registeredSessionID, owner)
			}
			_ = conn.SetReadDeadline(time.Time{})
			registeredSessionID = sessionID
			s.broker.register(*frame.Session, owner)
		case brokerFrameSessionUpdate:
			if frame.Session == nil {
				continue
			}
			s.broker.updateSession(*frame.Session, owner)
		case brokerFramePreviewUpdate:
			s.broker.updatePreview(frame.SessionID, frame.Preview, frame.UpdatedAt, owner)
		case brokerFrameSessionGone:
			sessionID := strings.TrimSpace(frame.SessionID)
			if sessionID == "" {
				sessionID = registeredSessionID
			}
			s.broker.remove(sessionID, owner)
			if sessionID == registeredSessionID {
				registeredSessionID = ""
			}
			return
		default:
		}
	}
}
