package session

import (
	"errors"
	"strings"
	"sync"

	"yuanbohan/tunnel/internal/logx"
	"yuanbohan/tunnel/internal/protocol"
)

var (
	ErrAgentPeerInactive = errors.New("agent peer inactive")
)

type AgentPeer interface {
	SendJSON(any) error
	Close() error
}

type deactivatableAgentPeer interface {
	Deactivate()
}

type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*liveSession
}

type SessionOwner struct {
	UserID       int64
	AgentTokenID string
}

type liveSession struct {
	info  protocol.SessionInfo
	owner SessionOwner
	peer  AgentPeer
}

func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*liveSession),
	}
}

func (r *Registry) Register(info protocol.SessionInfo, peer AgentPeer) {
	r.RegisterOwned(info, SessionOwner{}, peer)
}

func (r *Registry) RegisterOwned(info protocol.SessionInfo, owner SessionOwner, peer AgentPeer) {
	r.mu.Lock()
	old := r.sessions[info.SessionID]

	if old != nil {
		if old.peer != peer {
			deactivateAgentPeer(old.peer)
		}
	}

	r.sessions[info.SessionID] = &liveSession{
		info:  info,
		owner: owner,
		peer:  peer,
	}
	r.mu.Unlock()

	if old != nil {
		logx.Warn("session_replaced", logx.String("session_id", info.SessionID))
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
	r.mu.Unlock()

	if !ok || live == nil {
		return
	}
}

func (r *Registry) DisconnectIfOwner(sessionID string, owner AgentPeer) bool {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok || owner == nil || live.peer != owner {
		r.mu.Unlock()
		return false
	}

	delete(r.sessions, sessionID)
	deactivateAgentPeer(owner)
	r.mu.Unlock()

	_ = owner.Close()
	return true
}

func (r *Registry) HasSession(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.sessions[sessionID]
	return ok
}

func (r *Registry) SetLaunchSourceForUser(sessionID string, userID int64, launchSource string) bool {
	launchSource = strings.TrimSpace(launchSource)
	if launchSource == "" {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.sessions[sessionID]
	if !ok || live.owner.UserID != userID {
		return false
	}
	live.info.LaunchSource = launchSource
	return true
}

func (s *liveSession) snapshot() protocol.SessionInfo {
	return s.info
}

func deactivateAgentPeer(peer AgentPeer) {
	if peer == nil {
		return
	}
	if deactivatable, ok := peer.(deactivatableAgentPeer); ok {
		deactivatable.Deactivate()
	}
}

func (r *Registry) DisconnectUserSessions(userID int64, reason string) int {
	return r.disconnectMatching(func(live *liveSession) bool {
		return live.owner.UserID == userID
	}, reason)
}

func (r *Registry) DisconnectAgentTokenSessions(agentTokenID string, reason string) int {
	return r.disconnectMatching(func(live *liveSession) bool {
		return live.owner.AgentTokenID == agentTokenID
	}, reason)
}

func (r *Registry) disconnectMatching(match func(*liveSession) bool, reason string) int {
	r.mu.Lock()
	type disconnectedSession struct {
		peer AgentPeer
	}
	var toClose []disconnectedSession
	for sessionID, live := range r.sessions {
		if !match(live) {
			continue
		}
		delete(r.sessions, sessionID)
		deactivateAgentPeer(live.peer)
		toClose = append(toClose, disconnectedSession{
			peer: live.peer,
		})
	}
	r.mu.Unlock()

	for _, disconnected := range toClose {
		if disconnected.peer != nil {
			_ = disconnected.peer.Close()
		}
	}
	return len(toClose)
}
