package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	tunnelsession "yuanbohan/tunnel/internal/tunnel/session"
)

func TestBrokerServerRegistersSessionAndCachesLatestPreview(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	conn := dialBrokerForTest(t, paths.BrokerSocketPath)
	defer conn.Close()
	encoder := json.NewEncoder(conn)
	session := BrokerSession{
		SessionID:      "sess-1",
		Label:          "api-fix",
		CommandPreview: "codex --profile prod",
		CWD:            "/repo",
		GitBranch:      "main",
		StartedAt:      1_700_000_000,
		UpdatedAt:      1_700_000_001,
	}
	if err := encoder.Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &session}); err != nil {
		t.Fatalf("register Encode returned error: %v", err)
	}
	if err := encoder.Encode(BrokerFrame{Type: brokerFramePreviewUpdate, SessionID: "sess-1", Preview: "latest output", UpdatedAt: 1_700_000_002}); err != nil {
		t.Fatalf("preview Encode returned error: %v", err)
	}

	snapshot := waitForBrokerSnapshot(t, broker, 1)
	got := snapshot[0]
	if got.SessionID != "sess-1" || got.Label != "api-fix" || got.CommandPreview != "codex --profile prod" || got.CWD != "/repo" || got.GitBranch != "main" || got.StartedAt != 1_700_000_000 || got.UpdatedAt != 1_700_000_002 || !got.Online {
		t.Fatalf("snapshot = %#v, want registered session metadata", got)
	}
	if got.LatestPreview != "latest output" {
		t.Fatalf("LatestPreview = %q, want latest output", got.LatestPreview)
	}
}

func TestBrokerPublishesSessionAndPreviewEvents(t *testing.T) {
	broker := NewBroker()
	events, cancel := broker.Subscribe()
	defer cancel()
	owner := &brokerConnection{}

	session := BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 10}
	broker.register(session, owner)
	event := readBrokerEvent(t, events)
	if event.Type != BrokerEventSessionUpsert || event.SessionID != "sess-1" || event.Session.SessionID != "sess-1" {
		t.Fatalf("register event = %#v, want session_upsert sess-1", event)
	}

	broker.updatePreview("sess-1", "latest output", 11, owner)
	event = readBrokerEvent(t, events)
	if event.Type != BrokerEventPreview || event.SessionID != "sess-1" || event.Session.LatestPreview != "latest output" || event.Session.UpdatedAt != 11 {
		t.Fatalf("preview event = %#v, want preview update", event)
	}

	broker.remove("sess-1", owner)
	event = readBrokerEvent(t, events)
	if event.Type != BrokerEventSessionGone || event.SessionID != "sess-1" {
		t.Fatalf("remove event = %#v, want session_gone sess-1", event)
	}
}

func TestBrokerInteractiveOwnershipDeniesDuplicateOwners(t *testing.T) {
	broker := NewBroker()
	owner := &brokerConnection{}
	broker.register(BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 10}, owner)

	firstInteractive := &struct{ id int }{id: 1}
	secondInteractive := &struct{ id int }{id: 2}
	if err := broker.GrantInteractive("sess-1", firstInteractive); err != nil {
		t.Fatalf("first GrantInteractive returned error: %v", err)
	}
	if err := broker.GrantInteractive("sess-1", secondInteractive); !errors.Is(err, ErrBrokerInteractiveBusy) {
		t.Fatalf("second GrantInteractive err = %v, want ErrBrokerInteractiveBusy", err)
	}
	broker.ReleaseInteractive("sess-1", firstInteractive)
	if err := broker.GrantInteractive("sess-1", secondInteractive); err != nil {
		t.Fatalf("GrantInteractive after release returned error: %v", err)
	}
}

func TestBrokerCancelSubscribeDoesNotRaceEmitWithClosedChannel(t *testing.T) {
	broker := NewBroker()
	_, cancel := broker.Subscribe()
	cancel()
	for i := 0; i < 100; i++ {
		broker.emit(BrokerEvent{Type: BrokerEventSessionGone, SessionID: "sess-1"})
	}
}

