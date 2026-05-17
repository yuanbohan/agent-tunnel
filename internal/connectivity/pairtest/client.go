package pairtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"

	"yuanbohan/tunnel/internal/connectivity/pairing"
)

type Client struct {
	PrivateKey  ed25519.PrivateKey
	PublicKey   ed25519.PublicKey
	Fingerprint string
	DisplayName string
}

func NewClient(displayName string) (Client, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Client{}, err
	}
	fingerprint, err := pairing.PublicKeyFingerprintHex(publicKey)
	if err != nil {
		return Client{}, err
	}
	return Client{
		PrivateKey:  privateKey,
		PublicKey:   publicKey,
		Fingerprint: fingerprint,
		DisplayName: displayName,
	}, nil
}

func (c Client) PairingResponse(invitation pairing.Invitation, accountID string) (pairing.AndroidResponse, string, error) {
	response, err := pairing.SignAndroidResponse(pairing.AndroidResponse{
		Version:            pairing.Version,
		AccountID:          accountID,
		InvitationID:       invitation.InvitationID,
		CorrelationID:      invitation.CorrelationID,
		AndroidPublicKey:   hex.EncodeToString(c.PublicKey),
		AndroidFingerprint: c.Fingerprint,
		AndroidDisplayName: c.DisplayName,
	}, c.PrivateKey)
	if err != nil {
		return pairing.AndroidResponse{}, "", err
	}
	verified, err := pairing.VerifyPairingResponse(invitation, response, 0)
	if err != nil {
		return pairing.AndroidResponse{}, "", err
	}
	return response, verified.SAS, nil
}
