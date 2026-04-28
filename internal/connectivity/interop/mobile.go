package interop

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/frame"
	"yuanbohan/tunnel/internal/connectivity/transport"
)

var (
	ErrMissingTLSConfig = errors.New("missing mobile TLS config")
	ErrUnexpectedFrame  = errors.New("unexpected interop frame")
)

const (
	ActorMobile = "mobile"
	ActorDaemon = "daemon"

	PathDirect = "direct"
	PathRelay  = "relay"
)

type MobileClient struct {
	TLSConfig  *tls.Config
	QUICConfig *quic.Config
	MaxPayload int
}

type Hello struct {
	ProtocolVersion   int    `json:"protocol_version"`
	ActorType         string `json:"actor_type"`
	DeviceFingerprint string `json:"device_fingerprint"`
	PathKind          string `json:"path_kind"`
}

type SessionMetadata struct {
	SessionID      string `json:"session_id"`
	Label          string `json:"label"`
	CommandPreview string `json:"command_preview"`
	CWD            string `json:"cwd"`
	GitBranch      string `json:"git_branch"`
	StartedAt      int    `json:"started_at"`
	UpdatedAt      int    `json:"updated_at"`
	Online         bool   `json:"online"`
}

type SessionIndex struct {
	Sessions []SessionMetadata `json:"sessions"`
}

type InteractiveRequest struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type InteractiveGranted struct {
	SessionID           string `json:"session_id"`
	InteractiveStreamID int64  `json:"interactive_stream_id"`
	Cols                int    `json:"cols"`
	Rows                int    `json:"rows"`
}

type SnapshotBegin struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type SnapshotEnd struct {
	SessionID  string `json:"session_id,omitempty"`
	ChunkCount int    `json:"chunk_count,omitempty"`
}

type ProbeScript struct {
	MobileHello        Hello
	DaemonHello        Hello
	SessionIndex       SessionIndex
	InteractiveRequest InteractiveRequest
	InteractiveGranted InteractiveGranted
	SnapshotBegin      SnapshotBegin
	SnapshotChunk      []byte
	SnapshotEnd        SnapshotEnd
	LiveBytes          []byte
}

func (c MobileClient) DialAddr(ctx context.Context, addr string, script ProbeScript) error {
	tlsConfig, quicConfig, err := c.configs()
	if err != nil {
		return err
	}

	conn, err := quic.DialAddr(ctx, addr, tlsConfig, quicConfig)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "done")
	return c.Run(ctx, conn, script)
}

func (c MobileClient) DialPacketConn(ctx context.Context, packetConn net.PacketConn, daemonAddr net.Addr, script ProbeScript) error {
	tlsConfig, quicConfig, err := c.configs()
	if err != nil {
		return err
	}

	conn, err := quic.Dial(ctx, packetConn, daemonAddr, tlsConfig, quicConfig)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "done")
	return c.Run(ctx, conn, script)
}

func (c MobileClient) Run(ctx context.Context, conn *quic.Conn, script ProbeScript) error {
	if err := transport.ValidateConnectionState(conn.ConnectionState()); err != nil {
		return err
	}

	control, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	if err := writeJSONFrame(control, frame.TypeHello, script.MobileHello); err != nil {
		return err
	}

	daemonHello, err := readJSONFrame[Hello](control, frame.TypeHello, c.maxPayload())
	if err != nil {
		return err
	}
	if daemonHello != script.DaemonHello {
		return fmt.Errorf("%w: daemon hello=%#v", ErrUnexpectedFrame, daemonHello)
	}

	sessionIndex, err := readJSONFrame[SessionIndex](control, frame.TypeSessionIndex, c.maxPayload())
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(sessionIndex, script.SessionIndex) {
		return fmt.Errorf("%w: session index=%#v", ErrUnexpectedFrame, sessionIndex)
	}

	if err := writeJSONFrame(control, frame.TypeInteractiveRequest, script.InteractiveRequest); err != nil {
		return err
	}

	granted, err := readJSONFrame[InteractiveGranted](control, frame.TypeInteractiveGranted, c.maxPayload())
	if err != nil {
		return err
	}
	expectedGrant := script.InteractiveGranted
	expectedGrant.InteractiveStreamID = granted.InteractiveStreamID
	if granted != expectedGrant {
		return fmt.Errorf("%w: interactive granted=%#v", ErrUnexpectedFrame, granted)
	}

	interactive, err := conn.AcceptUniStream(ctx)
	if err != nil {
		return err
	}
	if int64(interactive.StreamID()) != granted.InteractiveStreamID {
		return fmt.Errorf("%w: uni stream id=%d grant=%d", ErrUnexpectedFrame, interactive.StreamID(), granted.InteractiveStreamID)
	}

	snapshotBegin, err := readJSONFrame[SnapshotBegin](interactive, frame.TypeSnapshotBegin, c.maxPayload())
	if err != nil {
		return err
	}
	if snapshotBegin != script.SnapshotBegin {
		return fmt.Errorf("%w: snapshot begin=%#v", ErrUnexpectedFrame, snapshotBegin)
	}

	snapshotChunk, err := readRawFrame(interactive, frame.TypeSnapshotChunk, c.maxPayload())
	if err != nil {
		return err
	}
	if !bytes.Equal(snapshotChunk, script.SnapshotChunk) {
		return fmt.Errorf("%w: snapshot chunk len=%d", ErrUnexpectedFrame, len(snapshotChunk))
	}

	snapshotEnd, err := readJSONFrame[SnapshotEnd](interactive, frame.TypeSnapshotEnd, c.maxPayload())
	if err != nil {
		return err
	}
	if snapshotEnd != script.SnapshotEnd {
		return fmt.Errorf("%w: snapshot end=%#v", ErrUnexpectedFrame, snapshotEnd)
	}

	live, err := readRawFrame(interactive, frame.TypeLiveBytes, c.maxPayload())
	if err != nil {
		return err
	}
	if !bytes.Equal(live, script.LiveBytes) {
		return fmt.Errorf("%w: live bytes len=%d", ErrUnexpectedFrame, len(live))
	}
	return nil
}

func (c MobileClient) configs() (*tls.Config, *quic.Config, error) {
	if c.TLSConfig == nil {
		return nil, nil, ErrMissingTLSConfig
	}
	return c.TLSConfig, c.quicConfig(), nil
}

func (c MobileClient) quicConfig() *quic.Config {
	if c.QUICConfig == nil {
		return transport.QUICConfig()
	}
	return c.QUICConfig
}

func (c MobileClient) maxPayload() int {
	if c.MaxPayload <= 0 {
		return frame.DefaultMaxPayload
	}
	return c.MaxPayload
}

func writeJSONFrame(w io.Writer, typ byte, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return frame.Write(w, frame.Frame{Type: typ, Payload: raw})
}

func readJSONFrame[T any](r io.Reader, typ byte, maxPayload int) (T, error) {
	var payload T
	raw, err := readRawFrame(r, typ, maxPayload)
	if err != nil {
		return payload, err
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func readRawFrame(r io.Reader, typ byte, maxPayload int) ([]byte, error) {
	got, err := frame.Read(r, maxPayload)
	if err != nil {
		return nil, err
	}
	if got.Type != typ {
		return nil, fmt.Errorf("%w: type=0x%02x want=0x%02x payload_len=%d", ErrUnexpectedFrame, got.Type, typ, len(got.Payload))
	}
	return got.Payload, nil
}
