package relay

import "yuanbohan/tunnel/protocol"

type clientUpdateSink interface {
	WriteClientUpdate(protocol.ClientUpdateMessage) error
	Close() error
}

func (r *Registry) AddUpdateSink(sinkID string, sink clientUpdateSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateSinks[sinkID] = sink
}

func (r *Registry) RemoveUpdateSink(sinkID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.updateSinks, sinkID)
}

func (r *Registry) broadcastClientUpdate(msg protocol.ClientUpdateMessage) {
	r.mu.RLock()
	sinks := make([]clientUpdateSink, 0, len(r.updateSinks))
	for _, sink := range r.updateSinks {
		sinks = append(sinks, sink)
	}
	r.mu.RUnlock()

	for _, sink := range sinks {
		_ = sink.WriteClientUpdate(msg)
	}
}
