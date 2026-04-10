package relay

import (
	"errors"
	"sort"
	"sync"
	"time"

	"yuanbohan/tunnel/protocol"
)

var (
	ErrSessionNotFound     = errors.New("relay session not found")
	ErrSessionReconnecting = errors.New("relay session reconnecting")
)

const defaultReconnectGrace = 60 * time.Second

type AgentPeer interface {
	SendJSON(any) error
	SendBinary([]byte) error
	Close() error
}

type AttachPeer interface {
	SendControl(protocol.AttachControlMessage) error
	SendBinary([]byte) error
	Close(reason string) error
}

type Registry struct {
	mu             sync.RWMutex
	sessions       map[string]*liveSession
	logger         *Logger
	reconnectGrace time.Duration
}

type liveSession struct {
	info            protocol.SessionInfo
	peer            AgentPeer
	removeTimer     *time.Timer
	pendingAttached map[string]AttachPeer
	attached        map[string]AttachPeer
}

func NewRegistry() *Registry {
	return &Registry{
		sessions:       make(map[string]*liveSession),
		logger:         NewDiscardLogger(),
		reconnectGrace: defaultReconnectGrace,
	}
}

func (r *Registry) SetLogger(logger *Logger) {
	if r == nil {
		return
	}
	if logger == nil {
		logger = NewDiscardLogger()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
}

func (r *Registry) Register(info protocol.SessionInfo, peer AgentPeer) {
	r.mu.Lock()
	old := r.sessions[info.SessionID]
	logger := r.logger
	lastActiveAt := info.LastActiveAt
	var attached []AttachPeer

	if old != nil {
		if old.removeTimer != nil {
			old.removeTimer.Stop()
		}
		if old.info.LastActiveAt != nil && (lastActiveAt == nil || *old.info.LastActiveAt > *lastActiveAt) {
			lastActiveAt = old.info.LastActiveAt
		}
		attached = takeAllAttachedLocked(old)
	}

	info.LastActiveAt = lastActiveAt
	info.State = protocol.SessionStateConnected
	r.sessions[info.SessionID] = &liveSession{
		info:            info,
		peer:            peer,
		pendingAttached: make(map[string]AttachPeer),
		attached:        make(map[string]AttachPeer),
	}
	r.mu.Unlock()

	closeAttachedPeers(attached, "session_reconnecting")

	if old != nil {
		logger.Warn("session_replaced", String("session_id", info.SessionID))
	}
	if old != nil && old.peer != nil {
		_ = old.peer.Close()
	}
}

func (r *Registry) Remove(sessionID string) {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if ok {
		delete(r.sessions, sessionID)
	}
	var attached []AttachPeer
	if ok {
		if live.removeTimer != nil {
			live.removeTimer.Stop()
		}
		attached = takeAllAttachedLocked(live)
	}
	r.mu.Unlock()

	if !ok {
		return
	}
	closeAttachedPeers(attached, "session_removed")
}

func (r *Registry) DisconnectIfOwner(sessionID string, owner AgentPeer) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || owner == nil || live.peer != owner {
		r.mu.Unlock()
		return false
	}

	live.peer = nil
	live.info.State = protocol.SessionStateReconnecting
	if live.removeTimer != nil {
		live.removeTimer.Stop()
	}
	sessionIDCopy := sessionID
	live.removeTimer = time.AfterFunc(r.reconnectGrace, func() {
		r.expireReconnectingSession(sessionIDCopy)
	})
	attached := takeAllAttachedLocked(live)
	r.mu.Unlock()

	closeAttachedPeers(attached, "session_reconnecting")
	_ = owner.Close()
	return true
}

func (r *Registry) HasSession(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.sessions[sessionID]
	return ok
}

func (r *Registry) List() []protocol.SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]protocol.SessionInfo, 0, len(r.sessions))
	for _, live := range r.sessions {
		out = append(out, live.snapshot())
	}
	sort.Slice(out, func(i, j int) bool {
		ti := lastActiveTime(out[i])
		tj := lastActiveTime(out[j])
		if ti == tj {
			return out[i].SessionID < out[j].SessionID
		}
		return ti > tj
	})
	return out
}

func (r *Registry) Session(sessionID string) (protocol.SessionInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	live, ok := r.sessions[sessionID]
	if !ok {
		return protocol.SessionInfo{}, false
	}
	return live.snapshot(), true
}

func (r *Registry) WriteInput(sessionID string, msg protocol.Message) error {
	return r.SendToOwner(sessionID, protocol.AgentFrame{
		Type:   msg.Type,
		Text:   msg.Text,
		Submit: msg.Submit,
		Key:    msg.Key,
	})
}

func (r *Registry) WriteAttachInput(sessionID string, frame protocol.AgentFrame) error {
	return r.SendToOwner(sessionID, frame)
}

func (r *Registry) SendToOwner(sessionID string, payload any) error {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.RUnlock()
		return ErrSessionNotFound
	}
	peer := live.peer
	state := live.info.State
	r.mu.RUnlock()

	if peer == nil || state != protocol.SessionStateConnected {
		return ErrSessionReconnecting
	}
	return peer.SendJSON(payload)
}

