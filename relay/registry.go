package relay

import (
	"errors"
	"sort"
	"sync"
	"time"

	"yuanbohan/tunnel/protocol"
)

var ErrSessionNotFound = errors.New("relay session not found")

type AgentPeer interface {
	Send(protocol.Message) error
	Close() error
}

type Registry struct {
	mu          sync.RWMutex
	sessions    map[string]*liveSession
	updateSinks map[string]clientUpdateSink
	logger      *Logger
	history     HistoryStore
	gateMu      sync.Mutex
	gates       map[string]*sessionGate
}

type liveSession struct {
	info protocol.SessionInfo
	peer AgentPeer
}

type sessionGate struct {
	mu   sync.Mutex
	refs int
}

func NewRegistry() *Registry {
	return NewRegistryWithHistoryStore(nil)
}

func NewRegistryWithHistoryStore(store HistoryStore) *Registry {
	if store == nil {
		store = newInMemoryHistoryStore()
	}
	return &Registry{
		sessions:    make(map[string]*liveSession),
		updateSinks: make(map[string]clientUpdateSink),
		logger:      NewDiscardLogger(),
		history:     store,
		gates:       make(map[string]*sessionGate),
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

func (r *Registry) RetainedLatestSeq(sessionID string) (uint64, bool, error) {
	return r.history.LatestSeq(sessionID)
}

func (r *Registry) Register(info protocol.SessionInfo, peer AgentPeer) {
	unlock := r.lockSessionGate(info.SessionID)
	defer unlock()

	r.registerLocked(info, peer)
}

func (r *Registry) registerLocked(info protocol.SessionInfo, peer AgentPeer) {
	r.mu.Lock()
	old := r.sessions[info.SessionID]
	logger := r.logger
	lastActiveAt := info.LastActiveAt
	if old != nil {
		if old.info.LastActiveAt != nil {
			lastActiveAt = old.info.LastActiveAt
		}
		if old.info.LatestSeq > info.LatestSeq {
			info.LatestSeq = old.info.LatestSeq
		}
	}
	r.sessions[info.SessionID] = &liveSession{
		info: info,
		peer: peer,
	}
	r.sessions[info.SessionID].info.LastActiveAt = lastActiveAt
	r.mu.Unlock()

	if old != nil {
		logger.Warn("session_replaced", String("session_id", info.SessionID))
	}
	if old != nil && old.peer != nil {
		_ = old.peer.Close()
	}
}

func (r *Registry) Remove(sessionID string) {
	unlock := r.lockSessionGate(sessionID)
	defer unlock()

	r.mu.Lock()
	_, ok := r.sessions[sessionID]
	if ok {
		delete(r.sessions, sessionID)
	}
	r.mu.Unlock()

	if !ok {
		return
	}
	r.broadcastClientUpdate(protocol.EncodeClientSessionRemoved(sessionID, "session_removed"))
}

func (r *Registry) RemoveIfOwner(sessionID string, owner AgentPeer) bool {
	unlock := r.lockSessionGate(sessionID)
	defer unlock()

	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || owner == nil || live.peer != owner {
		r.mu.Unlock()
		return false
	}
	delete(r.sessions, sessionID)
	r.mu.Unlock()

	r.broadcastClientUpdate(protocol.EncodeClientSessionRemoved(sessionID, "agent_disconnected"))
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
		out = append(out, live.info)
	}
	sort.Slice(out, func(i, j int) bool {
		ti := lastActiveTime(out[i])
		tj := lastActiveTime(out[j])
		if ti.Equal(tj) {
			return out[i].SessionID < out[j].SessionID
		}
		return ti.After(tj)
	})
	return out
}

func (r *Registry) TouchOutputIfOwner(sessionID string, owner AgentPeer, chunk []byte, cols, rows int, now time.Time) (bool, error) {
	return r.touchOutput(sessionID, owner, chunk, cols, rows, now, true)
}

func (r *Registry) Session(sessionID string) (protocol.SessionInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	live, ok := r.sessions[sessionID]
	if !ok {
		return protocol.SessionInfo{}, false
	}
	return live.info, true
}

func (r *Registry) Frames(sessionID string, from uint64, hasFrom bool, to uint64, hasTo bool) ([]outputFrameMessage, bool, error) {
	unlock := r.lockSessionGate(sessionID)
	defer unlock()

	frames, ok, err := r.history.Frames(sessionID, from, hasFrom, to, hasTo)
	if err != nil {
		return nil, false, err
	}
	if ok {
		return frames, true, nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, live := r.sessions[sessionID]; live {
		return []outputFrameMessage{}, true, nil
	}
	return nil, false, nil
}

func (r *Registry) WriteInput(sessionID string, msg protocol.Message) error {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.RUnlock()
		return ErrSessionNotFound
	}
	err := live.peer.Send(msg)
	r.mu.RUnlock()
	return err
}

func lastActiveTime(info protocol.SessionInfo) time.Time {
	if info.LastActiveAt == nil {
		return time.Time{}
	}
	return *info.LastActiveAt
}

func (r *Registry) touchOutput(sessionID string, owner AgentPeer, chunk []byte, cols, rows int, now time.Time, requireOwner bool) (bool, error) {
	unlock := r.lockSessionGate(sessionID)
	defer unlock()

	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok || (requireOwner && live.peer != owner) {
		r.mu.RUnlock()
		return false, nil
	}
	r.mu.RUnlock()

	seq, err := r.history.AppendFrame(sessionID, chunk, cols, rows, now)
	if err != nil {
		return true, err
	}

	r.mu.Lock()
	live, ok = r.sessions[sessionID]
	if !ok || (requireOwner && live.peer != owner) {
		r.mu.Unlock()
		return false, nil
	}
	nowCopy := now
	live.info.LastActiveAt = &nowCopy
	live.info.LatestSeq = seq
	r.mu.Unlock()

	r.broadcastClientUpdate(protocol.EncodeClientOutput(sessionID, seq, chunk, cols, rows, now))
	return true, nil
}

func (r *Registry) lockSessionGate(sessionID string) func() {
	r.gateMu.Lock()
	gate := r.gates[sessionID]
	if gate == nil {
		gate = &sessionGate{}
		r.gates[sessionID] = gate
	}
	gate.refs++
	r.gateMu.Unlock()

	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()

		r.gateMu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(r.gates, sessionID)
		}
		r.gateMu.Unlock()
	}
}
