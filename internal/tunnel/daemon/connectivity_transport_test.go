package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/carrier"
	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/identity"
	"yuanbohan/tunnel/internal/connectivity/sessionproto"
	conntransport "yuanbohan/tunnel/internal/connectivity/transport"
)

const (
	transportTestTrustedFingerprint   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	transportTestUntrustedFingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	transportTestDaemonFingerprint    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestConnectivityTransportSendsSessionIndexAndPreviewSnapshots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewBroker()
	owner := &brokerConnection{}
	broker.register(BrokerSession{
		SessionID:      "sess-before",
		Label:          "API fix",
		CommandPreview: "codex",
		CWD:            "/repo",
		GitBranch:      "main",
		StartedAt:      100,
		UpdatedAt:      101,
	}, owner)
	broker.updatePreview("sess-before", "cached preview", 102, owner)
	broker.updateSnapshot("sess-before", []byte("full terminal snapshot"), 100, 30, owner)
	broker.register(BrokerSession{
		SessionID:      "sess-2",
		Label:          "Release notes",
		CommandPreview: "vim",
		CWD:            "/repo/docs",
		GitBranch:      "docs/connectivity",
		StartedAt:      110,
		UpdatedAt:      111,
	}, owner)
	broker.updatePreview("sess-2", "second cached preview", 112, owner)

	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, broker, transportTestTrustedFingerprint)
	defer clientConn.CloseWithError(0, "done")
	control, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		ClientFingerprint: transportTestTrustedFingerprint,
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write hello returned error: %v", err)
	}

	hello := readTestJSONFrame[sessionproto.Hello](t, control, frame.TypeHello)
	if hello.ActorType != sessionproto.ActorDaemon || hello.ClientFingerprint != transportTestDaemonFingerprint || hello.PathKind != sessionproto.PathRelay {
		t.Fatalf("hello = %#v, want daemon relay hello", hello)
	}
	index := readTestJSONFrame[sessionproto.SessionIndex](t, control, frame.TypeSessionIndex)
	sessionsByID := sessionMetadataByID(t, index.Sessions)
	if len(sessionsByID) != 2 || sessionsByID["sess-2"].CommandPreview != "vim" {
		t.Fatalf("session index = %#v, want sess-before and sess-2 metadata", index)
	}
	if got := sessionsByID["sess-before"]; got.SessionID != "sess-before" || got.Label != "API fix" || got.CWD != "/repo" || got.CommandPreview != "codex" || got.GitBranch != "main" || got.StartedAt != 100 || got.UpdatedAt != 102 || !got.Online {
		t.Fatalf("session index metadata = %#v, want broker-known sess-before", got)
	}
	pathState := readTestJSONFrame[sessionproto.PathState](t, control, frame.TypePathState)
	if pathState.PathKind != sessionproto.PathRelay || pathState.AttemptID != "attempt-test" || pathState.FallbackReason != "direct_timeout" || pathState.DirectSetupLatencyMS != 3000 || pathState.RelaySetupLatencyMS != 120 {
		t.Fatalf("pathState = %#v, want relay attempt diagnostics", pathState)
	}

	if err := frame.Write(control, frame.Frame{Type: 0xfe, Payload: []byte("future frame")}); err != nil {
		t.Fatalf("write unknown frame returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypePreviewSubscribe, sessionproto.PreviewSubscribe{SessionID: "sess-before"}); err != nil {
		t.Fatalf("write preview_subscribe returned error: %v", err)
	}
	preview := readTestJSONFrame[sessionproto.PreviewSnapshot](t, control, frame.TypePreviewSnapshot)
	if preview.SessionID != "sess-before" || preview.Preview != "cached preview" || preview.UpdatedAt != 102 {
		t.Fatalf("preview = %#v, want cached preview", preview)
	}
	if err := writeTestJSONFrame(control, frame.TypePreviewSubscribe, sessionproto.PreviewSubscribe{SessionID: "sess-2"}); err != nil {
		t.Fatalf("write second preview_subscribe returned error: %v", err)
	}
	preview = readTestJSONFrame[sessionproto.PreviewSnapshot](t, control, frame.TypePreviewSnapshot)
	if preview.SessionID != "sess-2" || preview.Preview != "second cached preview" || preview.UpdatedAt != 112 {
		t.Fatalf("second preview = %#v, want second cached preview", preview)
	}

	broker.updatePreview("sess-before", "new preview", 103, owner)
	preview = readTestJSONFrame[sessionproto.PreviewSnapshot](t, control, frame.TypePreviewSnapshot)
	if preview.Preview != "new preview" || preview.UpdatedAt != 103 {
		t.Fatalf("preview = %#v, want updated preview", preview)
	}

	broker.updateSession(BrokerSession{SessionID: "sess-before", Label: "Renamed", CWD: "/repo", CommandPreview: "claude", StartedAt: 100, UpdatedAt: 104}, owner)
	upsert := readTestJSONFrame[sessionproto.SessionUpsert](t, control, frame.TypeSessionUpsert)
	if upsert.Session.SessionID != "sess-before" || upsert.Session.Label != "Renamed" || upsert.Session.CommandPreview != "claude" {
		t.Fatalf("upsert = %#v, want replacement metadata", upsert)
	}

	broker.register(BrokerSession{
		SessionID:      "sess-after",
		Label:          "Mobile launch",
		CommandPreview: "codex --profile prod",
		CWD:            "/repo/mobile",
		GitBranch:      "feature/mobile",
		StartedAt:      120,
		UpdatedAt:      121,
	}, owner)
	upsert = readTestJSONFrame[sessionproto.SessionUpsert](t, control, frame.TypeSessionUpsert)
	if got := upsert.Session; got.SessionID != "sess-after" || got.Label != "Mobile launch" || got.CWD != "/repo/mobile" || got.CommandPreview != "codex --profile prod" || got.GitBranch != "feature/mobile" || got.StartedAt != 120 || got.UpdatedAt != 121 || !got.Online {
		t.Fatalf("post-connect register upsert = %#v, want broker-known sess-after", upsert)
	}

	broker.remove("sess-before", owner)
	gone := readTestJSONFrame[sessionproto.SessionGone](t, control, frame.TypeSessionGone)
	if gone.SessionID != "sess-before" {
		t.Fatalf("gone = %#v, want sess-before", gone)
	}

	clientConn.CloseWithError(0, "done")
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for server")
	}
}

