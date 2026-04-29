package pairtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"

	"yuanbohan/tunnel/internal/connectivity/pairing"
)

type AndroidClient struct {
	PrivateKey  ed25519.PrivateKey
	PublicKey   ed25519.PublicKey
	Fingerprint string
	DisplayName string
}

func NewAndroidClient(displayName string) (AndroidClient, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return AndroidClient{}, err
	}
	fingerprint, err := pairing.PublicKeyFingerprintHex(publicKey)
	if err != nil {
		return AndroidClient{}, err
	}
	return AndroidClient{
		PrivateKey:  privateKey,
		PublicKey:   publicKey,
		Fingerprint: fingerprint,
		DisplayName: displayName,
	}, nil
}

func (c AndroidClient) PairingResponse(invitation pairing.Invitation, accountID string) (pairing.AndroidResponse, string, error) {
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