func TestBrokerRoutesRemoteCommandsToSessionOwner(t *testing.T) {
	broker := NewBroker()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	owner := &brokerConnection{conn: serverConn}
	broker.register(BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 10}, owner)
	interactiveOwner := &struct{ id int }{id: 1}
	if err := broker.GrantInteractive("sess-1", interactiveOwner); err != nil {
		t.Fatalf("GrantInteractive returned error: %v", err)
	}

	routeErr := make(chan error, 1)
	go func() {
		routeErr <- broker.RouteInputText("sess-1", interactiveOwner, "echo hi", true)
	}()
	var got BrokerFrame
	if err := json.NewDecoder(clientConn).Decode(&got); err != nil {
		t.Fatalf("Decode input frame returned error: %v", err)
	}
	if got.Type != brokerFrameInputText || got.SessionID != "sess-1" || got.Text != "echo hi" || !got.Submit {
		t.Fatalf("input frame = %#v, want submitted input_text", got)
	}
	if err := <-routeErr; err != nil {
		t.Fatalf("RouteInputText returned error: %v", err)
	}

	go func() {
		routeErr <- broker.RouteResize("sess-1", interactiveOwner, 100, 30)
	}()
	if err := json.NewDecoder(clientConn).Decode(&got); err != nil {
		t.Fatalf("Decode resize frame returned error: %v", err)
	}
	if got.Type != brokerFrameResize || got.SessionID != "sess-1" || got.Cols != 100 || got.Rows != 30 {
		t.Fatalf("resize frame = %#v, want 100x30 resize", got)
	}
	if err := <-routeErr; err != nil {
		t.Fatalf("RouteResize returned error: %v", err)
	}
}

func TestBrokerRejectsStaleInteractiveOwnerAfterSessionReRegister(t *testing.T) {
	broker := NewBroker()
	firstClient, firstServer := net.Pipe()
	defer firstClient.Close()
	defer firstServer.Close()
	firstSessionOwner := &brokerConnection{conn: firstServer}
	broker.register(BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 10}, firstSessionOwner)
	staleInteractiveOwner := &struct{ id int }{id: 1}
	if err := broker.GrantInteractive("sess-1", staleInteractiveOwner); err != nil {
		t.Fatalf("GrantInteractive stale owner returned error: %v", err)
	}
	broker.remove("sess-1", firstSessionOwner)

	secondClient, secondServer := net.Pipe()
	defer secondClient.Close()
	defer secondServer.Close()
	secondSessionOwner := &brokerConnection{conn: secondServer}
	broker.register(BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 11}, secondSessionOwner)
	currentInteractiveOwner := &struct{ id int }{id: 2}
	if err := broker.GrantInteractive("sess-1", currentInteractiveOwner); err != nil {
		t.Fatalf("GrantInteractive current owner returned error: %v", err)
	}

	if err := broker.RouteInputText("sess-1", staleInteractiveOwner, "echo stale", true); !errors.Is(err, ErrBrokerInteractiveNotGranted) {
		t.Fatalf("stale RouteInputText err = %v, want ErrBrokerInteractiveNotGranted", err)
	}

	routeErr := make(chan error, 1)
	go func() {
		routeErr <- broker.RouteInputText("sess-1", currentInteractiveOwner, "echo current", true)
	}()
	var got BrokerFrame
	if err := json.NewDecoder(secondClient).Decode(&got); err != nil {
		t.Fatalf("Decode current frame returned error: %v", err)
	}
	if got.Type != brokerFrameInputText || got.Text != "echo current" {
		t.Fatalf("current frame = %#v, want current input", got)
	}
	if err := <-routeErr; err != nil {
		t.Fatalf("current RouteInputText returned error: %v", err)
	}
}

func TestBrokerServerRemovesSessionWhenConnectionCloses(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	conn := dialBrokerForTest(t, paths.BrokerSocketPath)
	session := BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 1}
	if err := json.NewEncoder(conn).Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &session}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	_ = conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(broker.Snapshot()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want session removed after connection close", broker.Snapshot())
}