func TestConnectivityTransportRejectsUntrustedDeviceBeforeSessionIndex(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, NewBroker(), transportTestTrustedFingerprint)
	defer clientConn.CloseWithError(0, "done")
	control, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		ClientFingerprint: transportTestUntrustedFingerprint,
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write hello returned error: %v", err)
	}
	got := readTestJSONFrame[sessionproto.Error](t, control, frame.TypeError)
	if got.Code != "device_not_trusted" {
		t.Fatalf("error = %#v, want device_not_trusted", got)
	}
	if err := <-serverErr; err != ErrConnectivityDeviceUntrusted {
		t.Fatalf("server err = %v, want ErrConnectivityDeviceUntrusted", err)
	}
}

func TestConnectivityTransportRejectsLegacySessionProtocolBeforeTrustCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, NewBroker(), transportTestTrustedFingerprint)
	defer clientConn.CloseWithError(0, "done")
	control, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypeHello, map[string]any{
		"protocol_version":   sessionproto.ProtocolVersion - 1,
		"actor_type":         sessionproto.ActorMobile,
		"device_fingerprint": transportTestTrustedFingerprint,
		"path_kind":          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write legacy hello returned error: %v", err)
	}
	got := readTestJSONFrame[sessionproto.Error](t, control, frame.TypeError)
	if got.Code != "protocol_version_mismatch" {
		t.Fatalf("error = %#v, want protocol_version_mismatch", got)
	}
	if err := <-serverErr; err != ErrConnectivityProtocolVersion {
		t.Fatalf("server err = %v, want ErrConnectivityProtocolVersion", err)
	}
}

func TestConnectivityTransportServeStopsWhenContextCancelsDuringHandshake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, NewBroker(), transportTestTrustedFingerprint)
	defer clientConn.CloseWithError(0, "done")
	if _, err := clientConn.OpenStreamSync(ctx); err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}

	cancel()
	select {
	case <-serverErr:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Serve to stop after context cancellation")
	}
}

