package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/sessionproto"
	"yuanbohan/tunnel/internal/connectivity/transport"
	relayauth "yuanbohan/tunnel/internal/relay/auth"
)

var (
	ErrConnectivityProtocolVersion = errors.New("connectivity protocol version mismatch")
	ErrConnectivityDeviceUntrusted = errors.New("connectivity device not trusted")
)

const (
	connectivityTransportReadTimeout  = 15 * time.Second
	connectivityTransportWriteTimeout = 15 * time.Second
	connectivityInteractiveQueueLimit = 64
)

type ConnectivityTransport struct {
	Broker             *Broker
	DaemonFingerprint  string
	AndroidFingerprint string
	PathKind           string
	AttemptID          string
	FallbackReason     string
	DirectSetupLatency time.Duration
	RelaySetupLatency  time.Duration
	MaxPayload         int
}

type connectivitySendStream interface {
	io.Writer
	Close() error
	SetWriteDeadline(time.Time) error
}

type interactiveStreamState struct {
	stream connectivitySendStream
	queue  chan []byte
	done   chan struct{}
	once   sync.Once
}

func (t *ConnectivityTransport) Serve(ctx context.Context, conn *quic.Conn) error {
	if t == nil {
		return errors.New("nil connectivity transport")
	}
	if t.Broker == nil {
		t.Broker = NewBroker()
	}
	stopContextClose := context.AfterFunc(ctx, func() {
		_ = conn.CloseWithError(0, "context canceled")
	})
	defer stopContextClose()
	if err := transport.ValidateConnectionState(conn.ConnectionState()); err != nil {
		return err
	}
	control, err := conn.AcceptStream(ctx)
	if err != nil {
		return err
	}

	hello, err := readConnectivityJSON[sessionproto.Hello](control, frame.TypeHello, t.maxPayload())
	if err != nil {
		return err
	}
	if hello.ProtocolVersion != sessionproto.ProtocolVersion {
		_ = writeConnectivityJSON(control, frame.TypeError, sessionproto.Error{Code: "protocol_version_mismatch"})
		return ErrConnectivityProtocolVersion
	}
	configuredFingerprint := strings.TrimSpace(t.AndroidFingerprint)
	if configuredFingerprint != "" {
		normalized, err := relayauth.NormalizeDeviceFingerprint(configuredFingerprint)
		if err != nil {
			_ = writeConnectivityJSON(control, frame.TypeError, sessionproto.Error{Code: "device_not_trusted"})
			return ErrConnectivityDeviceUntrusted
		}
		configuredFingerprint = normalized
	}
	if configuredFingerprint != "" {
		clientFingerprint, err := relayauth.NormalizeDeviceFingerprint(strings.TrimSpace(hello.ClientFingerprint))
		if err != nil || clientFingerprint != configuredFingerprint {
			_ = writeConnectivityJSON(control, frame.TypeError, sessionproto.Error{Code: "device_not_trusted"})
			return ErrConnectivityDeviceUntrusted
		}
	}

	pathKind := t.PathKind
	if pathKind == "" {
		pathKind = sessionproto.PathRelay
	}
	if err := writeConnectivityJSON(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorDaemon,
		ClientFingerprint: t.DaemonFingerprint,
		PathKind:          pathKind,
	}); err != nil {
		return err
	}
	interactiveOwner := &struct{ id int }{id: 1}
	defer t.Broker.ReleaseInteractiveOwner(interactiveOwner)

	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	snapshot, events, cancel := t.Broker.SnapshotMetadataAndSubscribe()
	defer cancel()
	if err := writeConnectivityJSON(control, frame.TypeSessionIndex, sessionproto.SessionIndex{Sessions: brokerSessionMetadata(snapshot)}); err != nil {
		return err
	}
	if err := writeConnectivityJSON(control, frame.TypePathState, sessionproto.PathState{
		AttemptID:            t.AttemptID,
		PathKind:             pathKind,
		FallbackReason:       t.FallbackReason,
		DirectSetupLatencyMS: durationMillis(t.DirectSetupLatency),
		RelaySetupLatencyMS:  durationMillis(t.RelaySetupLatency),
	}); err != nil {
		return err
	}
	subscribedPreviews := make(map[string]struct{})
	subsMu := &sync.RWMutex{}
	writeMu := &sync.Mutex{}
	interactiveStreams := make(map[string]*interactiveStreamState)
	interactiveStreamsMu := &sync.RWMutex{}
	errCh := make(chan error, 2)

	go func() {
		errCh <- t.readControlLoop(serveCtx, conn, control, writeMu, subsMu, subscribedPreviews, interactiveOwner, interactiveStreamsMu, interactiveStreams)
	}()
	go func() {
		errCh <- t.writeBrokerEvents(serveCtx, control, writeMu, subsMu, events, subscribedPreviews, interactiveOwner, interactiveStreamsMu, interactiveStreams)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		cancelServe()
		if errors.Is(err, io.EOF) || isConnectivityNormalClose(err) {
			return nil
		}
		return err
	}
}

