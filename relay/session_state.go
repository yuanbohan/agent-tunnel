package relay

import (
	"time"

	"yuanbohan/tunnel/protocol"
)

type sessionStateSink interface {
	WriteSessionStateEvent(protocol.SessionStateEvent) error
	Close() error
}

func (s *liveSession) updateSessionState(state protocol.SessionState, changedAt time.Time, actionRequiredSince *time.Time) (protocol.SessionStateEvent, bool) {
	changedAtCopy := changedAt.UTC()
	sinceCopy := cloneTimePtr(actionRequiredSince)
	if state == protocol.SessionStateNormal {
		sinceCopy = nil
	}

	sameState := s.info.State == state
	sameSince := timesEqual(s.info.ActionRequiredSince, sinceCopy)
	if sameState && sameSince {
		return protocol.SessionStateEvent{}, false
	}

	s.info.State = state
	s.info.StateChangedAt = &changedAtCopy
	s.info.ActionRequiredSince = sinceCopy

	return protocol.SessionStateEvent{
		SessionID:           s.info.SessionID,
		State:               state,
		ChangedAt:           changedAtCopy,
		ActionRequiredSince: cloneTimePtr(sinceCopy),
	}, true
}

func (s *liveSession) actionRequiredResolvedEvent(changedAt time.Time) (protocol.SessionStateEvent, bool) {
	if s.info.State != protocol.SessionStateActionRequired {
		return protocol.SessionStateEvent{}, false
	}
	changedAtCopy := changedAt.UTC()
	return protocol.SessionStateEvent{
		SessionID: s.info.SessionID,
		State:     protocol.SessionStateNormal,
		ChangedAt: changedAtCopy,
	}, true
}

func (r *Registry) broadcastSessionState(event protocol.SessionStateEvent) {
	r.mu.RLock()
	sinks := make([]sessionStateSink, 0, len(r.stateSinks))
	for _, sink := range r.stateSinks {
		sinks = append(sinks, sink)
	}
	r.mu.RUnlock()

	for _, sink := range sinks {
		_ = sink.WriteSessionStateEvent(event)
	}
}

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
