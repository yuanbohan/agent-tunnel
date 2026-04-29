package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"yuanbohan/tunnel/internal/protocol"
	tunnelsession "yuanbohan/tunnel/internal/tunnel/session"
)

func TestSessionRegistrationClientRegistersAndPushesPreview(t *testing.T) {
	paths := testPaths(t)
	broker, server, cancel := startBrokerForTest(t, paths)
	defer cancel()
	defer server.Close()

	client := NewSessionRegistrationClient(paths, protocol.SessionInfo{
		SessionID:      "sess-1",
		DeviceID:       "dev-1",
		Launcher:       "codex",
		Label:          "api-fix",
		CWD:            "/repo",
		CommandPreview: "codex --profile prod",
		GitBranch:      "main",
		StartedAt:      1_700_000_000,
		PlatformFamily: "macos",
		PlatformID:     "macos",
		ComputerName:   "laptop",
		LaunchSource:   protocol.SessionLaunchSourceLocal,
	})
	client.throttle = 0
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	defer client.Close()

	if err := client.WriteOutput([]byte("\x1b[31mhello from terminal\x1b[0m\r\n")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	snapshot := waitForBrokerSnapshot(t, broker, 1)
	deadline := time.Now().Add(2 * time.Second)
	for snapshot[0].LatestPreview != "hello from terminal" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		snapshot = broker.Snapshot()
	}
	got := snapshot[0]
	if got.SessionID != "sess-1" || got.DeviceID != "dev-1" || got.Label != "api-fix" || got.CommandPreview != "codex --profile prod" || got.LatestPreview != "hello from terminal" {
		t.Fatalf("snapshot = %#v, want registered session with preview", got)
	}
}

func TestSessionRegistrationClientRoutesBrokerCommandsToHub(t *testing.T) {
	client := NewSessionRegistrationClient(testPaths(t), protocol.SessionInfo{SessionID: "sess-1"})
	var inputs [][]byte
	var resize [2]int
	hub := tunnelsession.NewHub(func(data []byte) error {
		inputs = append(inputs, append([]byte(nil), data...))
		return nil
	}, func(cols, rows int) error {
		resize = [2]int{cols, rows}
		return nil
	})

	client.handleBrokerFrame(BrokerFrame{Type: brokerFrameInputText, SessionID: "sess-1", Text: "echo hi", Submit: true})
	if len(inputs) != 0 {
		t.Fatalf("inputs = %#v, want queued command before BindHub", inputs)
	}
	client.BindHub(hub)
	if len(inputs) != 2 || string(inputs[0]) != "echo hi" || string(inputs[1]) != "\r" {
		t.Fatalf("inputs after BindHub = %#v, want submitted text chunks", inputs)
	}

	client.handleBrokerFrame(BrokerFrame{Type: brokerFrameInputKey, SessionID: "sess-1", Key: "TAB"})
	if len(inputs) != 3 || string(inputs[2]) != "\t" {
		t.Fatalf("inputs after key = %#v, want tab", inputs)
	}

	client.handleBrokerFrame(BrokerFrame{Type: brokerFrameResize, SessionID: "sess-1", Cols: 120, Rows: 40})
	if resize != [2]int{120, 40} {
		t.Fatalf("resize = %v, want 120x40", resize)
	}
}

func TestSessionRegistrationClientCloseSendsSessionGone(t *testing.T) {
	paths := testPaths(t)
	broker, server, cancel := startBrokerForTest(t, paths)
	defer cancel()
	defer server.Close()

	client := NewSessionRegistrationClient(paths, protocol.SessionInfo{
		SessionID:      "sess-1",
		CWD:            "/repo",
		CommandPreview: "codex",
		StartedAt:      1,
	})
	client.throttle = 0
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	waitForBrokerSnapshot(t, broker, 1)

	if err := client.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(broker.Snapshot()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want session removed after Close", broker.Snapshot())
}

func TestSessionRegistrationClientCloseUsesWriteDeadline(t *testing.T) {
	oldTimeout := brokerWriteTimeout
	brokerWriteTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		brokerWriteTimeout = oldTimeout
	})

	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	client := NewSessionRegistrationClient(testPaths(t), protocol.SessionInfo{SessionID: "sess-1"})
	client.mu.Lock()
	client.conn = clientConn
	client.encoder = json.NewEncoder(clientConn)
	client.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- client.Close()
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close timed out, want broker write deadline to unblock shutdown")
	}
}