func durationMillis(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(duration / time.Millisecond)
}

func isConnectivityNormalClose(err error) bool {
	var appErr *quic.ApplicationError
	return errors.As(err, &appErr) && appErr.ErrorCode == 0
}

func (t *ConnectivityTransport) readControlLoop(
	ctx context.Context,
	conn *quic.Conn,
	control *quic.Stream,
	writeMu *sync.Mutex,
	subsMu *sync.RWMutex,
	subscribedPreviews map[string]struct{},
	interactiveOwner any,
	interactiveStreamsMu *sync.RWMutex,
	interactiveStreams map[string]*interactiveStreamState,
) error {
	defer func() {
		interactiveStreamsMu.Lock()
		states := make([]*interactiveStreamState, 0, len(interactiveStreams))
		for sessionID, state := range interactiveStreams {
			if state != nil {
				states = append(states, state)
			}
			delete(interactiveStreams, sessionID)
		}
		interactiveStreamsMu.Unlock()
		for _, state := range states {
			closeInteractiveStreamState(state)
		}
	}()
	activeInteractive := make(map[string]struct{})
	for {
		got, err := frame.Read(control, t.maxPayload())
		if err != nil {
			return err
		}
		switch got.Type {
		case frame.TypeInteractiveRequest:
			var payload sessionproto.InteractiveRequest
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			grantErr := t.Broker.GrantInteractive(payload.SessionID, interactiveOwner)
			var streamID int64
			var interactiveStream *quic.SendStream
			var interactiveState *interactiveStreamState
			snapshot, hasSnapshot, grantCols, grantRows := t.initialInteractiveSnapshot(payload)
			if grantErr == nil {
				interactiveStream, streamID, grantErr = t.openInteractiveStream(ctx, conn, payload)
				if grantErr != nil {
					t.Broker.ReleaseInteractive(payload.SessionID, interactiveOwner)
				} else {
					interactiveState = newInteractiveStreamState(interactiveStream)
					interactiveStreamsMu.Lock()
					old := interactiveStreams[payload.SessionID]
					interactiveStreams[payload.SessionID] = interactiveState
					interactiveStreamsMu.Unlock()
					closeInteractiveStreamState(old)
				}
			}
			writeMu.Lock()
			if grantErr == nil {
				activeInteractive[payload.SessionID] = struct{}{}
				err = writeConnectivityJSON(control, frame.TypeInteractiveGranted, sessionproto.InteractiveGranted{
					SessionID:           payload.SessionID,
					InteractiveStreamID: streamID,
					Cols:                grantCols,
					Rows:                grantRows,
				})
			} else {
				reason := "session_unavailable"
				if errors.Is(grantErr, ErrBrokerInteractiveBusy) {
					reason = "daemon_busy"
				}
				err = writeConnectivityJSON(control, frame.TypeInteractiveDenied, sessionproto.InteractiveDenied{
					SessionID: payload.SessionID,
					Reason:    reason,
				})
			}
			writeMu.Unlock()
			if err != nil {
				if interactiveState != nil {
					interactiveStreamsMu.Lock()
					if interactiveStreams[payload.SessionID] == interactiveState {
						delete(interactiveStreams, payload.SessionID)
					}
					interactiveStreamsMu.Unlock()
					closeInteractiveStreamState(interactiveState)
				}
				return err
			}
			if interactiveState != nil {
				if err := t.writeInitialInteractiveSnapshot(interactiveStream, payload, snapshot, hasSnapshot, grantCols, grantRows); err != nil {
					interactiveStreamsMu.Lock()
					if interactiveStreams[payload.SessionID] == interactiveState {
						delete(interactiveStreams, payload.SessionID)
					}
					interactiveStreamsMu.Unlock()
					closeInteractiveStreamState(interactiveState)
					return err
				}
				interactiveState.start(serveInteractiveStreamContext(ctx), func() {
					t.Broker.ReleaseInteractive(payload.SessionID, interactiveOwner)
					removeInteractiveStreamState(payload.SessionID, interactiveState, interactiveStreamsMu, interactiveStreams)
				})
			}
		case frame.TypeInteractiveRelease:
			var payload sessionproto.InteractiveRelease
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			delete(activeInteractive, payload.SessionID)
			t.Broker.ReleaseInteractive(payload.SessionID, interactiveOwner)
			interactiveStreamsMu.Lock()
			state := interactiveStreams[payload.SessionID]
			delete(interactiveStreams, payload.SessionID)
			interactiveStreamsMu.Unlock()
			closeInteractiveStreamState(state)
		case frame.TypePreviewSubscribe:
			var payload sessionproto.PreviewSubscribe
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			if payload.SessionID != "" {
				subsMu.Lock()
				subscribedPreviews[payload.SessionID] = struct{}{}
				subsMu.Unlock()
				if snapshot, ok := t.Broker.SnapshotBySession(payload.SessionID); ok {
					writeMu.Lock()
					if err := writeConnectivityJSON(control, frame.TypePreviewSnapshot, sessionproto.PreviewSnapshot{
						SessionID: snapshot.SessionID,
						Preview:   snapshot.LatestPreview,
						UpdatedAt: snapshot.UpdatedAt,
					}); err != nil {
						writeMu.Unlock()
						return err
					}
					writeMu.Unlock()
				}
			}
		case frame.TypePreviewUnsubscribe:
			var payload sessionproto.PreviewUnsubscribe
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			subsMu.Lock()
			delete(subscribedPreviews, payload.SessionID)
			subsMu.Unlock()
		case frame.TypeInputText:
			var payload sessionproto.InputText
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			if _, ok := activeInteractive[payload.SessionID]; !ok {
				if err := t.writeError(control, writeMu, "interactive_not_granted"); err != nil {
					return err
				}
				continue
			}
			if err := t.Broker.RouteInputText(payload.SessionID, interactiveOwner, payload.Text, payload.Submit); err != nil {
				if err := t.writeRouteError(control, writeMu, err); err != nil {
					return err
				}
			}
		case frame.TypeInputKey:
			var payload sessionproto.InputKey
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			if _, ok := activeInteractive[payload.SessionID]; !ok {
				if err := t.writeError(control, writeMu, "interactive_not_granted"); err != nil {
					return err
				}
				continue
			}
			if err := t.Broker.RouteInputKey(payload.SessionID, interactiveOwner, payload.Key); err != nil {
				if err := t.writeRouteError(control, writeMu, err); err != nil {
					return err
				}
			}
		case frame.TypeResize:
			var payload sessionproto.Resize
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			if _, ok := activeInteractive[payload.SessionID]; !ok {
				if err := t.writeError(control, writeMu, "interactive_not_granted"); err != nil {
					return err
				}
				continue
			}
			if err := t.Broker.RouteResize(payload.SessionID, interactiveOwner, payload.Cols, payload.Rows); err != nil {
				if err := t.writeRouteError(control, writeMu, err); err != nil {
					return err
				}
			}
		default:
			// Forward compatibility: unknown frame types are ignored.
		}
	}
}

