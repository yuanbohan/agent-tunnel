package protocol

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	AttachPacketTypeTerminalBytes byte = 0x01

	attachPacketHeaderSize   = 17
	attachPacketClientIDSize = 16
)

var (
	ErrAttachPacketTooShort     = errors.New("attach packet too short")
	ErrAttachPacketEmptyPayload = errors.New("attach packet empty payload")
	ErrAttachPacketUnknownType  = errors.New("attach packet unknown type")
	ErrAttachPacketBadClientID  = errors.New("attach packet invalid client id")
)

type AttachPacket struct {
	Type     byte
	ClientID string
	Payload  []byte
}

func EncodeTerminalBytesPacket(clientID string, payload []byte) ([]byte, error) {
	return EncodeAttachPacket(AttachPacket{
		Type:     AttachPacketTypeTerminalBytes,
		ClientID: clientID,
		Payload:  payload,
	})
}

func EncodeAttachPacket(packet AttachPacket) ([]byte, error) {
	if packet.Type != AttachPacketTypeTerminalBytes {
		return nil, fmt.Errorf("%w: 0x%02x", ErrAttachPacketUnknownType, packet.Type)
	}

	clientID, err := parseAttachClientID(packet.ClientID)
	if err != nil {
		return nil, err
	}

	raw := make([]byte, attachPacketHeaderSize+len(packet.Payload))
	raw[0] = packet.Type
	copy(raw[1:1+attachPacketClientIDSize], clientID[:])
	copy(raw[attachPacketHeaderSize:], packet.Payload)
	return raw, nil
}

func DecodeAttachPacket(raw []byte) (AttachPacket, error) {
	if len(raw) < attachPacketHeaderSize {
		return AttachPacket{}, ErrAttachPacketTooShort
	}
	if len(raw) == attachPacketHeaderSize {
		return AttachPacket{}, ErrAttachPacketEmptyPayload
	}
	if raw[0] != AttachPacketTypeTerminalBytes {
		return AttachPacket{}, fmt.Errorf("%w: 0x%02x", ErrAttachPacketUnknownType, raw[0])
	}

	var clientID [attachPacketClientIDSize]byte
	copy(clientID[:], raw[1:attachPacketHeaderSize])

	return AttachPacket{
		Type:     raw[0],
		ClientID: formatAttachClientID(clientID),
		Payload:  append([]byte(nil), raw[attachPacketHeaderSize:]...),
	}, nil
}

func parseAttachClientID(clientID string) ([attachPacketClientIDSize]byte, error) {
	var parsed [attachPacketClientIDSize]byte

	normalized := strings.ReplaceAll(strings.TrimSpace(clientID), "-", "")
	if len(normalized) != attachPacketClientIDSize*2 {
		return parsed, fmt.Errorf("%w: %q", ErrAttachPacketBadClientID, clientID)
	}

	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return parsed, fmt.Errorf("%w: %q", ErrAttachPacketBadClientID, clientID)
	}
	copy(parsed[:], decoded)
	return parsed, nil
}

func formatAttachClientID(clientID [attachPacketClientIDSize]byte) string {
	hexID := hex.EncodeToString(clientID[:])
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		hexID[0:8],
		hexID[8:12],
		hexID[12:16],
		hexID[16:20],
		hexID[20:32],
	)
}