func TestConnectivityTransportJSONHelpersSetDeadlines(t *testing.T) {
	var writer deadlineBuffer
	if err := writeConnectivityJSON(&writer, frame.TypeHello, sessionproto.Hello{ProtocolVersion: sessionproto.ProtocolVersion}); err != nil {
		t.Fatalf("writeConnectivityJSON returned error: %v", err)
	}
	if writer.writeDeadlineSet.IsZero() {
		t.Fatal("write deadline was not set")
	}
	if !writer.writeDeadlineCleared {
		t.Fatal("write deadline was not cleared after the frame write")
	}

	var reader deadlineBuffer
	if err := writeTestJSONFrame(&reader, frame.TypeHello, sessionproto.Hello{ProtocolVersion: sessionproto.ProtocolVersion}); err != nil {
		t.Fatalf("write test frame returned error: %v", err)
	}
	if _, err := readConnectivityJSON[sessionproto.Hello](&reader, frame.TypeHello, frame.DefaultMaxPayload); err != nil {
		t.Fatalf("readConnectivityJSON returned error: %v", err)
	}
	if reader.readDeadlineSet.IsZero() {
		t.Fatal("read deadline was not set")
	}
	if !reader.readDeadlineCleared {
		t.Fatal("read deadline was not cleared after the frame read")
	}
}

func TestConnectivityTransportChunksLargeInitialSnapshot(t *testing.T) {
	broker := NewBroker()
	owner := &brokerConnection{}
	broker.register(BrokerSession{SessionID: "sess-1", CommandPreview: "codex", CWD: "/repo", StartedAt: 100}, owner)
	snapshot := bytes.Repeat([]byte("x"), frame.DefaultMaxPayload+17)
	broker.updateSnapshot("sess-1", snapshot, 132, 43, owner)

	var out bytes.Buffer
	transport := &ConnectivityTransport{Broker: broker}
	request := sessionproto.InteractiveRequest{
		SessionID: "sess-1",
		Cols:      100,
		Rows:      30,
	}
	initial, ok, cols, rows := transport.initialInteractiveSnapshot(request)
	if err := transport.writeInitialInteractiveSnapshot(&out, request, initial, ok, cols, rows); err != nil {
		t.Fatalf("writeInitialInteractiveSnapshot returned error: %v", err)
	}
	begin := readTestJSONFrame[sessionproto.SnapshotBegin](t, &out, frame.TypeSnapshotBegin)
	if begin.Cols != 132 || begin.Rows != 43 {
		t.Fatalf("snapshot_begin = %#v, want cached 132x43 dimensions", begin)
	}
	first := readTestRawFrame(t, &out, frame.TypeSnapshotChunk)
	second := readTestRawFrame(t, &out, frame.TypeSnapshotChunk)
	if len(first) != frame.DefaultMaxPayload || len(second) != 17 {
		t.Fatalf("chunk lengths = %d,%d, want %d,17", len(first), len(second), frame.DefaultMaxPayload)
	}
	end := readTestJSONFrame[sessionproto.SnapshotEnd](t, &out, frame.TypeSnapshotEnd)
	if end.ChunkCount != 2 {
		t.Fatalf("snapshot_end = %#v, want two chunks", end)
	}
	if got := append(first, second...); !bytes.Equal(got, snapshot) {
		t.Fatal("snapshot chunks did not reconstruct original snapshot")
	}
}

func TestConnectivityTransportInitialSnapshotFallsBackToRequestedDimensionsWithoutCache(t *testing.T) {
	broker := NewBroker()
	owner := &brokerConnection{}
	broker.register(BrokerSession{SessionID: "sess-1", CommandPreview: "codex", CWD: "/repo", StartedAt: 100}, owner)

	var out bytes.Buffer
	transport := &ConnectivityTransport{Broker: broker}
	request := sessionproto.InteractiveRequest{
		SessionID: "sess-1",
		Cols:      100,
		Rows:      30,
	}
	initial, ok, cols, rows := transport.initialInteractiveSnapshot(request)
	if err := transport.writeInitialInteractiveSnapshot(&out, request, initial, ok, cols, rows); err != nil {
		t.Fatalf("writeInitialInteractiveSnapshot returned error: %v", err)
	}
	begin := readTestJSONFrame[sessionproto.SnapshotBegin](t, &out, frame.TypeSnapshotBegin)
	if begin.Cols != 100 || begin.Rows != 30 {
		t.Fatalf("snapshot_begin = %#v, want requested 100x30 dimensions", begin)
	}
	end := readTestJSONFrame[sessionproto.SnapshotEnd](t, &out, frame.TypeSnapshotEnd)
	if end.ChunkCount != 0 {
		t.Fatalf("snapshot_end = %#v, want zero chunks", end)
	}
}

