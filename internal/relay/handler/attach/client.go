package attach

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/protocol"
	handlerws "yuanbohan/tunnel/internal/relay/handler/ws"
)

type outbound struct {
	control *protocol.AttachControlMessage
	binary  []byte
}

type wsAttachPeer struct {
	conn    wsConn
	tracker *handlerws.Tracker

	mu          sync.RWMutex
	closed      bool
	closeReason string
	outbound    chan outbound
	closeOnce   sync.Once
}

type wsConn interface {
	WriteMessage(messageType int, data []byte) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

func newWSAttachPeer(conn wsConn, tracker *handlerws.Tracker) *wsAttachPeer {
	bufferSize := config.RelayWSSinkBufferSize()
	if bufferSize <= 0 {
		bufferSize = 1
	}

	peer := &wsAttachPeer{
		conn:     conn,
		tracker:  tracker,
		outbound: make(chan outbound, bufferSize),
	}
	go peer.run()
	return peer
}

func (p *wsAttachPeer) SendControl(msg protocol.AttachControlMessage) error {
	return p.enqueue(outbound{control: &msg})
}

func (p *wsAttachPeer) SendBinary(payload []byte) error {
	return p.enqueue(outbound{binary: append([]byte(nil), payload...)})
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

func (p *wsAttachPeer) enqueue(msg outbound) error {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return handlerws.ErrSinkClosed
	}
	select {
	case p.outbound <- msg:
		p.mu.RUnlock()
		return nil
	default:
		p.mu.RUnlock()
		if p.tracker != nil {
			p.tracker.NoteDisconnectError(handlerws.ErrSinkBackpressure)
		}
		_ = p.Close("slow_client")
		return handlerws.ErrSinkBackpressure
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
	if err := p.write(outbound{control: &protocol.AttachControlMessage{
		Type:   "closing",
		Reason: reason,
	}}); err != nil && p.tracker != nil {
		p.tracker.NoteDisconnectError(err)
	}
}

func (p *wsAttachPeer) write(msg outbound) error {
	writeTimeout := config.RelayWSWriteTimeout()
	if writeTimeout > 0 {
		if err := p.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
			return err
		}
	}

	if msg.control != nil {
		payload, err := handlerws.WriteJSON(p.conn, *msg.control)
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