func TestSessionRegistrationClientSkipsBrokerWhenDaemonBaseURLDiffers(t *testing.T) {
	paths := testPaths(t)
	broker, server, cancel := startBrokerForTest(t, paths)
	defer cancel()
	defer server.Close()

	client := NewSessionRegistrationClient(paths, protocol.SessionInfo{
		SessionID:      "sess-1",
		CWD:            "/repo",
		CommandPreview: "codex",
		StartedAt:      1,
	})
	client.SetExpectedBaseURL("https://relay-a.example.com")
	client.daemonStatus = func(context.Context, Paths) (StatusInfo, error) {
		return StatusInfo{Running: true, BaseURL: "https://relay-b.example.com"}, nil
	}
	client.sleep = func(ctx context.Context, d time.Duration) bool {
		timer := time.NewTimer(10 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	defer client.Close()

	if err := client.WriteOutput([]byte("wrong daemon\n")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if snapshot := broker.Snapshot(); len(snapshot) != 0 {
		t.Fatalf("snapshot = %#v, want no registration with mismatched daemon base URL", snapshot)
	}
}

func TestSessionRegistrationClientSkipsBrokerWhenDaemonAuthContextDiffers(t *testing.T) {
	paths := testPaths(t)
	broker, server, cancel := startBrokerForTest(t, paths)
	defer cancel()
	defer server.Close()

	client := NewSessionRegistrationClient(paths, protocol.SessionInfo{
		SessionID:      "sess-1",
		CWD:            "/repo",
		CommandPreview: "codex",
		StartedAt:      1,
	})
	client.SetExpectedDaemonContext("https://relay.example.com", "token-a")
	client.daemonStatus = func(context.Context, Paths) (StatusInfo, error) {
		return StatusInfo{
			Running:                true,
			BaseURL:                "https://relay.example.com",
			AuthContextFingerprint: AuthContextFingerprint("token-b"),
		}, nil
	}
	client.sleep = func(ctx context.Context, d time.Duration) bool {
		timer := time.NewTimer(10 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	defer client.Close()

	if err := client.WriteOutput([]byte("wrong auth\n")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if snapshot := broker.Snapshot(); len(snapshot) != 0 {
		t.Fatalf("snapshot = %#v, want no registration with mismatched daemon auth context", snapshot)
	}
}

func TestSessionRegistrationClientThrottleDoesNotStarveContinuousPreview(t *testing.T) {
	paths := testPaths(t)
	broker, server, cancel := startBrokerForTest(t, paths)
	defer cancel()
	defer server.Close()

	client := NewSessionRegistrationClient(paths, protocol.SessionInfo{
		SessionID:      "sess-1",
		CWD:            "/repo",
		CommandPreview: "codex",
		StartedAt:      1,
	})
	client.throttle = 80 * time.Millisecond
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	defer client.Close()
	waitForBrokerSnapshot(t, broker, 1)

	if err := client.WriteOutput([]byte("a")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}
	waitForBrokerPreview(t, broker, "a")

	start := time.Now()
	for _, chunk := range []byte{'b', 'c', 'd', 'e'} {
		if err := client.WriteOutput([]byte{chunk}); err != nil {
			t.Fatalf("WriteOutput(%q) returned error: %v", chunk, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if delay := start.Add(130 * time.Millisecond).Sub(time.Now()); delay > 0 {
		time.Sleep(delay)
	}

	snapshot := broker.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot = %#v, want one registered session", snapshot)
	}
	if snapshot[0].LatestPreview == "a" {
		t.Fatalf("LatestPreview = %q, want throttled preview update before output becomes quiet", snapshot[0].LatestPreview)
	}
}

func TestSessionRegistrationClientRetriesAfterBrokerRestart(t *testing.T) {
	paths := testPaths(t)
	broker, server, cancel := startBrokerForTest(t, paths)

	client := NewSessionRegistrationClient(paths, protocol.SessionInfo{
		SessionID:      "sess-1",
		CWD:            "/repo",
		CommandPreview: "codex",
		StartedAt:      1,
	})
	client.throttle = 0
	client.sleep = func(ctx context.Context, d time.Duration) bool {
		timer := time.NewTimer(10 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	waitForBrokerSnapshot(t, broker, 1)

	cancel()
	_ = server.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(broker.Snapshot()) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	restartedBroker, restartedServer, restartedCancel := startBrokerForTest(t, paths)
	defer restartedCancel()
	defer restartedServer.Close()
	if err := client.WriteOutput([]byte("after restart\n")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	snapshot := waitForBrokerSnapshot(t, restartedBroker, 1)
	deadline = time.Now().Add(2 * time.Second)
	for snapshot[0].LatestPreview != "after restart" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		snapshot = restartedBroker.Snapshot()
	}
	if snapshot[0].LatestPreview != "after restart" {
		t.Fatalf("snapshot = %#v, want latest preview after reconnect", snapshot)
	}
}

func startBrokerForTest(t *testing.T, paths Paths) (*Broker, *BrokerServer, context.CancelFunc) {
	t.Helper()
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = server.Serve(ctx)
	}()
	return broker, server, cancel
}

func waitForBrokerPreview(t *testing.T, broker *Broker, want string) BrokerSessionSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var snapshot []BrokerSessionSnapshot
	for time.Now().Before(deadline) {
		snapshot = broker.Snapshot()
		if len(snapshot) == 1 && snapshot[0].LatestPreview == want {
			return snapshot[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want latest preview %q", snapshot, want)
	return BrokerSessionSnapshot{}
}