func TestConnectivityTransportDropsOnlySlowInteractiveStreamQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker := NewBroker()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	sessionOwner := &brokerConnection{conn: serverConn}
	broker.register(BrokerSession{SessionID: "sess-1", CommandPreview: "codex", CWD: "/repo", StartedAt: 100}, sessionOwner)
	interactiveOwner := &struct{}{}
	if err := broker.GrantInteractive("sess-1", interactiveOwner); err != nil {
		t.Fatalf("GrantInteractive returned error: %v", err)
	}
	state := newInteractiveStreamState(&deadlineBuffer{})
	for i := 0; i < cap(state.queue); i++ {
		state.queue <- []byte("queued")
	}
	streams := map[string]*interactiveStreamState{"sess-1": state}
	events := make(chan BrokerEvent, 2)
	var control deadlineBuffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- (&ConnectivityTransport{Broker: broker}).writeBrokerEvents(
			ctx,
			&control,
			&sync.Mutex{},
			&sync.RWMutex{},
			events,
			map[string]struct{}{},
			interactiveOwner,
			&sync.RWMutex{},
			streams,
		)
	}()

	events <- BrokerEvent{Type: BrokerEventOutput, SessionID: "sess-1", Output: []byte("overflow")}
	events <- BrokerEvent{Type: BrokerEventSessionUpsert, SessionID: "sess-1", Session: BrokerSessionSnapshot{BrokerSession: BrokerSession{SessionID: "sess-1"}}}
	close(events)

	if err := <-errCh; err != nil {
		t.Fatalf("writeBrokerEvents returned error: %v", err)
	}
	if _, ok := streams["sess-1"]; ok {
		t.Fatal("slow interactive stream remained registered after queue overflow")
	}
	if err := broker.RouteInputText("sess-1", interactiveOwner, "echo hi", true); err != ErrBrokerInteractiveNotGranted {
		t.Fatalf("RouteInputText err = %v, want ErrBrokerInteractiveNotGranted", err)
	}
	_ = readTestJSONFrame[sessionproto.SessionUpsert](t, &control, frame.TypeSessionUpsert)
}

