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
}

type historyMessage struct {
	Seq     uint64 `json:"seq"`
	DataB64 string `json:"data_b64"`
}

type historyPage struct {
	Messages    []historyMessage `json:"messages"`
	HasMore     bool             `json:"has_more"`
	LatestSeq   uint64           `json:"latest_seq"`
	LastReadSeq uint64           `json:"last_read_seq"`
}

func (s *liveSession) appendOutput(chunk []byte) uint64 {
	cp := append([]byte(nil), chunk...)
	seq := s.latestSeq + 1

	s.latestSeq = seq
	s.previewSeq = seq
	s.previewData = cp
	s.history = append(s.history, historyFrame{
		Seq:  seq,
		Data: cp,
		Size: len(cp),
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
	info.LatestSeq = s.latestSeq
	info.LastReadSeq = s.lastReadSeq
	info.UnreadCount = s.unreadCount()
	info.PreviewSeq = s.previewSeq
	if len(s.previewData) > 0 {
		info.PreviewB64 = base64.StdEncoding.EncodeToString(s.previewData)
		if preview, ok := ExtractPreview(s.previewData); ok {
			info.LastPreview = preview
		} else {
			info.LastPreview = ""
		}
	} else {
		info.PreviewB64 = ""
		info.LastPreview = ""
	}
	return info
}

func (s *liveSession) historySnapshot(before, after uint64, limit, maxBytes int) historyPage {
	if limit <= 0 {
		limit = defaultHistoryPageLimit
	}
	if maxBytes <= 0 {
		maxBytes = defaultHistoryPageMaxBytes
	}

	start := 0
	if after > 0 {
		start = sort.Search(len(s.history), func(i int) bool {
			return s.history[i].Seq > after
		})
	}

	end := len(s.history)
	if before > 0 {
		end = sort.Search(len(s.history), func(i int) bool {
			return s.history[i].Seq >= before
		})
	}
	if start >= end {
		return historyPage{
			LatestSeq:   s.latestSeq,
			LastReadSeq: s.lastReadSeq,
		}
	}

	if after > 0 {
		stop := start
		used := 0
		count := 0
		for i := start; i < end; i++ {
			frame := s.history[i]
			if count > 0 && (count >= limit || used+frame.Size > maxBytes) {
				break
			}
			stop = i + 1
			count++
			used += frame.Size
			if count >= limit || used >= maxBytes {
				break
			}
		}

		messages := make([]historyMessage, 0, stop-start)
		for _, frame := range s.history[start:stop] {
			messages = append(messages, historyMessage{
				Seq:     frame.Seq,
				DataB64: base64.StdEncoding.EncodeToString(frame.Data),
			})
		}

		return historyPage{
			Messages:    messages,
			HasMore:     stop < end,
			LatestSeq:   s.latestSeq,
			LastReadSeq: s.lastReadSeq,
		}
	}

	start = end
	used := 0
	count := 0
	for i := end - 1; i >= 0; i-- {
		frame := s.history[i]
		if count > 0 && (count >= limit || used+frame.Size > maxBytes) {
			break
		}
		start = i
		count++
		used += frame.Size
		if count >= limit || used >= maxBytes || i == 0 {
			break
		}
	}

	messages := make([]historyMessage, 0, end-start)
	for _, frame := range s.history[start:end] {
		messages = append(messages, historyMessage{
			Seq:     frame.Seq,
			DataB64: base64.StdEncoding.EncodeToString(frame.Data),
		})
	}

	return historyPage{
		Messages:    messages,
		HasMore:     start > 0,
		LatestSeq:   s.latestSeq,
		LastReadSeq: s.lastReadSeq,
	}
}
