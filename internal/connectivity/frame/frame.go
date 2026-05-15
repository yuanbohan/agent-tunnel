package frame

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// Frame type values mirror agent-tunnel-protocols:docs/protocol.md.
	TypeHello              byte = 0x01
	TypeSessionIndex       byte = 0x02
	TypePreviewSubscribe   byte = 0x03
	TypeSessionUpsert      byte = 0x04
	TypeSessionGone        byte = 0x05
	TypePreviewUnsubscribe byte = 0x06
	TypePreviewSnapshot    byte = 0x07
	TypeInteractiveRequest byte = 0x08
	TypeInteractiveGranted byte = 0x09
	TypeInteractiveDenied  byte = 0x0a
	TypeInteractiveRelease byte = 0x0b
	TypeInputText          byte = 0x0c
	TypeInputKey           byte = 0x0d
	TypeResize             byte = 0x0e
	TypePathState          byte = 0x0f
	TypeSnapshotBegin      byte = 0x10
	TypeSnapshotChunk      byte = 0x11
	TypeLiveBytes          byte = 0x12
	TypeSnapshotEnd        byte = 0x13
	TypeError              byte = 0x7f

	DefaultMaxPayload = 1 << 20
)

var (
	ErrTruncatedVarint = errors.New("truncated varint")
	ErrPayloadTooLarge = errors.New("payload too large")
	ErrInvalidVarint   = errors.New("invalid varint")
)

type Frame struct {
	Type    byte
	Payload []byte
}

func Encode(frame Frame) ([]byte, error) {
	length, err := encodeVarint(uint64(len(frame.Payload)))
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 1+len(length)+len(frame.Payload))
	out = append(out, frame.Type)
	out = append(out, length...)
	out = append(out, frame.Payload...)
	return out, nil
}

func Decode(raw []byte, maxPayload int) (Frame, int, error) {
	if len(raw) < 2 {
		return Frame{}, 0, ErrTruncatedVarint
	}

	payloadLen, varintLen, err := decodeVarint(raw[1:])
	if err != nil {
		return Frame{}, 0, err
	}
	if payloadLen > uint64(maxPayload) {
		return Frame{}, 0, ErrPayloadTooLarge
	}

	headerLen := 1 + varintLen
	totalLen := headerLen + int(payloadLen)
	if len(raw) < totalLen {
		return Frame{}, 0, io.ErrUnexpectedEOF
	}

	payload := append([]byte(nil), raw[headerLen:totalLen]...)
	return Frame{Type: raw[0], Payload: payload}, totalLen, nil
}

func Write(w io.Writer, frame Frame) error {
	raw, err := Encode(frame)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func Read(r io.Reader, maxPayload int) (Frame, error) {
	var header [9]byte
	if _, err := io.ReadFull(r, header[:2]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Frame{}, ErrTruncatedVarint
		}
		return Frame{}, err
	}

	varintLen := varintEncodedLen(header[1])
	if varintLen > 1 {
		if _, err := io.ReadFull(r, header[2:1+varintLen]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return Frame{}, ErrTruncatedVarint
			}
			return Frame{}, err
		}
	}

	payloadLen, _, err := decodeVarint(header[1 : 1+varintLen])
	if err != nil {
		return Frame{}, err
	}
	if payloadLen > uint64(maxPayload) {
		return Frame{}, ErrPayloadTooLarge
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Frame{}, io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	return Frame{Type: header[0], Payload: payload}, nil
}

func encodeVarint(value uint64) ([]byte, error) {
	switch {
	case value <= 63:
		return []byte{byte(value)}, nil
	case value <= 16_383:
		var out [2]byte
		binary.BigEndian.PutUint16(out[:], uint16(value)|0x4000)
		return out[:], nil
	case value <= 1_073_741_823:
		var out [4]byte
		binary.BigEndian.PutUint32(out[:], uint32(value)|0x80000000)
		return out[:], nil
	case value <= 4_611_686_018_427_387_903:
		var out [8]byte
		binary.BigEndian.PutUint64(out[:], value|0xc000000000000000)
		return out[:], nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrInvalidVarint, value)
	}
}

func decodeVarint(raw []byte) (uint64, int, error) {
	if len(raw) == 0 {
		return 0, 0, ErrTruncatedVarint
	}

	encodedLen := varintEncodedLen(raw[0])
	if len(raw) < encodedLen {
		return 0, 0, ErrTruncatedVarint
	}

	switch encodedLen {
	case 1:
		return uint64(raw[0] & 0x3f), 1, nil
	case 2:
		return uint64(binary.BigEndian.Uint16(raw[:2]) & 0x3fff), 2, nil
	case 4:
		return uint64(binary.BigEndian.Uint32(raw[:4]) & 0x3fffffff), 4, nil
	case 8:
		return binary.BigEndian.Uint64(raw[:8]) & 0x3fffffffffffffff, 8, nil
	default:
		return 0, 0, ErrInvalidVarint
	}
}

func varintEncodedLen(first byte) int {
	return 1 << (first >> 6)
}
