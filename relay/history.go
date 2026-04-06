package relay

import (
	"encoding/base64"
	"sort"
	"time"

	"yuanbohan/tunnel/protocol"
)

const (
	maxSessionHistoryBytes = 10 << 20
)

type outputFrame struct {
	Seq  uint64
	Data []byte
	Size int
	Cols int
	Rows int
	TS   time.Time
}

type outputFrameMessage struct {
	Seq     uint64    `json:"seq"`
	DataB64 string    `json:"data_b64"`
	Cols    int       `json:"cols"`
	Rows    int       `json:"rows"`
	TS      time.Time `json:"ts"`
}

func (s *liveSession) appendOutput(chunk []byte, cols, rows int, ts time.Time) uint64 {
	cp := append([]byte(nil), chunk...)
	seq := s.latestSeq + 1

	s.latestSeq = seq
	s.frames = append(s.frames, outputFrame{
		Seq:  seq,
		Data: cp,
		Size: len(cp),
		Cols: cols,
		Rows: rows,
		TS:   ts,
	})
	s.frameBytes += len(cp)

	for s.frameBytes > maxSessionHistoryBytes && len(s.frames) > 1 {
		s.frameBytes -= s.frames[0].Size
		s.frames = s.frames[1:]
	}

	return seq
}

func (s *liveSession) snapshot() protocol.SessionInfo {
	info := s.info
	info.LatestSeq = s.latestSeq
	return info
}

func (s *liveSession) frameSnapshot(from uint64, hasFrom bool, to uint64, hasTo bool) []outputFrameMessage {
	start := 0
	if hasFrom {
		start = sort.Search(len(s.frames), func(i int) bool {
			return s.frames[i].Seq >= from
		})
	}

	end := len(s.frames)
	if hasTo {
		end = sort.Search(len(s.frames), func(i int) bool {
			return s.frames[i].Seq > to
		})
	}

	if start >= len(s.frames) || start >= end {
		return []outputFrameMessage{}
	}

	frames := make([]outputFrameMessage, 0, end-start)
	for _, frame := range s.frames[start:end] {
		frames = append(frames, outputFrameMessage{
			Seq:     frame.Seq,
			DataB64: base64.StdEncoding.EncodeToString(frame.Data),
			Cols:    frame.Cols,
			Rows:    frame.Rows,
			TS:      frame.TS,
		})
	}
	return frames
}