func TestConnectivityFallbackSimulatorRoutesInputAndReconnectsFreshState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewBroker()
	brokerClientConn, brokerServerConn := net.Pipe()
	defer brokerClientConn.Close()
	defer brokerServerConn.Close()
	owner := &brokerConnection{conn: brokerServerConn}
	broker.register(BrokerSession{
		SessionID:      "sess-1",
		Label:          "API fix",
		CommandPreview: "codex",
		CWD:            "/repo",
		GitBranch:      "main",
		StartedAt:      100,
		UpdatedAt:      101,
	}, owner)
	broker.updatePreview("sess-1", "cached preview", 102, owner)
	broker.updateSnapshot("sess-1", []byte("full terminal snapshot"), 132, 43, owner)

	firstConn, firstServerErr := startConnectivityTransportForTest(t, ctx, broker, transportTestTrustedFingerprint)
	firstControl, err := firstConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("first OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(firstControl, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		ClientFingerprint: transportTestTrustedFingerprint,
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write first hello returned error: %v", err)
	}
	_ = readTestJSONFrame[sessionproto.Hello](t, firstControl, frame.TypeHello)
	_ = readTestJSONFrame[sessionproto.SessionIndex](t, firstControl, frame.TypeSessionIndex)
	_ = readTestJSONFrame[sessionproto.PathState](t, firstControl, frame.TypePathState)
	if err := writeTestJSONFrame(firstControl, frame.TypeInteractiveRequest, sessionproto.InteractiveRequest{
		SessionID: "sess-1",
		Cols:      100,
		Rows:      30,
	}); err != nil {
		t.Fatalf("write interactive_request returned error: %v", err)
	}
	granted := readTestJSONFrame[sessionproto.InteractiveGranted](t, firstControl, frame.TypeInteractiveGranted)
	if granted.SessionID != "sess-1" || granted.InteractiveStreamID == 0 {
		t.Fatalf("interactive granted = %#v, want sess-1 with interactive stream", granted)
	}
	if granted.Cols != 132 || granted.Rows != 43 {
		t.Fatalf("interactive granted size = %dx%d, want stored snapshot dimensions 132x43", granted.Cols, granted.Rows)
	}
	interactiveStream, err := firstConn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatalf("AcceptUniStream returned error: %v", err)
	}
	if int64(interactiveStream.StreamID()) != granted.InteractiveStreamID {
		t.Fatalf("interactive stream id = %d, want %d", interactiveStream.StreamID(), granted.InteractiveStreamID)
	}
	snapshotBegin := readTestJSONFrame[sessionproto.SnapshotBegin](t, interactiveStream, frame.TypeSnapshotBegin)
	if snapshotBegin.SessionID != "sess-1" || snapshotBegin.Cols != 132 || snapshotBegin.Rows != 43 {
		t.Fatalf("snapshot_begin = %#v, want sess-1 stored 132x43 snapshot dimensions", snapshotBegin)
	}
	snapshotChunk := readTestRawFrame(t, interactiveStream, frame.TypeSnapshotChunk)
	if string(snapshotChunk) != "full terminal snapshot" {
		t.Fatalf("snapshot_chunk = %q, want full terminal snapshot", string(snapshotChunk))
	}
	snapshotEnd := readTestJSONFrame[sessionproto.SnapshotEnd](t, interactiveStream, frame.TypeSnapshotEnd)
	if snapshotEnd.SessionID != "sess-1" || snapshotEnd.ChunkCount != 1 {
		t.Fatalf("snapshot_end = %#v, want sess-1 with one chunk", snapshotEnd)
	}

	broker.output("sess-1", []byte("live bytes"), owner)
	live := readTestRawFrame(t, interactiveStream, frame.TypeLiveBytes)
	if string(live) != "live bytes" {
		t.Fatalf("live = %q, want live bytes", string(live))
	}

	if err := writeTestJSONFrame(firstControl, frame.TypeInputText, sessionproto.InputText{
		SessionID: "sess-1",
		Text:      "echo fallback",
		Submit:    true,
	}); err != nil {
		t.Fatalf("write input_text returned error: %v", err)
	}
	var routed BrokerFrame
	if err := json.NewDecoder(brokerClientConn).Decode(&routed); err != nil {
		t.Fatalf("Decode broker frame returned error: %v", err)
	}
	if routed.Type != brokerFrameInputText || routed.SessionID != "sess-1" || routed.Text != "echo fallback" || !routed.Submit {
		t.Fatalf("broker frame = %#v, want submitted fallback input", routed)
	}
	if err := writeTestJSONFrame(firstControl, frame.TypeInputKey, sessionproto.InputKey{
		SessionID: "sess-1",
		Key:       "TAB",
	}); err != nil {
		t.Fatalf("write input_key returned error: %v", err)
	}
	if err := json.NewDecoder(brokerClientConn).Decode(&routed); err != nil {
		t.Fatalf("Decode input_key broker frame returned error: %v", err)
	}
	if routed.Type != brokerFrameInputKey || routed.SessionID != "sess-1" || routed.Key != "TAB" {
		t.Fatalf("broker frame = %#v, want input key", routed)
	}
	if err := writeTestJSONFrame(firstControl, frame.TypeResize, sessionproto.Resize{
		SessionID: "sess-1",
		Cols:      120,
		Rows:      40,
	}); err != nil {
		t.Fatalf("write resize returned error: %v", err)
	}
	if err := json.NewDecoder(brokerClientConn).Decode(&routed); err != nil {
		t.Fatalf("Decode resize broker frame returned error: %v", err)
	}
	if routed.Type != brokerFrameResize || routed.SessionID != "sess-1" || routed.Cols != 120 || routed.Rows != 40 {
		t.Fatalf("broker frame = %#v, want resize", routed)
	}
	firstConn.CloseWithError(0, "done")
	if err := <-firstServerErr; err != nil {
		t.Fatalf("first server returned error: %v", err)
	}

	broker.updatePreview("sess-1", "after reconnect", 103, owner)
	secondConn, secondServerErr := startConnectivityTransportForTest(t, ctx, broker, transportTestTrustedFingerprint)
	secondControl, err := secondConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("second OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(secondControl, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		ClientFingerprint: transportTestTrustedFingerprint,
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write second hello returned error: %v", err)
	}
	_ = readTestJSONFrame[sessionproto.Hello](t, secondControl, frame.TypeHello)
	index := readTestJSONFrame[sessionproto.SessionIndex](t, secondControl, frame.TypeSessionIndex)
	if len(index.Sessions) != 1 || index.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("reconnect index = %#v, want fresh sess-1 state", index)
	}
	_ = readTestJSONFrame[sessionproto.PathState](t, secondControl, frame.TypePathState)
	if err := writeTestJSONFrame(secondControl, frame.TypePreviewSubscribe, sessionproto.PreviewSubscribe{SessionID: "sess-1"}); err != nil {
		t.Fatalf("write reconnect preview_subscribe returned error: %v", err)
	}
	preview := readTestJSONFrame[sessionproto.PreviewSnapshot](t, secondControl, frame.TypePreviewSnapshot)
	if preview.Preview != "after reconnect" || preview.UpdatedAt != 103 {
		t.Fatalf("reconnect preview = %#v, want fresh preview", preview)
	}
	secondConn.CloseWithError(0, "done")
	if err := <-secondServerErr; err != nil {
		t.Fatalf("second server returned error: %v", err)
	}
}