func closeInteractiveStreamState(state *interactiveStreamState) {
	if state == nil {
		return
	}
	state.close()
}

func removeInteractiveStreamState(sessionID string, state *interactiveStreamState, streamsMu *sync.RWMutex, streams map[string]*interactiveStreamState) {
	streamsMu.Lock()
	if streams[sessionID] == state {
		delete(streams, sessionID)
	}
	streamsMu.Unlock()
	closeInteractiveStreamState(state)
}

func newInteractiveStreamState(stream connectivitySendStream) *interactiveStreamState {
	return &interactiveStreamState{
		stream: stream,
		queue:  make(chan []byte, connectivityInteractiveQueueLimit),
		done:   make(chan struct{}),
	}
}

func (s *interactiveStreamState) start(ctx context.Context, onClose func()) {
	if s == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				s.close()
				return
			case <-s.done:
				return
			case payload := <-s.queue:
				if err := writeConnectivityRaw(s.stream, frame.TypeLiveBytes, payload); err != nil {
					if onClose != nil {
						onClose()
					} else {
						s.close()
					}
					return
				}
			}
		}
	}()
}

func (s *interactiveStreamState) enqueue(payload []byte) bool {
	if s == nil || len(payload) == 0 {
		return true
	}
	copyPayload := append([]byte(nil), payload...)
	select {
	case <-s.done:
		return false
	case s.queue <- copyPayload:
		return true
	default:
		return false
	}
}

