package relayserver

import (
	"errors"
	"sort"
	"sync"
	"time"

	"yuanbohan/tunnel/internal/relayapi"
	"yuanbohan/tunnel/session"
)

var ErrSessionNotFound = errors.New("relay session not found")

type AgentPeer interface {
	SendInput([]byte) error
	Resize(int, int) error
	Close() error
}

type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*liveSession
}

type liveSession struct {
	info  relayapi.SessionInfo
	peer  AgentPeer
	sinks map[string]session.OutputSink
}

func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]*liveSession)}
}

func (r *Registry) Register(info relayapi.SessionInfo, peer AgentPeer) {
	r.mu.Lock()
	old := r.sessions[info.SessionID]
	var sinks map[string]session.OutputSink
	if old != nil {
		sinks = old.sinks
	}
	if sinks == nil {
		sinks = make(map[string]session.OutputSink)
	}
	r.sessions[info.SessionID] = &liveSession{
		info:  info,
		peer:  peer,
		sinks: sinks,
	}
	r.mu.Unlock()

	if old != nil && old.peer != nil {
		_ = old.peer.Close()
	}
}

func (r *Registry) Remove(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

func (r *Registry) RemoveIfOwner(sessionID string, owner AgentPeer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.sessions[sessionID]
	if !ok || owner == nil || live.peer != owner {
		return false
	}
	delete(r.sessions, sessionID)
	return true
}

func (r *Registry) HasSession(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.sessions[sessionID]
	return ok
}

func (r *Registry) List() []relayapi.SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]relayapi.SessionInfo, 0, len(r.sessions))
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

// TouchOutput is the legacy sessionID-only output path.
// Prefer TouchOutputIfOwner for peer-owned loops so stale peers cannot mutate state.
func (r *Registry) TouchOutput(sessionID string, chunk []byte, now time.Time) {
	r.touchOutput(sessionID, nil, chunk, now, false)
}

func (r *Registry) TouchOutputIfOwner(sessionID string, owner AgentPeer, chunk []byte, now time.Time) bool {
	return r.touchOutput(sessionID, owner, chunk, now, true)
}

func (r *Registry) AddSink(sessionID, sinkID string, sink session.OutputSink) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	live, ok := r.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	live.sinks[sinkID] = sink
	return nil
}

func (r *Registry) RemoveSink(sessionID, sinkID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if live, ok := r.sessions[sessionID]; ok {
		delete(live.sinks, sinkID)
	}
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

func (r *Registry) Resize(sessionID string, cols, rows int) error {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.RUnlock()
		return ErrSessionNotFound
	}
	err := live.peer.Resize(cols, rows)
	r.mu.RUnlock()
	return err
}

func lastActiveTime(info relayapi.SessionInfo) time.Time {
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
	nowCopy := now
	live.info.LastActiveAt = &nowCopy
	if preview, ok := ExtractPreview(chunk); ok {
		live.info.LastPreview = preview
	}
	sinks := make([]session.OutputSink, 0, len(live.sinks))
	for _, sink := range live.sinks {
		sinks = append(sinks, sink)
	}
	r.mu.Unlock()

	for _, sink := range sinks {
		cp := append([]byte(nil), chunk...)
		_ = sink.WriteOutput(cp)
	}
	return true
}
