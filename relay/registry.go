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
	Send(protocol.Message) error
	Close() error
}

type Registry struct {
	mu             sync.RWMutex
	sessions       map[string]*liveSession
	updateSinks    map[string]clientUpdateSink
	logger         *Logger
	reconnectGrace time.Duration
}

type historyResult struct {
	frames []protocol.ReplayFrame
	err    error
}

type liveSession struct {
	info           protocol.SessionInfo
	peer           AgentPeer
	removeTimer    *time.Timer
	pendingHistory map[string]chan historyResult
}

func NewRegistry() *Registry {
	return &Registry{
		sessions:       make(map[string]*liveSession),
		updateSinks:    make(map[string]clientUpdateSink),
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
	var pending []chan historyResult

	if old != nil {
		if old.removeTimer != nil {
			old.removeTimer.Stop()
		}
		if old.info.LastActiveAt != nil && (lastActiveAt == nil || old.info.LastActiveAt.After(*lastActiveAt)) {
			lastActiveAt = old.info.LastActiveAt
		}
		if info.LatestSeq < old.info.LatestSeq {
			info.LatestSeq = old.info.LatestSeq
		}
		pending = takeAllPendingHistoryLocked(old)
	}

	info.LastActiveAt = lastActiveAt
	info.State = protocol.SessionStateConnected
	r.sessions[info.SessionID] = &liveSession{
		info:           info,
		peer:           peer,
		pendingHistory: make(map[string]chan historyResult),
	}
	r.mu.Unlock()

	failHistoryWaiters(pending, ErrSessionReconnecting)

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
	var pending []chan historyResult
	if ok {
		if live.removeTimer != nil {
			live.removeTimer.Stop()
		}
		pending = takeAllPendingHistoryLocked(live)
	}
	r.mu.Unlock()

	if !ok {
		return
	}
	failHistoryWaiters(pending, ErrSessionNotFound)
	r.broadcastClientUpdate(protocol.EncodeClientSessionRemoved(sessionID, "session_removed"))
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
	pending := takeAllPendingHistoryLocked(live)
	r.mu.Unlock()

	failHistoryWaiters(pending, ErrSessionReconnecting)
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

func (r *Registry) TouchOutputIfOwner(sessionID string, owner AgentPeer, frame protocol.ReplayFrame) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.Unlock()
		return false
	}
	if frame.Seq > live.info.LatestSeq {
		live.info.LatestSeq = frame.Seq
	}
	if !frame.TS.IsZero() {
		nowCopy := frame.TS
		live.info.LastActiveAt = &nowCopy
	}
	live.info.State = protocol.SessionStateConnected
	r.mu.Unlock()

	r.broadcastClientUpdate(protocol.EncodeClientOutputFrame(sessionID, frame))
	return true
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

func (r *Registry) StartHistoryRequest(sessionID, requestID string) (AgentPeer, <-chan historyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.sessions[sessionID]
	if !ok {
		return nil, nil, ErrSessionNotFound
	}
	if live.peer == nil || live.info.State != protocol.SessionStateConnected {
		return nil, nil, ErrSessionReconnecting
	}
	ch := make(chan historyResult, 1)
	live.pendingHistory[requestID] = ch
	return live.peer, ch, nil
}

func (r *Registry) ResolveHistoryRequest(sessionID string, owner AgentPeer, requestID string, frames []protocol.ReplayFrame) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.Unlock()
		return false
	}
	ch, ok := live.pendingHistory[requestID]
	if ok {
		delete(live.pendingHistory, requestID)
	}
	r.mu.Unlock()

	if !ok {
		return false
	}
	ch <- historyResult{frames: append([]protocol.ReplayFrame(nil), frames...)}
	close(ch)
	return true
}

func (r *Registry) FailHistoryRequest(sessionID, requestID string, err error) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return false
	}
	ch, ok := live.pendingHistory[requestID]
	if ok {
		delete(live.pendingHistory, requestID)
	}
	r.mu.Unlock()

	if !ok {
		return false
	}
	ch <- historyResult{err: err}
	close(ch)
	return true
}

func (r *Registry) FailPendingHistoryRequestsIfOwner(sessionID string, owner AgentPeer, err error) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		r.mu.Unlock()
		return false
	}
	pending := takeAllPendingHistoryLocked(live)
	r.mu.Unlock()

	failHistoryWaiters(pending, err)
	return len(pending) > 0
}

func (r *Registry) PendingHistoryCountIfOwner(sessionID string, owner AgentPeer) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	live, ok := r.sessions[sessionID]
	if !ok || live.peer != owner {
		return 0
	}
	return len(live.pendingHistory)
}

func (r *Registry) WriteInput(sessionID string, msg protocol.Message) error {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.RUnlock()
		return ErrSessionNotFound
	}
	if live.peer == nil || live.info.State != protocol.SessionStateConnected {
		r.mu.RUnlock()
		return ErrSessionReconnecting
	}
	err := live.peer.Send(msg)
	r.mu.RUnlock()
	return err
}

func (r *Registry) expireReconnectingSession(sessionID string) {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || live.info.State != protocol.SessionStateReconnecting || live.peer != nil {
		r.mu.Unlock()
		return
	}
	delete(r.sessions, sessionID)
	pending := takeAllPendingHistoryLocked(live)
	r.mu.Unlock()

	failHistoryWaiters(pending, ErrSessionNotFound)
	r.broadcastClientUpdate(protocol.EncodeClientSessionRemoved(sessionID, "session_removed"))
}

func takeAllPendingHistoryLocked(live *liveSession) []chan historyResult {
	if len(live.pendingHistory) == 0 {
		return nil
	}
	pending := make([]chan historyResult, 0, len(live.pendingHistory))
	for requestID, ch := range live.pendingHistory {
		delete(live.pendingHistory, requestID)
		pending = append(pending, ch)
	}
	return pending
}

func failHistoryWaiters(waiters []chan historyResult, err error) {
	for _, ch := range waiters {
		ch <- historyResult{err: err}
		close(ch)
	}
}

func (s *liveSession) snapshot() protocol.SessionInfo {
	return s.info
}

func lastActiveTime(info protocol.SessionInfo) time.Time {
	if info.LastActiveAt == nil {
		return time.Time{}
	}
	return *info.LastActiveAt
}