func (s *interactiveStreamState) close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.done)
		if s.stream != nil {
			_ = s.stream.Close()
		}
	})
}

func serveInteractiveStreamContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (t *ConnectivityTransport) openInteractiveStream(ctx context.Context, conn *quic.Conn, request sessionproto.InteractiveRequest) (*quic.SendStream, int64, error) {
	stream, err := conn.OpenUniStreamSync(ctx)
	if err != nil {
		return nil, 0, err
	}
	return stream, int64(stream.StreamID()), nil
}

func (t *ConnectivityTransport) initialInteractiveSnapshot(request sessionproto.InteractiveRequest) (BrokerSessionSnapshot, bool, int, int) {
	snapshot, ok := t.Broker.SnapshotBySession(request.SessionID)
	cols := request.Cols
	rows := request.Rows
	if ok && snapshot.SnapshotCols > 0 && snapshot.SnapshotRows > 0 {
		cols = snapshot.SnapshotCols
		rows = snapshot.SnapshotRows
	}
	return snapshot, ok, cols, rows
}

func (t *ConnectivityTransport) writeInitialInteractiveSnapshot(stream io.Writer, request sessionproto.InteractiveRequest, snapshot BrokerSessionSnapshot, ok bool, cols, rows int) error {
	chunkCount := 0
	if err := writeConnectivityJSON(stream, frame.TypeSnapshotBegin, sessionproto.SnapshotBegin{
		SessionID: request.SessionID,
		Cols:      cols,
		Rows:      rows,
	}); err != nil {
		return err
	}
	if ok && len(snapshot.LatestSnapshot) > 0 {
		for start := 0; start < len(snapshot.LatestSnapshot); start += t.maxPayload() {
			end := start + t.maxPayload()
			if end > len(snapshot.LatestSnapshot) {
				end = len(snapshot.LatestSnapshot)
			}
			if err := writeConnectivityRaw(stream, frame.TypeSnapshotChunk, snapshot.LatestSnapshot[start:end]); err != nil {
				return err
			}
			chunkCount++
		}
	}
	return writeConnectivityJSON(stream, frame.TypeSnapshotEnd, sessionproto.SnapshotEnd{
		SessionID:  request.SessionID,
		ChunkCount: chunkCount,
	})
}

func (t *ConnectivityTransport) writeSessionUnavailable(control *quic.Stream, writeMu *sync.Mutex) error {
	return t.writeError(control, writeMu, "session_unavailable")
}

func (t *ConnectivityTransport) writeRouteError(control *quic.Stream, writeMu *sync.Mutex, err error) error {
	if errors.Is(err, ErrBrokerInteractiveNotGranted) {
		return t.writeError(control, writeMu, "interactive_not_granted")
	}
	return t.writeSessionUnavailable(control, writeMu)
}

func (t *ConnectivityTransport) writeError(control *quic.Stream, writeMu *sync.Mutex, code string) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	return writeConnectivityJSON(control, frame.TypeError, sessionproto.Error{Code: code})
}

