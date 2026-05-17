package agent

import (
	"errors"
	"sync"
	"testing"
	"time"

	relayconfig "yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/relay/session"
)

type mockWSConn struct {
	mu             sync.Mutex
	deadline       time.Time
	messages       [][]byte
	setDeadlineErr error
	writeErr       error
	closeErr       error
}

func (m *mockWSConn) WriteMessage(_ int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeErr != nil {
		return m.writeErr
	}
	m.messages = append(m.messages, append([]byte(nil), data...))
	return nil
}

func (m *mockWSConn) SetWriteDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setDeadlineErr != nil {
		return m.setDeadlineErr
	}
	m.deadline = t
	return nil
}

func (m *mockWSConn) Close() error { return m.closeErr }

func TestWSAgentPeerSendJSONSetsWriteDeadline(t *testing.T) {
	restore := relayconfig.UseRelayForTest(relayconfig.Relay{WSWriteTimeout: 5 * time.Second})
	defer restore()

	conn := &mockWSConn{}
	peer := &wsAgentPeer{
		conn:   conn,
		active: true,
	}

	if err := peer.SendJSON(protocol.LaunchReadyFrame(protocol.LaunchContext{})); err != nil {
		t.Fatalf("SendJSON returned error: %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.deadline.IsZero() {
		t.Fatal("SetWriteDeadline was not called")
	}
	if len(conn.messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(conn.messages))
	}
}

func TestWSAgentPeerSendJSONReturnsDeadlineError(t *testing.T) {
	restore := relayconfig.UseRelayForTest(relayconfig.Relay{WSWriteTimeout: 5 * time.Second})
	defer restore()

	conn := &mockWSConn{setDeadlineErr: errors.New("deadline failed")}
	peer := &wsAgentPeer{
		conn:   conn,
		active: true,
	}

	if err := peer.SendJSON(protocol.LaunchReadyFrame(protocol.LaunchContext{})); err == nil || err.Error() != "deadline failed" {
		t.Fatalf("SendJSON error = %v, want deadline failed", err)
	}
}

func TestWSAgentPeerRejectsSendsAfterDeactivate(t *testing.T) {
	restore := relayconfig.UseRelayForTest(relayconfig.Relay{WSWriteTimeout: 5 * time.Second})
	defer restore()

	conn := &mockWSConn{}
	peer := &wsAgentPeer{
		conn:   conn,
		active: true,
	}

	peer.Deactivate()

	if err := peer.SendJSON(protocol.LaunchReadyFrame(protocol.LaunchContext{})); !errors.Is(err, session.ErrAgentPeerInactive) {
		t.Fatalf("SendJSON error = %v, want errAgentPeerInactive", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.messages) != 0 {
		t.Fatalf("message count = %d, want 0", len(conn.messages))
	}
}
