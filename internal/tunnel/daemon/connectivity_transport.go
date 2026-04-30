package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/sessionproto"
	"yuanbohan/tunnel/internal/connectivity/transport"
)

var (
	ErrConnectivityProtocolVersion = errors.New("connectivity protocol version mismatch")
	ErrConnectivityDeviceUntrusted = errors.New("connectivity device not trusted")
)

const (
	connectivityTransportReadTimeout  = 15 * time.Second
	connectivityTransportWriteTimeout = 15 * time.Second
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
	if t.AndroidFingerprint != "" && hello.DeviceFingerprint != t.AndroidFingerprint {
		_ = writeConnectivityJSON(control, frame.TypeError, sessionproto.Error{Code: "device_not_trusted"})
		return ErrConnectivityDeviceUntrusted
	}

	pathKind := t.PathKind
	if pathKind == "" {
		pathKind = sessionproto.PathRelay
	}
	if err := writeConnectivityJSON(control, frame.TypeHello, sessionproto.Hello{
		ProtocolVersion:   sessionproto.ProtocolVersion,
		ActorType:         sessionproto.ActorDaemon,
		DeviceFingerprint: t.DaemonFingerprint,
		PathKind:          pathKind,
	}); err != nil {
		return err
	}
	interactiveOwner := &struct{ id int }{id: 1}
	defer t.Broker.ReleaseInteractiveOwner(interactiveOwner)

	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	snapshot, events, cancel := t.Broker.SnapshotAndSubscribe()
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
	errCh := make(chan error, 2)

	go func() {
		errCh <- t.readControlLoop(serveCtx, conn, control, writeMu, subsMu, subscribedPreviews, interactiveOwner)
	}()
	go func() {
		errCh <- t.writeBrokerEvents(serveCtx, control, writeMu, subsMu, events, subscribedPreviews)
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

func (t *ConnectivityTransport) readControlLoop(ctx context.Context, conn *quic.Conn, control *quic.Stream, writeMu *sync.Mutex, subsMu *sync.RWMutex, subscribedPreviews map[string]struct{}, interactiveOwner any) error {
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
			if grantErr == nil {
				interactiveStream, streamID, grantErr = t.openInteractiveStream(ctx, conn, payload)
				if grantErr != nil {
					t.Broker.ReleaseInteractive(payload.SessionID, interactiveOwner)
				}
			}
			writeMu.Lock()
			if grantErr == nil {
				activeInteractive[payload.SessionID] = struct{}{}
				err = writeConnectivityJSON(control, frame.TypeInteractiveGranted, sessionproto.InteractiveGranted{
					SessionID:           payload.SessionID,
					InteractiveStreamID: streamID,
					Cols:                payload.Cols,
					Rows:                payload.Rows,
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
				if interactiveStream != nil {
					_ = interactiveStream.Close()
				}
				return err
			}
			if interactiveStream != nil {
				if err := t.writeInitialInteractiveSnapshot(interactiveStream, payload); err != nil {
					return err
				}
			}
		case frame.TypeInteractiveRelease:
			var payload sessionproto.InteractiveRelease
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			delete(activeInteractive, payload.SessionID)
			t.Broker.ReleaseInteractive(payload.SessionID, interactiveOwner)
		case frame.TypePreviewSubscribe:
			var payload sessionproto.PreviewSubscribe
			if err := json.Unmarshal(got.Payload, &payload); err != nil {
				return err
			}
			if payload.SessionID != "" {
				subsMu.Lock()
				subscribedPreviews[payload.SessionID] = struct{}{}
				subsMu.Unlock()
				if snapshot, ok := brokerSnapshotBySession(t.Broker.Snapshot(), payload.SessionID); ok {
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

func (t *ConnectivityTransport) openInteractiveStream(ctx context.Context, conn *quic.Conn, request sessionproto.InteractiveRequest) (*quic.SendStream, int64, error) {
	stream, err := conn.OpenUniStreamSync(ctx)
	if err != nil {
		return nil, 0, err
	}
	return stream, int64(stream.StreamID()), nil
}

func (t *ConnectivityTransport) writeInitialInteractiveSnapshot(stream *quic.SendStream, request sessionproto.InteractiveRequest) error {
	defer stream.Close()
	if err := writeConnectivityJSON(stream, frame.TypeSnapshotBegin, sessionproto.SnapshotBegin{
		SessionID: request.SessionID,
		Cols:      request.Cols,
		Rows:      request.Rows,
	}); err != nil {
		return err
	}
	return writeConnectivityJSON(stream, frame.TypeSnapshotEnd, sessionproto.SnapshotEnd{
		SessionID: request.SessionID,
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

func (t *ConnectivityTransport) writeBrokerEvents(ctx context.Context, control *quic.Stream, writeMu *sync.Mutex, subsMu *sync.RWMutex, events <-chan BrokerEvent, subscribedPreviews map[string]struct{}) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			writeMu.Lock()
			var err error
			switch event.Type {
			case BrokerEventSessionUpsert:
				err = writeConnectivityJSON(control, frame.TypeSessionUpsert, sessionproto.SessionUpsert{Session: brokerSessionMetadataOne(event.Session)})
			case BrokerEventSessionGone:
				err = writeConnectivityJSON(control, frame.TypeSessionGone, sessionproto.SessionGone{SessionID: event.SessionID})
			case BrokerEventPreview:
				subsMu.RLock()
				_, subscribed := subscribedPreviews[event.SessionID]
				subsMu.RUnlock()
				if subscribed {
					err = writeConnectivityJSON(control, frame.TypePreviewSnapshot, sessionproto.PreviewSnapshot{
						SessionID: event.SessionID,
						Preview:   event.Session.LatestPreview,
						UpdatedAt: event.Session.UpdatedAt,
					})
				}
			}
			writeMu.Unlock()
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
	}
	return frame.Write(w, frame.Frame{Type: typ, Payload: raw})
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
