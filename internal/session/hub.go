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

	mu    sync.RWMutex
	sinks map[string]OutputSink
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
	defer h.mu.RUnlock()
	for _, sink := range h.sinks {
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
	return h.resizePTY(cols, rows)
}
