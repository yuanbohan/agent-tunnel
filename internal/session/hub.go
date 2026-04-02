package session

import (
	"fmt"
	"sync"
)

type OutputSink interface {
	WriteOutput([]byte) error
}

type Hub struct {
	writeInput func([]byte) error
	resizePTY  func(int, int) error

	mu       sync.RWMutex
	sinks    map[string]OutputSink
	cols     int
	rows     int
	onResize func(int, int)
}

func NewHub(writeInput func([]byte) error, resizePTY func(int, int) error) *Hub {
	return &Hub{
		writeInput: writeInput,
		resizePTY:  resizePTY,
		sinks:      make(map[string]OutputSink),
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
	cp := append([]byte(nil), data...)
	return h.writeInput(cp)
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
	cb := h.onResize
	h.mu.Unlock()

	if cb != nil {
		cb(cols, rows)
	}
	return nil
}

func (h *Hub) CurrentSize() (int, int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cols, h.rows
}

func (h *Hub) OnResize(cb func(cols, rows int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onResize = cb
}