func TestBrokerServerDuplicateRegistrationReplacesPreviousOwner(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	first := dialBrokerForTest(t, paths.BrokerSocketPath)
	defer first.Close()
	firstSession := BrokerSession{SessionID: "sess-1", Label: "old", CWD: "/repo", CommandPreview: "codex", StartedAt: 1}
	if err := json.NewEncoder(first).Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &firstSession}); err != nil {
		t.Fatalf("first Encode returned error: %v", err)
	}
	waitForBrokerSnapshot(t, broker, 1)

	second := dialBrokerForTest(t, paths.BrokerSocketPath)
	defer second.Close()
	secondSession := BrokerSession{SessionID: "sess-1", Label: "new", CWD: "/repo", CommandPreview: "claude", StartedAt: 2}
	if err := json.NewEncoder(second).Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &secondSession}); err != nil {
		t.Fatalf("second Encode returned error: %v", err)
	}

	var snapshot []BrokerSessionSnapshot
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot = broker.Snapshot()
		if len(snapshot) == 1 && snapshot[0].Label == "new" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(snapshot) != 1 {
		t.Fatalf("snapshot = %#v, want one replacement entry", snapshot)
	}
	if snapshot[0].Label != "new" || snapshot[0].CommandPreview != "claude" || snapshot[0].StartedAt != 2 {
		t.Fatalf("snapshot = %#v, want replacement metadata", snapshot[0])
	}
}

func TestBrokerServerReregisterOnSameConnectionRemovesOldSession(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	conn := dialBrokerForTest(t, paths.BrokerSocketPath)
	defer conn.Close()
	encoder := json.NewEncoder(conn)
	firstSession := BrokerSession{SessionID: "sess-1", Label: "old", CWD: "/repo", CommandPreview: "codex", StartedAt: 1}
	if err := encoder.Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &firstSession}); err != nil {
		t.Fatalf("first Encode returned error: %v", err)
	}
	waitForBrokerSnapshot(t, broker, 1)

	secondSession := BrokerSession{SessionID: "sess-2", Label: "new", CWD: "/repo", CommandPreview: "claude", StartedAt: 2}
	if err := encoder.Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &secondSession}); err != nil {
		t.Fatalf("second Encode returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := broker.Snapshot()
		if len(snapshot) == 1 && snapshot[0].SessionID == "sess-2" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want old session removed after same-connection reregister", broker.Snapshot())
}

func TestBrokerServerBlankReregisterDoesNotOrphanPreviousSession(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	conn := dialBrokerForTest(t, paths.BrokerSocketPath)
	encoder := json.NewEncoder(conn)
	session := BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 1}
	if err := encoder.Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &session}); err != nil {
		t.Fatalf("register Encode returned error: %v", err)
	}
	waitForBrokerSnapshot(t, broker, 1)
	blankSession := BrokerSession{SessionID: "   ", CWD: "/repo", CommandPreview: "codex", StartedAt: 1}
	if err := encoder.Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &blankSession}); err != nil {
		t.Fatalf("blank register Encode returned error: %v", err)
	}
	_ = conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(broker.Snapshot()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want previous session removed after connection close", broker.Snapshot())
}