func TestConnectivityTransportClosesInteractiveStreamWhenSessionGone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewBroker()
	owner := &brokerConnection{}
	broker.register(BrokerSession{
		SessionID:      "sess-1",
		CommandPreview: "codex",
		CWD:            "/repo",
		StartedAt:      100,
		UpdatedAt:      101,
	}, owner)
	broker.updateSnapshot("sess-1", []byte("snapshot"), 100, 30, owner)

	clientConn, serverErr := startConnectivityTransportForTest(t, ctx, broker, transportTestTrustedFingerprint)
	defer clientConn.CloseWithError(0, "done")
	control, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("OpenStreamSync returned error: %v", err)
	}
	if err := writeTestJSONFrame(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorMobile,
		ClientFingerprint: transportTestTrustedFingerprint,
		PathKind:          sessionproto.PathRelay,
	}); err != nil {
		t.Fatalf("write hello returned error: %v", err)
	}
	_ = readTestJSONFrame[sessionproto.Hello](t, control, frame.TypeHello)
	_ = readTestJSONFrame[sessionproto.SessionIndex](t, control, frame.TypeSessionIndex)
	_ = readTestJSONFrame[sessionproto.PathState](t, control, frame.TypePathState)
	if err := writeTestJSONFrame(control, frame.TypeInteractiveRequest, sessionproto.InteractiveRequest{
		SessionID: "sess-1",
		Cols:      100,
		Rows:      30,
	}); err != nil {
		t.Fatalf("write interactive_request returned error: %v", err)
	}
	granted := readTestJSONFrame[sessionproto.InteractiveGranted](t, control, frame.TypeInteractiveGranted)
	interactiveStream, err := clientConn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatalf("AcceptUniStream returned error: %v", err)
	}
	if int64(interactiveStream.StreamID()) != granted.InteractiveStreamID {
		t.Fatalf("interactive stream id = %d, want %d", interactiveStream.StreamID(), granted.InteractiveStreamID)
	}
	_ = readTestJSONFrame[sessionproto.SnapshotBegin](t, interactiveStream, frame.TypeSnapshotBegin)
	_ = readTestRawFrame(t, interactiveStream, frame.TypeSnapshotChunk)
	_ = readTestJSONFrame[sessionproto.SnapshotEnd](t, interactiveStream, frame.TypeSnapshotEnd)

	broker.remove("sess-1", owner)
	gone := readTestJSONFrame[sessionproto.SessionGone](t, control, frame.TypeSessionGone)
	if gone.SessionID != "sess-1" {
		t.Fatalf("session_gone = %#v, want sess-1", gone)
	}
	if got, err := frame.Read(interactiveStream, frame.DefaultMaxPayload); err == nil {
		t.Fatalf("old interactive stream read frame %#v after session_gone, want stream closed", got)
	}

	clientConn.CloseWithError(0, "done")
	if err := <-serverErr; err != nil {
		t.Fatalf("server returned error: %v", err)
	}
}