func (t *ConnectivityTransport) writeBrokerEvents(
	ctx context.Context,
	control io.Writer,
	writeMu *sync.Mutex,
	subsMu *sync.RWMutex,
	events <-chan BrokerEvent,
	subscribedPreviews map[string]struct{},
	interactiveOwner any,
	interactiveStreamsMu *sync.RWMutex,
	interactiveStreams map[string]*interactiveStreamState,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			var err error
			switch event.Type {
			case BrokerEventSessionUpsert:
				writeMu.Lock()
				err = writeConnectivityJSON(control, frame.TypeSessionUpsert, sessionproto.SessionUpsert{Session: brokerSessionMetadataOne(event.Session)})
				writeMu.Unlock()
			case BrokerEventSessionGone:
				interactiveStreamsMu.Lock()
				state := interactiveStreams[event.SessionID]
				delete(interactiveStreams, event.SessionID)
				interactiveStreamsMu.Unlock()
				closeInteractiveStreamState(state)
				writeMu.Lock()
				err = writeConnectivityJSON(control, frame.TypeSessionGone, sessionproto.SessionGone{SessionID: event.SessionID})
				writeMu.Unlock()
			case BrokerEventPreview:
				subsMu.RLock()
				_, subscribed := subscribedPreviews[event.SessionID]
				subsMu.RUnlock()
				if subscribed {
					writeMu.Lock()
					err = writeConnectivityJSON(control, frame.TypePreviewSnapshot, sessionproto.PreviewSnapshot{
						SessionID: event.SessionID,
						Preview:   event.Session.LatestPreview,
						UpdatedAt: event.Session.UpdatedAt,
					})
					writeMu.Unlock()
				}
			case BrokerEventOutput:
				interactiveStreamsMu.RLock()
				state := interactiveStreams[event.SessionID]
				interactiveStreamsMu.RUnlock()
				if state != nil && len(event.Output) > 0 && !state.enqueue(event.Output) {
					t.Broker.ReleaseInteractive(event.SessionID, interactiveOwner)
					removeInteractiveStreamState(event.SessionID, state, interactiveStreamsMu, interactiveStreams)
				}
			case BrokerEventOutputOverflow:
				t.Broker.ReleaseInteractive(event.SessionID, interactiveOwner)
				interactiveStreamsMu.RLock()
				state := interactiveStreams[event.SessionID]
				interactiveStreamsMu.RUnlock()
				removeInteractiveStreamState(event.SessionID, state, interactiveStreamsMu, interactiveStreams)
			}
			if err != nil {
				return err
			}
		}
	}
}

func writeConnectivityJSON(w io.Writer, typ byte, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if setter, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = setter.SetWriteDeadline(time.Now().Add(connectivityTransportWriteTimeout))
		defer setter.SetWriteDeadline(time.Time{})
	}
	return frame.Write(w, frame.Frame{Type: typ, Payload: raw})
}

func writeConnectivityRaw(w io.Writer, typ byte, payload []byte) error {
	if setter, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = setter.SetWriteDeadline(time.Now().Add(connectivityTransportWriteTimeout))
		defer setter.SetWriteDeadline(time.Time{})
	}
	return frame.Write(w, frame.Frame{Type: typ, Payload: payload})
}

func readConnectivityJSON[T any](r io.Reader, typ byte, maxPayload int) (T, error) {
	var payload T
	if setter, ok := r.(interface{ SetReadDeadline(time.Time) error }); ok {
		_ = setter.SetReadDeadline(time.Now().Add(connectivityTransportReadTimeout))
		defer setter.SetReadDeadline(time.Time{})
	}
	got, err := frame.Read(r, maxPayload)
	if err != nil {
		return payload, err
	}
	if got.Type != typ {
		return payload, fmt.Errorf("connectivity frame type=0x%02x want=0x%02x", got.Type, typ)
	}
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func (t *ConnectivityTransport) maxPayload() int {
	if t.MaxPayload <= 0 {
		return frame.DefaultMaxPayload
	}
	return t.MaxPayload
}

func brokerSessionMetadata(sessions []BrokerSessionSnapshot) []sessionproto.SessionMetadata {
	out := make([]sessionproto.SessionMetadata, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, brokerSessionMetadataOne(session))
	}
	return out
}

func brokerSessionMetadataOne(session BrokerSessionSnapshot) sessionproto.SessionMetadata {
	return sessionproto.SessionMetadata{
		SessionID:      session.SessionID,
		Label:          session.Label,
		CommandPreview: session.CommandPreview,
		CWD:            session.CWD,
		GitBranch:      session.GitBranch,
		StartedAt:      session.StartedAt,
		UpdatedAt:      session.UpdatedAt,
		Online:         session.Online,
	}
}

func brokerSnapshotBySession(sessions []BrokerSessionSnapshot, sessionID string) (BrokerSessionSnapshot, bool) {
	for _, session := range sessions {
		if session.SessionID == sessionID {
			return session, true
		}
	}
	return BrokerSessionSnapshot{}, false
}
