package relay

import (
	"encoding/base64"
	"sort"

	"yuanbohan/tunnel/protocol"
)

const (
	maxSessionHistoryBytes     = 10 << 20
	defaultHistoryPageLimit    = 5
	defaultHistoryPageMaxBytes = 64 << 10
)

type historyFrame struct {
	Seq  uint64
	Data []byte
	Size int
	Cols int
	Rows int
}

type historyMessage struct {
	Seq     uint64 `json:"seq"`
	DataB64 string `json:"data_b64"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

type historyPage struct {
	Messages    []historyMessage `json:"messages"`
	LatestSeq   uint64           `json:"latest_seq"`
	LastReadSeq uint64           `json:"last_read_seq"`
	CurrentCols int              `json:"current_cols"`
	CurrentRows int              `json:"current_rows"`
}

func (s *liveSession) appendOutput(chunk []byte) uint64 {
	cp := append([]byte(nil), chunk...)
	seq := s.latestSeq + 1

	s.latestSeq = seq
	s.history = append(s.history, historyFrame{
		Seq:  seq,
		Data: cp,
		Size: len(cp),
		Cols: s.currentCols,
		Rows: s.currentRows,
	})
	s.historyBytes += len(cp)

	for s.historyBytes > maxSessionHistoryBytes && len(s.history) > 1 {
		s.historyBytes -= s.history[0].Size
		s.history = s.history[1:]
	}

	return seq
}

func (s *liveSession) markRead(seq uint64) {
	if seq > s.latestSeq {
		seq = s.latestSeq
	}
	if seq > s.lastReadSeq {
		s.lastReadSeq = seq
	}
}

func (s *liveSession) unreadCount() uint64 {
	if s.latestSeq <= s.lastReadSeq {
		return 0
	}
	return s.latestSeq - s.lastReadSeq
}

func (s *liveSession) snapshot() protocol.SessionInfo {
	info := s.info
	if info.State == "" {
		info.State = protocol.SessionStateNormal
	}
	info.LatestSeq = s.latestSeq
	info.LastReadSeq = s.lastReadSeq
	info.UnreadCount = s.unreadCount()
	return info
}

func (s *liveSession) historySnapshot(after uint64) historyPage {
	start := 0
	if after > 0 {
		start = sort.Search(len(s.history), func(i int) bool {
			return s.history[i].Seq > after
		})
	}
	if start >= len(s.history) {
		return historyPage{
			LatestSeq:   s.latestSeq,
			LastReadSeq: s.lastReadSeq,
			CurrentCols: s.currentCols,
			CurrentRows: s.currentRows,
		}
	}

	messages := make([]historyMessage, 0, len(s.history)-start)
	for _, frame := range s.history[start:] {
		messages = append(messages, historyMessage{
			Seq:     frame.Seq,
			DataB64: base64.StdEncoding.EncodeToString(frame.Data),
			Cols:    frame.Cols,
			Rows:    frame.Rows,
		})
	}

	return historyPage{
		Messages:    messages,
		LatestSeq:   s.latestSeq,
		LastReadSeq: s.lastReadSeq,
		CurrentCols: s.currentCols,
		CurrentRows: s.currentRows,
	}
}
