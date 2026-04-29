package session

import (
	"fmt"
	"sync"
	"time"
)

type OutputSink interface {
	WriteOutput([]byte) error
}

type Hub struct {
	writeInput func([]byte) error
	resizePTY  func(int, int) error
	sleep      func(time.Duration)

	writeMu sync.Mutex
	mu      sync.RWMutex
	sinks   map[string]OutputSink
	cols    int
	rows    int
	resizes map[string]func(int, int)
}

func NewHub(writeInput func([]byte) error, resizePTY func(int, int) error) *Hub {
	return &Hub{
		writeInput: writeInput,
		resizePTY:  resizePTY,
		sleep:      time.Sleep,
		sinks:      make(map[string]OutputSink),
		resizes:    make(map[string]func(int, int)),
	}
}

func (h *Hub) AddSink(id string, sink OutputSink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sinks[id] = sink
}

func (h *Hub) RemoveSink(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sinks, id)
}

func (h *Hub) BroadcastOutput(data []byte) {
	h.mu.RLock()
	sinks := make([]OutputSink, 0, len(h.sinks))
	for _, sink := range h.sinks {
		sinks = append(sinks, sink)
	}
	h.mu.RUnlock()

	for _, sink := range sinks {
		cp := append([]byte(nil), data...)
		_ = sink.WriteOutput(cp)
	}
}

func (h *Hub) WriteInput(data []byte) error {
	return h.WriteInputSequence(data)
}

func (h *Hub) WriteInputSequence(chunks ...[]byte) error {
	return h.WriteInputSequenceWithGap(0, chunks...)
}

func (h *Hub) WriteInputSequenceWithGap(gap time.Duration, chunks ...[]byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	for i, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		cp := append([]byte(nil), chunk...)
		err := h.writeInput(cp)
		if err != nil {
			return err
		}
		if gap > 0 && hasNonEmptyChunk(chunks[i+1:]) {
			h.sleep(gap)
		}
	}
	return nil
}

func hasNonEmptyChunk(chunks [][]byte) bool {
	for _, chunk := range chunks {
		if len(chunk) > 0 {
			return true
		}
	}
	return false
}

func (h *Hub) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("invalid resize %dx%d", cols, rows)
	}
	if err := h.resizePTY(cols, rows); err != nil {
		return err
	}

	h.mu.Lock()
	h.cols = cols
	h.rows = rows
	callbacks := make([]func(int, int), 0, len(h.resizes))
	for _, cb := range h.resizes {
		callbacks = append(callbacks, cb)
	}
	h.mu.Unlock()

	for _, cb := range callbacks {
		cb(cols, rows)
	}
	return nil
}

func (h *Hub) CurrentSize() (int, int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cols, h.rows
}

func (h *Hub) AddResizeListener(id string, cb func(cols, rows int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cb == nil {
		delete(h.resizes, id)
		return
	}
	h.resizes[id] = cb
}

func (h *Hub) RemoveResizeListener(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.resizes, id)
}
