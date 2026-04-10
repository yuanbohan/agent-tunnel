package relay

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/protocol"
)

type attachOutbound struct {
	control *protocol.AttachControlMessage
	binary  []byte
}

type wsAttachPeer struct {
	conn         wsConn
	tracker      *wsTrafficTracker
	writeTimeout time.Duration

	mu          sync.RWMutex
	closed      bool
	closeReason string
	outbound    chan attachOutbound
	closeOnce   sync.Once
}

func newWSAttachPeer(conn wsConn, tracker *wsTrafficTracker, bufferSize int, writeTimeout time.Duration) *wsAttachPeer {
	if bufferSize <= 0 {
		bufferSize = 1
	}

	peer := &wsAttachPeer{
		conn:         conn,
		tracker:      tracker,
		writeTimeout: writeTimeout,
		outbound:     make(chan attachOutbound, bufferSize),
	}
	go peer.run()
	return peer
}

func (p *wsAttachPeer) SendControl(msg protocol.AttachControlMessage) error {
	return p.enqueue(attachOutbound{control: &msg})
}

func (p *wsAttachPeer) SendBinary(payload []byte) error {
	return p.enqueue(attachOutbound{binary: append([]byte(nil), payload...)})
}

func (p *wsAttachPeer) Close(reason string) error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.closeReason = reason
		close(p.outbound)
		p.mu.Unlock()
	})
	return nil
}

func (p *wsAttachPeer) enqueue(msg attachOutbound) error {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return errWSSinkClosed
	}
	select {
	case p.outbound <- msg:
		p.mu.RUnlock()
		return nil
	default:
		p.mu.RUnlock()
		if p.tracker != nil {
			p.tracker.NoteDisconnectError(errWSSinkBackpressure)
		}
		_ = p.Close("slow_client")
		return errWSSinkBackpressure
	}
}

func (p *wsAttachPeer) run() {
	defer p.conn.Close()

	for msg := range p.outbound {
		if err := p.write(msg); err != nil {
			if p.tracker != nil {
				p.tracker.NoteDisconnectError(err)
			}
			return
		}
	}

	p.mu.RLock()
	reason := p.closeReason
	p.mu.RUnlock()
	if reason == "" {
		return
	}
	if err := p.write(attachOutbound{control: &protocol.AttachControlMessage{
		Type:   "closing",
		Reason: reason,
	}}); err != nil && p.tracker != nil {
		p.tracker.NoteDisconnectError(err)
	}
}

func (p *wsAttachPeer) write(msg attachOutbound) error {
	if p.writeTimeout > 0 {
		if err := p.conn.SetWriteDeadline(time.Now().Add(p.writeTimeout)); err != nil {
			return err
		}
	}

	if msg.control != nil {
		payload, err := writeWSJSON(p.conn, *msg.control)
		if err != nil {
			return err
		}
		if p.tracker != nil {
			p.tracker.RecordOutbound(len(payload))
		}
		return nil
	}

	if err := p.conn.WriteMessage(websocket.BinaryMessage, msg.binary); err != nil {
		return err
	}
	if p.tracker != nil {
		p.tracker.RecordOutbound(len(msg.binary))
	}
	return nil
}