func startConnectivityTransportForTest(t *testing.T, ctx context.Context, broker *Broker, androidFingerprint string) (*quic.Conn, <-chan error) {
	t.Helper()
	relay := carrier.NewRelay()
	clientPacketConn := relay.NewPacketConn("android")
	serverPacketConn := relay.NewPacketConn("daemon")
	t.Cleanup(func() {
		_ = clientPacketConn.Close()
		_ = serverPacketConn.Close()
	})
	serverTLS, clientTLS := connectivityTransportTLSConfigs(t)
	listener, err := quic.Listen(serverPacketConn, serverTLS, conntransport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Listen returned error: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverErr <- err
			return
		}
		serverErr <- (&ConnectivityTransport{
			Broker:             broker,
			DaemonFingerprint:  transportTestDaemonFingerprint,
			AndroidFingerprint: androidFingerprint,
			PathKind:           sessionproto.PathRelay,
			AttemptID:          "attempt-test",
			FallbackReason:     "direct_timeout",
			DirectSetupLatency: 3 * time.Second,
			RelaySetupLatency:  120 * time.Millisecond,
		}).Serve(ctx, conn)
	}()
	clientConn, err := quic.Dial(ctx, clientPacketConn, serverPacketConn.LocalAddr(), clientTLS, conntransport.QUICConfig())
	if err != nil {
		t.Fatalf("quic.Dial returned error: %v", err)
	}
	return clientConn, serverErr
}

func writeTestJSONFrame(w io.Writer, typ byte, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return frame.Write(w, frame.Frame{Type: typ, Payload: raw})
}

func readTestJSONFrame[T any](t *testing.T, r io.Reader, typ byte) T {
	t.Helper()
	got, err := frame.Read(r, frame.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("frame.Read returned error: %v", err)
	}
	if got.Type != typ {
		t.Fatalf("frame type = 0x%02x, want 0x%02x", got.Type, typ)
	}
	var payload T
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return payload
}

func sessionMetadataByID(t *testing.T, sessions []sessionproto.SessionMetadata) map[string]sessionproto.SessionMetadata {
	t.Helper()
	out := make(map[string]sessionproto.SessionMetadata, len(sessions))
	for _, session := range sessions {
		if session.SessionID == "" {
			t.Fatalf("session index contains blank session id: %#v", sessions)
		}
		if _, exists := out[session.SessionID]; exists {
			t.Fatalf("session index contains duplicate session id %q: %#v", session.SessionID, sessions)
		}
		out[session.SessionID] = session
	}
	return out
}

func readTestRawFrame(t *testing.T, r io.Reader, typ byte) []byte {
	t.Helper()
	got, err := frame.Read(r, frame.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("frame.Read returned error: %v", err)
	}
	if got.Type != typ {
		t.Fatalf("frame type = 0x%02x, want 0x%02x", got.Type, typ)
	}
	payload := make([]byte, len(got.Payload))
	copy(payload, got.Payload)
	return payload
}

type deadlineBuffer struct {
	bytes.Buffer
	readDeadlineSet      time.Time
	readDeadlineCleared  bool
	writeDeadlineSet     time.Time
	writeDeadlineCleared bool
}

func (b *deadlineBuffer) SetReadDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		b.readDeadlineCleared = true
		return nil
	}
	b.readDeadlineSet = deadline
	return nil
}

func (b *deadlineBuffer) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		b.writeDeadlineCleared = true
		return nil
	}
	b.writeDeadlineSet = deadline
	return nil
}

func (b *deadlineBuffer) Close() error {
	return nil
}

func connectivityTransportTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	daemonKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x41}, ed25519.SeedSize))
	androidKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))

	daemonCert, err := identity.SelfSignedCertificate(daemonKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("daemon SelfSignedCertificate returned error: %v", err)
	}
	androidCert, err := identity.SelfSignedCertificate(androidKey, identity.CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("android SelfSignedCertificate returned error: %v", err)
	}

	return conntransport.DaemonTLSConfig(conntransport.EndpointConfig{
			Certificate:         daemonCert,
			PinnedPeerPublicKey: androidKey.Public().(ed25519.PublicKey),
		}),
		conntransport.AndroidTLSConfig(conntransport.EndpointConfig{
			Certificate:         androidCert,
			PinnedPeerPublicKey: daemonKey.Public().(ed25519.PublicKey),
			ServerName:          "connectivity.daemon",
		})
}