func TestBrokerServerIgnoresUnknownFramesBeforeRegistration(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	conn := dialBrokerForTest(t, paths.BrokerSocketPath)
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(BrokerFrame{Type: brokerFramePreviewUpdate, SessionID: "missing", Preview: "secret"}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if snapshot := broker.Snapshot(); len(snapshot) != 0 {
		t.Fatalf("snapshot = %#v, want no roster entries", snapshot)
	}
}

func TestBrokerServerBoundsIncomingPreview(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	broker := NewBroker()
	server, err := NewBrokerServer(paths.BrokerSocketPath, broker)
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	conn := dialBrokerForTest(t, paths.BrokerSocketPath)
	defer conn.Close()
	encoder := json.NewEncoder(conn)
	session := BrokerSession{SessionID: "sess-1", CWD: "/repo", CommandPreview: "codex", StartedAt: 1}
	if err := encoder.Encode(BrokerFrame{Type: brokerFrameRegisterSession, Session: &session}); err != nil {
		t.Fatalf("register Encode returned error: %v", err)
	}
	oversized := strings.Repeat("x", tunnelsession.DefaultPreviewMaxChars+500)
	if err := encoder.Encode(BrokerFrame{Type: brokerFramePreviewUpdate, SessionID: "sess-1", Preview: oversized}); err != nil {
		t.Fatalf("preview Encode returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := broker.Snapshot()
		if len(snapshot) == 1 && snapshot[0].LatestPreview != "" {
			if got := len([]rune(snapshot[0].LatestPreview)); got > tunnelsession.DefaultPreviewMaxChars {
				t.Fatalf("LatestPreview length = %d, want <= %d", got, tunnelsession.DefaultPreviewMaxChars)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want bounded latest preview", broker.Snapshot())
}

func TestBrokerServerClosesConnectionsThatNeverRegister(t *testing.T) {
	oldTimeout := brokerRegistrationTimeout
	brokerRegistrationTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		brokerRegistrationTimeout = oldTimeout
	})

	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	server, err := NewBrokerServer(paths.BrokerSocketPath, NewBroker())
	if err != nil {
		t.Fatalf("NewBrokerServer returned error: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.Serve(ctx)
	}()

	conn := dialBrokerForTest(t, paths.BrokerSocketPath)
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	buffer := make([]byte, 1)
	_, err = conn.Read(buffer)
	if err == nil {
		t.Fatal("Read error = nil, want connection close")
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("Read error = %v, want server-side close before client deadline", err)
	}
}

func TestNewBrokerServerRejectsNonSocketPath(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	if err := os.WriteFile(paths.BrokerSocketPath, []byte("not-a-socket"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	_, err := NewBrokerServer(paths.BrokerSocketPath, NewBroker())
	if err == nil {
		t.Fatal("NewBrokerServer error = nil, want non-socket path failure")
	}
	if !strings.Contains(err.Error(), "not a unix socket") {
		t.Fatalf("error = %q, want non-socket path guidance", err)
	}
	payload, readErr := os.ReadFile(paths.BrokerSocketPath)
	if readErr != nil {
		t.Fatalf("ReadFile returned error: %v", readErr)
	}
	if string(payload) != "not-a-socket" {
		t.Fatalf("payload = %q, want original file preserved", string(payload))
	}
}

func dialBrokerForTest(t *testing.T, socketPath string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			return conn
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Dial(%s) failed: %v", socketPath, lastErr)
	return nil
}

func waitForBrokerSnapshot(t *testing.T, broker *Broker, want int) []BrokerSessionSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var snapshot []BrokerSessionSnapshot
	for time.Now().Before(deadline) {
		snapshot = broker.Snapshot()
		if len(snapshot) == want {
			return snapshot
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot = %#v, want %d entries", snapshot, want)
	return nil
}

func readBrokerEvent(t *testing.T, events <-chan BrokerEvent) BrokerEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broker event")
		return BrokerEvent{}
	}
}

func TestVerifyBrokerSocketRejectsUnsafePermissions(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	listener, err := net.Listen("unix", paths.BrokerSocketPath)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()
	if err := os.Chmod(paths.BrokerSocketPath, 0o666); err != nil {
		t.Fatalf("Chmod returned error: %v", err)
	}
	if err := verifyBrokerSocket(paths.BrokerSocketPath); err == nil {
		t.Fatal("verifyBrokerSocket error = nil, want owner-only permission rejection")
	}
}

func TestVerifyBrokerSocketRejectsNonSocket(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	if err := os.WriteFile(paths.BrokerSocketPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := verifyBrokerSocket(paths.BrokerSocketPath); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verifyBrokerSocket error = %v, want non-socket rejection", err)
	}
}
