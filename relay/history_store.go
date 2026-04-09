package relay

import (
	"sync"
	"time"
)

type HistoryStore interface {
	LatestSeq(sessionID string) (uint64, bool, error)
	AppendFrame(sessionID string, chunk []byte, cols, rows int, ts time.Time) (uint64, error)
	Frames(sessionID string, from uint64, hasFrom bool, to uint64, hasTo bool) ([]outputFrameMessage, bool, error)
}

type inMemoryHistoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*inMemoryHistory
}

type inMemoryHistory struct {
	firstSeq   uint64
	latestSeq  uint64
	frameBytes int
	frames     []inMemoryStoredFrame
}

type inMemoryStoredFrame struct {
	payload historyFramePayload
	size    int
}

func newInMemoryHistoryStore() HistoryStore {
	return &inMemoryHistoryStore{
		sessions: make(map[string]*inMemoryHistory),
	}
}

func (s *inMemoryHistoryStore) LatestSeq(sessionID string) (uint64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history, ok := s.sessions[sessionID]
	if !ok {
		return 0, false, nil
	}
	return history.latestSeq, true, nil
}

func (s *inMemoryHistoryStore) AppendFrame(sessionID string, chunk []byte, cols, rows int, ts time.Time) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history, ok := s.sessions[sessionID]
	if !ok {
		history = &inMemoryHistory{}
		s.sessions[sessionID] = history
	}

	seq := history.latestSeq + 1
	if history.firstSeq == 0 {
		history.firstSeq = seq
	}
	history.latestSeq = seq
	history.frames = append(history.frames, inMemoryStoredFrame{
		payload: newHistoryFramePayload(chunk, cols, rows, ts),
		size:    len(chunk),
	})
	history.frameBytes += len(chunk)

	for history.frameBytes > maxSessionHistoryBytes && len(history.frames) > 1 {
		history.frameBytes -= history.frames[0].size
		history.frames = history.frames[1:]
		history.firstSeq++
	}

	return seq, nil
}

func (s *inMemoryHistoryStore) Frames(sessionID string, from uint64, hasFrom bool, to uint64, hasTo bool) ([]outputFrameMessage, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history, ok := s.sessions[sessionID]
	if !ok {
		return nil, false, nil
	}
	if len(history.frames) == 0 {
		return []outputFrameMessage{}, true, nil
	}

	startSeq := history.firstSeq
	if hasFrom && from > startSeq {
		startSeq = from
	}
	endSeq := history.latestSeq
	if hasTo && to < endSeq {
		endSeq = to
	}
	if startSeq > endSeq {
		return []outputFrameMessage{}, true, nil
	}

	startIdx := int(startSeq - history.firstSeq)
	endIdx := int(endSeq-history.firstSeq) + 1
	payloads := make([]historyFramePayload, 0, endIdx-startIdx)
	for _, frame := range history.frames[startIdx:endIdx] {
		payloads = append(payloads, frame.payload)
	}
	return historyFrameMessages(startSeq, payloads), true, nil
}
