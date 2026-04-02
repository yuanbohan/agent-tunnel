package relayserver

import (
	"errors"
	"sort"
	"sync"
	"time"

	"yuanbohan/tunnel/internal/relayapi"
	"yuanbohan/tunnel/internal/session"
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
	r.sessions[info.SessionID] = &liveSession{
		info:  info,
		peer:  peer,
		sinks: make(map[string]session.OutputSink),
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

func (r *Registry) TouchOutput(sessionID string, chunk []byte, now time.Time) {
	r.mu.Lock()
	live, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return
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
	r.mu.RUnlock()
	if !ok {
		return ErrSessionNotFound
	}
	return live.peer.SendInput(data)
}

func (r *Registry) Resize(sessionID string, cols, rows int) error {
	r.mu.RLock()
	live, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return ErrSessionNotFound
	}
	return live.peer.Resize(cols, rows)
}

func lastActiveTime(info relayapi.SessionInfo) time.Time {
	if info.LastActiveAt == nil {
		return time.Time{}
	}
	return *info.LastActiveAt
}