func (r *Registry) StartAttach(sessionID, clientID string, client AttachPeer) (AgentPeer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if live.peer == nil || live.info.State != protocol.SessionStateConnected {
		return nil, ErrSessionReconnecting
	}
	live.pendingAttached[clientID] = client
	return live.peer, nil
}

func (r *Registry) DetachClient(sessionID, clientID, reason string) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return false
	}
	client, ok := removeAttachClientLocked(live, clientID)
	owner := live.peer
	r.mu.Unlock()

	if !ok {
		return false
	}
	_ = client.Close(reason)
	if owner != nil && reason != "" {
		_ = owner.SendJSON(protocol.AttachCloseFrame(clientID, reason))
	}
	return true
}

func (r *Registry) RouteAttachReadyIfOwner(sessionID string, owner AgentPeer, clientID string, cols, rows int) bool {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.RUnlock()
		return false
	}
	client, ok := live.pendingAttached[clientID]
	info := live.info
	r.mu.RUnlock()

	if !ok {
		return false
	}
	if err := client.SendControl(protocol.AttachedMessage(info.SessionID, cols, rows)); err != nil {
		return r.DetachClient(sessionID, clientID, "slow_client")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok = r.sessions[sessionID]
	if !ok || live.peer != owner {
		return false
	}
	pending, ok := live.pendingAttached[clientID]
	if !ok || pending != client {
		return false
	}
	delete(live.pendingAttached, clientID)
	live.attached[clientID] = client
	return true
}

func (r *Registry) RouteSnapshotDoneIfOwner(sessionID string, owner AgentPeer, clientID string) bool {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.RUnlock()
		return false
	}
	client, ok := live.attached[clientID]
	r.mu.RUnlock()

	if !ok {
		return false
	}
	if err := client.SendControl(protocol.SnapshotDoneMessage()); err != nil {
		return r.DetachClient(sessionID, clientID, "slow_client")
	}
	return true
}

func (r *Registry) RouteResizeIfOwner(sessionID string, owner AgentPeer, cols, rows int) bool {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.RUnlock()
		return false
	}
	clients := snapshotAttachedClients(live.attached)
	r.mu.RUnlock()

	for clientID, client := range clients {
		if err := client.SendControl(protocol.ResizeMessage(cols, rows)); err != nil {
			r.DetachClient(sessionID, clientID, "slow_client")
		}
	}
	return true
}

func (r *Registry) RouteAttachCloseIfOwner(sessionID string, owner AgentPeer, clientID, reason string) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.Unlock()
		return false
	}
	client, ok := removeAttachClientLocked(live, clientID)
	r.mu.Unlock()

	if !ok {
		return false
	}
	_ = client.Close(reason)
	return true
}

func (r *Registry) RouteTerminalBytesIfOwner(sessionID string, owner AgentPeer, packet protocol.AttachPacket) bool {
	if len(packet.Payload) == 0 {
		return false
	}

	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.RUnlock()
		return false
	}
	client, ok := live.attached[packet.ClientID]
	r.mu.RUnlock()

	if !ok {
		return false
	}
	if err := client.SendBinary(packet.Payload); err != nil {
		return r.DetachClient(sessionID, packet.ClientID, "slow_client")
	}
	return true
}

func (r *Registry) TouchActivityIfOwner(sessionID string, owner AgentPeer, lastActiveAt int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		return false
	}
	if lastActiveAt > 0 {
		tsCopy := lastActiveAt
		live.info.LastActiveAt = &tsCopy
	}
	live.info.State = protocol.SessionStateConnected
	return true
}

func (r *Registry) expireReconnectingSession(sessionID string) {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || live.info.State != protocol.SessionStateReconnecting || live.peer != nil {
		r.mu.Unlock()
		return
	}
	delete(r.sessions, sessionID)
	attached := takeAllAttachedLocked(live)
	r.mu.Unlock()

	closeAttachedPeers(attached, "session_removed")
}

func takeAllAttachedLocked(live *liveSession) []AttachPeer {
	total := len(live.pendingAttached) + len(live.attached)
	if total == 0 {
		return nil
	}
	attached := make([]AttachPeer, 0, total)
	for clientID, client := range live.pendingAttached {
		delete(live.pendingAttached, clientID)
		attached = append(attached, client)
	}
	for clientID, client := range live.attached {
		delete(live.attached, clientID)
		attached = append(attached, client)
	}
	return attached
}

func removeAttachClientLocked(live *liveSession, clientID string) (AttachPeer, bool) {
	if client, ok := live.pendingAttached[clientID]; ok {
		delete(live.pendingAttached, clientID)
		return client, true
	}
	client, ok := live.attached[clientID]
	if ok {
		delete(live.attached, clientID)
	}
	return client, ok
}

func snapshotAttachedClients(attached map[string]AttachPeer) map[string]AttachPeer {
	out := make(map[string]AttachPeer, len(attached))
	for clientID, client := range attached {
		out[clientID] = client
	}
	return out
}

func closeAttachedPeers(peers []AttachPeer, reason string) {
	for _, peer := range peers {
		_ = peer.Close(reason)
	}
}

func (s *liveSession) snapshot() protocol.SessionInfo {
	return s.info
}

func lastActiveTime(info protocol.SessionInfo) int {
	if info.LastActiveAt == nil {
		return 0
	}
	return *info.LastActiveAt
}
