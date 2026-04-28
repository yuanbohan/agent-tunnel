package pairing

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	PublicKeySize = 32
	SASModulo     = 1_000_000
)

var (
	ErrInvalidPublicKeyLength = errors.New("invalid public key length")
	ErrSASInputTooLong        = errors.New("sas input too long")
)

func ComputeSAS(daemonPub, androidPub []byte, invitationID string, nonce []byte) (string, error) {
	if len(daemonPub) != PublicKeySize || len(androidPub) != PublicKeySize {
		return "", ErrInvalidPublicKeyLength
	}

	canonical, err := CanonicalSASInput(daemonPub, androidPub, invitationID, nonce)
	if err != nil {
		return "", err
	}

	digest := sha256.Sum256(canonical)
	short := binary.BigEndian.Uint32(digest[:4]) % SASModulo
	return fmt.Sprintf("%06d", short), nil
}

func CanonicalSASInput(daemonPub, androidPub []byte, invitationID string, nonce []byte) ([]byte, error) {
	out := make([]byte, 0, 2+len(daemonPub)+2+len(androidPub)+2+len(invitationID)+2+len(nonce))
	var err error
	if out, err = appendLengthPrefixed(out, daemonPub); err != nil {
		return nil, err
	}
	if out, err = appendLengthPrefixed(out, androidPub); err != nil {
		return nil, err
	}
	if out, err = appendLengthPrefixed(out, []byte(invitationID)); err != nil {
		return nil, err
	}
	if out, err = appendLengthPrefixed(out, nonce); err != nil {
		return nil, err
	}
	return out, nil
}

func appendLengthPrefixed(out, value []byte) ([]byte, error) {
	if len(value) > 0xffff {
		return nil, ErrSASInputTooLong
	}

	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(value)))
	out = append(out, length[:]...)
	out = append(out, value...)
	return out, nil
}
