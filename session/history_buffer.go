package session

import (
	"sort"
	"sync"
	"time"

	"yuanbohan/tunnel/protocol"
)

const DefaultHistoryBufferBytes = 10 << 20

type historyEntry struct {
	frame protocol.ReplayFrame
	size  int
}

type HistoryBuffer struct {
	mu         sync.RWMutex
	maxBytes   int
	frameBytes int
	latestSeq  uint64
	frames     []historyEntry
}

func NewHistoryBuffer(maxBytes int) *HistoryBuffer {
	if maxBytes <= 0 {
		maxBytes = DefaultHistoryBufferBytes
	}
	return &HistoryBuffer{maxBytes: maxBytes}
}

func (b *HistoryBuffer) AppendOutput(chunk []byte, cols, rows int, ts time.Time) protocol.ReplayFrame {
	b.mu.Lock()
	defer b.mu.Unlock()

	frame := protocol.EncodeReplayFrame(b.latestSeq+1, chunk, cols, rows, ts)
	entry := historyEntry{
		frame: frame,
		size:  len(chunk),
	}

	b.latestSeq = frame.Seq
	b.frames = append(b.frames, entry)
	b.frameBytes += entry.size

	for b.frameBytes > b.maxBytes && len(b.frames) > 1 {
		b.frameBytes -= b.frames[0].size
		b.frames = b.frames[1:]
	}

	return frame
}

func (b *HistoryBuffer) Snapshot(from, to *uint64) []protocol.ReplayFrame {
	b.mu.RLock()
	defer b.mu.RUnlock()

	start := 0
	if from != nil {
		start = sort.Search(len(b.frames), func(i int) bool {
			return b.frames[i].frame.Seq >= *from
		})
	}

	end := len(b.frames)
	if to != nil {
		end = sort.Search(len(b.frames), func(i int) bool {
			return b.frames[i].frame.Seq > *to
		})
	}

	if start >= len(b.frames) || start >= end {
		return []protocol.ReplayFrame{}
	}

	frames := make([]protocol.ReplayFrame, 0, end-start)
	for _, entry := range b.frames[start:end] {
		frames = append(frames, entry.frame)
	}
	return frames
}

func (b *HistoryBuffer) LatestSeq() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.latestSeq
}
