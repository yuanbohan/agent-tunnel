package relay

import (
	"time"

	"yuanbohan/tunnel/protocol"
)

// updateSessionState mutates only structured session metadata. Output frames and
// replay history stay separate, which lets the relay remain content-opaque while
// still exposing transport-safe lifecycle state such as action_required.
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

func (r *Registry) broadcastSessionStateUpdate(event protocol.SessionStateEvent) {
	r.broadcastClientUpdate(protocol.EncodeClientSessionState(event))
}

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
