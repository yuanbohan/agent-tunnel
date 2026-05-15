package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
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
	full, ok := broker.SnapshotBySession("sess-1")
	if !ok || len(full.LatestSnapshot) == 0 || full.SnapshotCols == 0 || full.SnapshotRows == 0 {
		t.Fatalf("snapshot = %#v, want terminal snapshot bytes and dimensions", full)
	}
}

func TestSessionRegistrationClientWaitUntilRegisteredReturnsAfterBrokerAck(t *testing.T) {
	paths := testPaths(t)
	broker, server, cancel := startBrokerForTest(t, paths)
	defer cancel()
	defer server.Close()

	client := NewSessionRegistrationClient(paths, protocol.SessionInfo{
		SessionID:      "sess-ack",
		CWD:            "/repo",
		CommandPreview: "codex",
		StartedAt:      1,
	})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	defer client.Close()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWait()
	if err := client.WaitUntilRegistered(waitCtx); err != nil {
		t.Fatalf("WaitUntilRegistered returned error: %v", err)
	}
	if _, ok := broker.SnapshotBySession("sess-ack"); !ok {
		t.Fatal("broker missing registered session after WaitUntilRegistered returned")
	}
}

func TestSessionRegistrationClientWaitUntilRegisteredReturnsLastPreAckError(t *testing.T) {
	client := NewSessionRegistrationClient(Paths{}, protocol.SessionInfo{SessionID: "sess-error"})
	client.sleep = func(context.Context, time.Duration) bool { return false }
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for client.lastRegistrationError() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if client.lastRegistrationError() == nil {
		t.Fatal("lastRegistrationError = nil, want broker setup error")
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelWait()
	err := client.WaitUntilRegistered(waitCtx)
	if err == nil || !strings.Contains(err.Error(), "broker socket path is empty") {
		t.Fatalf("WaitUntilRegistered error = %v, want broker socket setup error", err)
	}
}

func TestSessionRegistrationClientWaitUntilRegisteredTimesOut(t *testing.T) {
	client := NewSessionRegistrationClient(testPaths(t), protocol.SessionInfo{SessionID: "sess-timeout"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := client.WaitUntilRegistered(ctx)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for daemon broker") {
		t.Fatalf("WaitUntilRegistered error = %v, want timeout", err)
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

	snapshot, cols, rows := client.mirror.Snapshot()
	if cols != 120 || rows != 40 {
		t.Fatalf("mirror snapshot len=%d size=%dx%d, want resized 120x40 snapshot", len(snapshot), cols, rows)
	}
}

func TestSessionRegistrationClientRunsStopHandlerForMatchingSession(t *testing.T) {
	client := NewSessionRegistrationClient(testPaths(t), protocol.SessionInfo{SessionID: "sess-1"})
	stops := 0
	client.SetStopHandler(func() {
		stops++
	})

	client.handleBrokerFrame(BrokerFrame{Type: brokerFrameStopSession, SessionID: "sess-2"})
	if stops != 0 {
		t.Fatalf("stops = %d, want no stop for mismatched session", stops)
	}

	client.handleBrokerFrame(BrokerFrame{Type: brokerFrameStopSession, SessionID: "sess-1"})
	if stops != 1 {
		t.Fatalf("stops = %d, want one stop for matching session", stops)
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

func TestSessionRegistrationClientWriteOutputDoesNotBlockOnBrokerBackpressure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	client := NewSessionRegistrationClient(testPaths(t), protocol.SessionInfo{SessionID: "sess-1"})
	client.mu.Lock()
	client.conn = clientConn
	client.encoder = json.NewEncoder(clientConn)
	client.brokerWrites = make(chan BrokerFrame)
	client.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- client.WriteOutput([]byte("live output"))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteOutput returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WriteOutput blocked on broker backpressure")
	}

	_ = serverConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var buf [1]byte
	if _, err := serverConn.Read(buf[:]); err == nil {
		t.Fatal("server side stayed open after broker write overflow")
	}
}

func TestSessionRegistrationClientPublishesOutputBytesBrokerEvent(t *testing.T) {
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
	defer client.Close()
	waitForBrokerSnapshot(t, broker, 1)

	events, cancelEvents := broker.Subscribe()
	defer cancelEvents()
	if err := client.WriteOutput([]byte("live output")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == BrokerEventOutput {
				if event.SessionID != "sess-1" || string(event.Output) != "live output" {
					t.Fatalf("output event = %#v, want sess-1 live output", event)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for BrokerEventOutput from session registration client")
		}
	}
}

func TestSessionRegistrationClientDoesNotAttachSnapshotToEachOutputEvent(t *testing.T) {
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
	client.throttle = time.Hour
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	defer client.Close()
	waitForBrokerSnapshot(t, broker, 1)

	events, cancelEvents := broker.Subscribe()
	defer cancelEvents()
	before, ok := broker.SnapshotBySession("sess-1")
	if !ok {
		t.Fatal("broker snapshot missing sess-1 before output")
	}
	if err := client.WriteOutput([]byte("fresh output")); err != nil {
		t.Fatalf("WriteOutput returned error: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != BrokerEventOutput {
				continue
			}
			snapshot, ok := broker.SnapshotBySession("sess-1")
			if !ok {
				t.Fatal("broker snapshot missing sess-1")
			}
			if string(snapshot.LatestSnapshot) != string(before.LatestSnapshot) {
				t.Fatalf("snapshot changed with live output event: before %q after %q", string(before.LatestSnapshot), string(snapshot.LatestSnapshot))
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for BrokerEventOutput from session registration client")
		}
	}
}

func TestSessionRegistrationClientResizeUpdatesBrokerSnapshotDimensions(t *testing.T) {
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
	hub := tunnelsession.NewHub(func(data []byte) error { return nil }, func(cols, rows int) error { return nil })
	client.BindHub(hub)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go client.Run(ctx)
	defer client.Close()
	waitForBrokerSnapshot(t, broker, 1)

	interactiveOwner := &struct{ id int }{id: 1}
	if err := broker.GrantInteractive("sess-1", interactiveOwner); err != nil {
		t.Fatalf("GrantInteractive returned error: %v", err)
	}
	if err := broker.RouteResize("sess-1", interactiveOwner, 132, 43); err != nil {
		t.Fatalf("RouteResize returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := broker.Snapshot()
		if len(snapshot) == 1 && snapshot[0].SnapshotCols == 132 && snapshot[0].SnapshotRows == 43 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want resized broker snapshot dimensions 132x43", broker.Snapshot())
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
