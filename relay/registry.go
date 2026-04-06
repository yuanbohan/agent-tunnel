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
	SendInput([]byte) error
	Close() error
}

type Registry struct {
	mu          sync.RWMutex
	sessions    map[string]*liveSession
	updateSinks map[string]clientUpdateSink
	logger      *Logger
}

type liveSession struct {
	info protocol.SessionInfo
	peer AgentPeer

	frames      []outputFrame
	frameBytes  int
	latestSeq   uint64
	currentCols int
	currentRows int
}

func NewRegistry() *Registry {
	return &Registry{
		sessions:    make(map[string]*liveSession),
		updateSinks: make(map[string]clientUpdateSink),
		logger:      NewDiscardLogger(),
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
	var frames []outputFrame
	var frameBytes int
	var latestSeq uint64
	var currentCols int
	var currentRows int
	lastActiveAt := info.LastActiveAt
	if old != nil {
		frames = old.frames
		frameBytes = old.frameBytes
		latestSeq = old.latestSeq
		currentCols = old.currentCols
		currentRows = old.currentRows
		if old.info.LastActiveAt != nil {
			lastActiveAt = old.info.LastActiveAt
		}
	}
	r.sessions[info.SessionID] = &liveSession{
		info:        info,
		peer:        peer,
		frames:      frames,
		frameBytes:  frameBytes,
		latestSeq:   latestSeq,
		currentCols: currentCols,
		currentRows: currentRows,
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
		out = append(out, live.snapshot())
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

func (r *Registry) TouchOutputIfOwner(sessionID string, owner AgentPeer, chunk []byte, now time.Time) bool {
	return r.touchOutput(sessionID, owner, chunk, now, true)
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

func (r *Registry) Frames(sessionID string, from uint64, hasFrom bool, to uint64, hasTo bool) ([]outputFrameMessage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	live, ok := r.sessions[sessionID]
	if !ok {
		return nil, false
	}
	return live.frameSnapshot(from, hasFrom, to, hasTo), true
}

func (r *Registry) WriteInput(sessionID string, data []byte) error {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.RUnlock()
		return ErrSessionNotFound
	}
	err := live.peer.SendInput(data)
	r.mu.RUnlock()
	return err
}

func (r *Registry) UpdateSizeIfOwner(sessionID string, owner AgentPeer, cols, rows int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		return false
	}
	live.currentCols = cols
	live.currentRows = rows
	return true
}

func lastActiveTime(info protocol.SessionInfo) time.Time {
	if info.LastActiveAt == nil {
		return time.Time{}
	}
	return *info.LastActiveAt
}

func (r *Registry) touchOutput(sessionID string, owner AgentPeer, chunk []byte, now time.Time, requireOwner bool) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || (requireOwner && live.peer != owner) {
		r.mu.Unlock()
		return false
	}
	seq := live.appendOutput(chunk)
	cols := live.currentCols
	rows := live.currentRows
	nowCopy := now
	live.info.LastActiveAt = &nowCopy
	r.mu.Unlock()

	r.broadcastClientUpdate(protocol.EncodeClientOutput(sessionID, seq, chunk, cols, rows))
	return true
}
