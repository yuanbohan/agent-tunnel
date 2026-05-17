package pairing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

func TestSignedInvitationAndAndroidResponseVerify(t *testing.T) {
	daemonPub, daemonPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey daemon returned error: %v", err)
	}
	androidPub, androidPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey android returned error: %v", err)
	}
	daemonFingerprint, err := PublicKeyFingerprintHex(daemonPub)
	if err != nil {
		t.Fatalf("PublicKeyFingerprintHex daemon returned error: %v", err)
	}
	androidFingerprint, err := PublicKeyFingerprintHex(androidPub)
	if err != nil {
		t.Fatalf("PublicKeyFingerprintHex android returned error: %v", err)
	}

	invitation, err := SignInvitation(Invitation{
		AccountID:         "acct-1",
		DaemonID:          "dev-1",
		DaemonDisplayName: "Test Mac",
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
	if err := VerifyInvitation(invitation); err != nil {
		t.Fatalf("VerifyInvitation returned error: %v", err)
	}

	response, err := SignAndroidResponse(AndroidResponse{
		AccountID:          "acct-1",
		InvitationID:       "pair-1",
		CorrelationID:      "corr-1",
		AndroidPublicKey:   hex.EncodeToString(androidPub),
		AndroidFingerprint: androidFingerprint,
		AndroidDisplayName: "Pixel",
	}, androidPriv)
	if err != nil {
		t.Fatalf("SignAndroidResponse returned error: %v", err)
	}
	verified, err := VerifyPairingResponse(invitation, response, 99)
	if err != nil {
		t.Fatalf("VerifyPairingResponse returned error: %v", err)
	}
	if verified.SAS == "" || verified.AndroidFingerprint != androidFingerprint {
		t.Fatalf("verified = %#v, want SAS and android fingerprint", verified)
	}
}

func TestVerifyPairingResponseRejectsTamperedAccount(t *testing.T) {
	daemonPub, daemonPriv, _ := ed25519.GenerateKey(rand.Reader)
	androidPub, androidPriv, _ := ed25519.GenerateKey(rand.Reader)
	daemonFingerprint, _ := PublicKeyFingerprintHex(daemonPub)
	androidFingerprint, _ := PublicKeyFingerprintHex(androidPub)

	invitation, err := SignInvitation(Invitation{
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
	response, err := SignAndroidResponse(AndroidResponse{
		AccountID:          "acct-2",
		InvitationID:       "pair-1",
		CorrelationID:      "corr-1",
		AndroidPublicKey:   hex.EncodeToString(androidPub),
		AndroidFingerprint: androidFingerprint,
	}, androidPriv)
	if err != nil {
		t.Fatalf("SignAndroidResponse returned error: %v", err)
	}

	_, err = VerifyPairingResponse(invitation, response, 99)
	if !errors.Is(err, ErrTranscriptMismatch) {
		t.Fatalf("VerifyPairingResponse error = %v, want ErrTranscriptMismatch", err)
	}
}

func TestVerifyPairingResponseAcceptsRelayAssertedAccountWhenInvitationIsUnbound(t *testing.T) {
	daemonPub, daemonPriv, _ := ed25519.GenerateKey(rand.Reader)
	androidPub, androidPriv, _ := ed25519.GenerateKey(rand.Reader)
	daemonFingerprint, _ := PublicKeyFingerprintHex(daemonPub)
	androidFingerprint, _ := PublicKeyFingerprintHex(androidPub)

	invitation, err := SignInvitation(Invitation{
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
	response, err := SignAndroidResponse(AndroidResponse{
		AccountID:          "acct-1",
		InvitationID:       "pair-1",
		CorrelationID:      "corr-1",
		AndroidPublicKey:   hex.EncodeToString(androidPub),
		AndroidFingerprint: androidFingerprint,
	}, androidPriv)
	if err != nil {
		t.Fatalf("SignAndroidResponse returned error: %v", err)
	}

	if _, err := VerifyPairingResponse(invitation, response, 99); err != nil {
		t.Fatalf("VerifyPairingResponse returned error: %v", err)
	}
}

func TestVerifyInvitationRejectsSignatureTampering(t *testing.T) {
	daemonPub, daemonPriv, _ := ed25519.GenerateKey(rand.Reader)
	daemonFingerprint, _ := PublicKeyFingerprintHex(daemonPub)
	invitation, err := SignInvitation(Invitation{
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
	invitation.RelayBaseURL = "https://evil.example.com"
	if err := VerifyInvitation(invitation); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("VerifyInvitation error = %v, want ErrInvalidSignature", err)
	}
}
