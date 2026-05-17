package pairtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"yuanbohan/tunnel/internal/connectivity/pairing"
)

func TestClientSignsPairingResponse(t *testing.T) {
	daemonPub, daemonPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	daemonFingerprint, err := pairing.PublicKeyFingerprintHex(daemonPub)
	if err != nil {
		t.Fatalf("PublicKeyFingerprintHex returned error: %v", err)
	}
	invitation, err := pairing.SignInvitation(pairing.Invitation{
		Version:           pairing.Version,
		AccountID:         "acct-1",
		DaemonID:          "dev-1",
		DaemonPublicKey:   hex.EncodeToString(daemonPub),
		DaemonFingerprint: daemonFingerprint,
		InvitationID:      "pair-1",
		CorrelationID:     "corr-1",
		Nonce:             "000102030405060708090a0b0c0d0e0f",
		ExpiresAt:         100,
		RelayBaseURL:      "https://relay.example.com",
	}, daemonPriv)
	if err != nil {
		t.Fatalf("SignInvitation returned error: %v", err)
	}
	client, err := NewClient("Pixel")
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	response, sas, err := client.PairingResponse(invitation, "acct-1")
	if err != nil {
		t.Fatalf("PairingResponse returned error: %v", err)
	}
	if response.AndroidFingerprint != client.Fingerprint || response.AndroidDisplayName != "Pixel" {
		t.Fatalf("response = %#v, want Pixel fingerprint", response)
	}
	if sas == "" {
		t.Fatal("sas is empty")
	}
}
