package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
	"yuanbohan/tunnel/internal/connectivity/pairtest"
)

func TestPairInvitationPersistsAndKeepsConsumedUntilExpiry(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	now := time.Date(2026, time.April, 28, 12, 0, 0, 0, time.UTC)

	invitation, err := CreatePairInvitation(paths, PairInvitationOptions{
		BaseURL:        "https://relay.example.com",
		DeviceID:       "dev_test",
		DisplayName:    "Test Mac",
		Now:            now,
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("CreatePairInvitation returned error: %v", err)
	}
	if invitation.InvitationID == "" || invitation.CorrelationID == "" || invitation.Nonce == "" {
		t.Fatalf("invitation = %#v, want ids and nonce", invitation)
	}
	if invitation.DaemonFingerprint != identity.Fingerprint {
		t.Fatalf("DaemonFingerprint = %q, want %q", invitation.DaemonFingerprint, identity.Fingerprint)
	}

	consumed, err := ConsumePairInvitation(paths, invitation.InvitationID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ConsumePairInvitation returned error: %v", err)
	}
	if consumed.ConsumedAt == nil {
		t.Fatal("ConsumedAt is nil, want consumed invitation retained")
	}

	_, err = CreatePairInvitation(paths, PairInvitationOptions{
		BaseURL:        "https://relay.example.com",
		DeviceID:       "dev_test",
		Now:            now.Add(PairingInvitationTTL + time.Second),
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("second CreatePairInvitation returned error: %v", err)
	}
	state, err := LoadPairingState(paths)
	if err != nil {
		t.Fatalf("LoadPairingState returned error: %v", err)
	}
	for _, record := range state.Invitations {
		if record.InvitationID == invitation.InvitationID {
			t.Fatalf("expired invitation %s was not swept", invitation.InvitationID)
		}
	}

	info, err := os.Stat(paths.PairingStateFile)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("pairing state file mode = %o, want 0600", mode)
	}
}

func TestPairingStatePersistsTrustedRosterAndRevocation(t *testing.T) {
	paths := testPaths(t)
	fingerprint := strings.Repeat("a", 64)
	if err := UpsertTrustedAndroidDevice(paths, TrustedAndroidDevice{
		Fingerprint: fingerprint,
		PublicKey:   strings.Repeat("b", 64),
		DisplayName: "Android Phone",
		PairedAt:    100,
	}); err != nil {
		t.Fatalf("UpsertTrustedAndroidDevice returned error: %v", err)
	}

	devices, err := ListTrustedAndroidDevices(paths)
	if err != nil {
		t.Fatalf("ListTrustedAndroidDevices returned error: %v", err)
	}
	if len(devices) != 1 || devices[0].Fingerprint != fingerprint {
		t.Fatalf("devices = %#v, want trusted android device", devices)
	}

	revoked, err := RevokeTrustedAndroidDevice(paths, fingerprint)
	if err != nil {
		t.Fatalf("RevokeTrustedAndroidDevice returned error: %v", err)
	}
	if revoked.Status != TrustedDeviceStatusRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked = %#v, want revoked status", revoked)
	}

	devices, err = ListTrustedAndroidDevices(paths)
	if err != nil {
		t.Fatalf("ListTrustedAndroidDevices after revoke returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("devices = %#v, want revoked device hidden", devices)
	}
}

func TestLoadPairingStateMigratesLegacyClientKeys(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	legacyFingerprint := strings.Repeat("a", 64)
	legacyPayload := `{
  "version": 1,
  "invitations": [
    {
      "invitation_id": "pair-1",
      "correlation_id": "corr-1",
      "nonce": "abcd",
      "account_id": "123",
      "relay_base_url": "https://relay.example.com",
      "device_id": "dev-1",
      "daemon_fingerprint": "` + strings.Repeat("b", 64) + `",
      "expires_at": 1775376000,
      "created_at": 1775375700
    }
  ],
  "pending_responses": [
    {
      "invitation_id": "pair-1",
      "correlation_id": "corr-1",
      "account_id": "123",
      "android_pubkey": "` + strings.Repeat("c", 64) + `",
      "android_fingerprint": "` + legacyFingerprint + `",
      "android_display_name": "Pixel",
      "signature": "` + strings.Repeat("d", 128) + `",
      "sas": "123456",
      "received_at": 1775375710,
      "expires_at": 1775376000
    }
  ],
  "trusted_devices": [
    {
      "fingerprint": "` + legacyFingerprint + `",
      "public_key": "` + strings.Repeat("e", 64) + `",
      "display_name": "Pixel",
      "account_id": "123",
      "status": "trusted",
      "paired_at": 1775375720
    }
  ]
}`
	if err := os.WriteFile(paths.PairingStateFile, []byte(legacyPayload), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	state, err := LoadPairingState(paths)
	if err != nil {
		t.Fatalf("LoadPairingState returned error: %v", err)
	}
	if state.Version != PairingStateVersion {
		t.Fatalf("Version = %d, want %d", state.Version, PairingStateVersion)
	}
	if len(state.TrustedDevices) != 1 || state.TrustedDevices[0].Fingerprint != legacyFingerprint {
		t.Fatalf("TrustedDevices = %#v, want migrated trusted client", state.TrustedDevices)
	}
	if len(state.Invitations) != 1 || state.Invitations[0].DaemonFingerprint != strings.Repeat("b", 64) {
		t.Fatalf("Invitations = %#v, want migrated daemon fingerprint", state.Invitations)
	}
	if len(state.PendingResponses) != 1 ||
		state.PendingResponses[0].AndroidPublicKey != strings.Repeat("c", 64) ||
		state.PendingResponses[0].AndroidFingerprint != legacyFingerprint ||
		state.PendingResponses[0].AndroidDisplayName != "Pixel" {
		t.Fatalf("PendingResponses = %#v, want migrated pending response", state.PendingResponses)
	}

	if err := SavePairingState(paths, state); err != nil {
		t.Fatalf("SavePairingState returned error: %v", err)
	}
	payload, err := os.ReadFile(paths.PairingStateFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(payload), `"trusted_clients"`) || strings.Contains(string(payload), `"trusted_devices"`) {
		t.Fatalf("saved payload = %s, want rewritten client-neutral keys", string(payload))
	}
}

func TestRevokeTrustedAndroidDeviceRejectsUnknownFingerprint(t *testing.T) {
	paths := testPaths(t)

	_, err := RevokeTrustedAndroidDevice(paths, strings.Repeat("c", 64))
	if !errors.Is(err, ErrTrustedDeviceNotFound) {
		t.Fatalf("RevokeTrustedAndroidDevice error = %v, want ErrTrustedDeviceNotFound", err)
	}
}

func TestCompletePairingResponsePersistsTrustAfterSASMatch(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	now := time.Date(2026, time.April, 28, 12, 0, 0, 0, time.UTC)
	invitation, err := CreatePairInvitation(paths, PairInvitationOptions{
		BaseURL:        "https://relay.example.com",
		DeviceID:       "dev_test",
		AccountID:      "acct-1",
		Now:            now,
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("CreatePairInvitation returned error: %v", err)
	}

	androidPub, androidPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	androidFingerprint, err := connectivitypairing.PublicKeyFingerprintHex(androidPub)
	if err != nil {
		t.Fatalf("PublicKeyFingerprintHex returned error: %v", err)
	}
	response, err := connectivitypairing.SignAndroidResponse(connectivitypairing.AndroidResponse{
		AccountID:          "acct-1",
		InvitationID:       invitation.InvitationID,
		CorrelationID:      invitation.CorrelationID,
		AndroidPublicKey:   hex.EncodeToString(androidPub),
		AndroidFingerprint: androidFingerprint,
		AndroidDisplayName: "Pixel",
	}, androidPriv)
	if err != nil {
		t.Fatalf("SignAndroidResponse returned error: %v", err)
	}
	verified, err := connectivitypairing.VerifyPairingResponse(connectivitypairing.Invitation{
		Version:           connectivitypairing.Version,
		AccountID:         invitation.AccountID,
		DaemonID:          invitation.DeviceID,
		DaemonPublicKey:   invitation.DaemonPublicKey,
		DaemonFingerprint: invitation.DaemonFingerprint,
		InvitationID:      invitation.InvitationID,
		CorrelationID:     invitation.CorrelationID,
		Nonce:             invitation.Nonce,
		ExpiresAt:         invitation.ExpiresAt,
		RelayBaseURL:      invitation.RelayBaseURL,
		Signature:         invitation.Signature,
	}, response, now.Add(time.Minute).Unix())
	if err != nil {
		t.Fatalf("VerifyPairingResponse returned error: %v", err)
	}

	completed, err := CompletePairingResponse(paths, response, verified.SAS, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CompletePairingResponse returned error: %v", err)
	}
	if completed.Device.Fingerprint != androidFingerprint || completed.Device.DisplayName != "Pixel" {
		t.Fatalf("completed = %#v, want trusted Pixel", completed)
	}

	devices, err := ListTrustedAndroidDevices(paths)
	if err != nil {
		t.Fatalf("ListTrustedAndroidDevices returned error: %v", err)
	}
	if len(devices) != 1 || devices[0].Fingerprint != androidFingerprint {
		t.Fatalf("devices = %#v, want paired android", devices)
	}
}

func TestCompletePairingResponseConsumesInvitationOnSASMismatch(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	now := time.Date(2026, time.April, 28, 12, 0, 0, 0, time.UTC)
	invitation, err := CreatePairInvitation(paths, PairInvitationOptions{
		BaseURL:        "https://relay.example.com",
		DeviceID:       "dev_test",
		AccountID:      "acct-1",
		Now:            now,
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("CreatePairInvitation returned error: %v", err)
	}
	androidPub, androidPriv, _ := ed25519.GenerateKey(rand.Reader)
	androidFingerprint, _ := connectivitypairing.PublicKeyFingerprintHex(androidPub)
	response, err := connectivitypairing.SignAndroidResponse(connectivitypairing.AndroidResponse{
		AccountID:          "acct-1",
		InvitationID:       invitation.InvitationID,
		CorrelationID:      invitation.CorrelationID,
		AndroidPublicKey:   hex.EncodeToString(androidPub),
		AndroidFingerprint: androidFingerprint,
	}, androidPriv)
	if err != nil {
		t.Fatalf("SignAndroidResponse returned error: %v", err)
	}

	_, err = CompletePairingResponse(paths, response, "000000", now.Add(time.Minute))
	if !errors.Is(err, ErrPairingSASMismatch) {
		t.Fatalf("CompletePairingResponse error = %v, want ErrPairingSASMismatch", err)
	}
	_, err = CompletePairingResponse(paths, response, "000000", now.Add(2*time.Minute))
	if !errors.Is(err, ErrPairingInvitationConsumed) {
		t.Fatalf("second CompletePairingResponse error = %v, want ErrPairingInvitationConsumed", err)
	}
}

func TestPendingPairingResponseRequiresSASConfirmation(t *testing.T) {
	paths := testPaths(t)
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	now := time.Date(2026, time.April, 28, 12, 0, 0, 0, time.UTC)
	invitation, err := CreatePairInvitation(paths, PairInvitationOptions{
		BaseURL:        "https://relay.example.com",
		DeviceID:       "dev_test",
		DisplayName:    "Test Mac",
		AccountID:      "acct-1",
		CorrelationID:  "corr-test",
		Now:            now,
		DaemonIdentity: identity,
	})
	if err != nil {
		t.Fatalf("CreatePairInvitation returned error: %v", err)
	}
	android, err := pairtest.NewClient("Pixel")
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	response, sas, err := android.PairingResponse(connectivitypairing.Invitation{
		Version:           connectivitypairing.Version,
		AccountID:         invitation.AccountID,
		DaemonID:          invitation.DeviceID,
		DaemonDisplayName: invitation.DisplayName,
		DaemonPublicKey:   invitation.DaemonPublicKey,
		DaemonFingerprint: invitation.DaemonFingerprint,
		InvitationID:      invitation.InvitationID,
		CorrelationID:     invitation.CorrelationID,
		Nonce:             invitation.Nonce,
		ExpiresAt:         invitation.ExpiresAt,
		RelayBaseURL:      invitation.RelayBaseURL,
		Signature:         invitation.Signature,
	}, "acct-1")
	if err != nil {
		t.Fatalf("PairingResponse returned error: %v", err)
	}

	pending, err := StorePendingPairingResponse(paths, response, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("StorePendingPairingResponse returned error: %v", err)
	}
	if pending.SAS != sas || pending.AndroidFingerprint != android.Fingerprint {
		t.Fatalf("pending = %#v, want SAS %s for Android", pending, sas)
	}
	devices, err := ListTrustedAndroidDevices(paths)
	if err != nil {
		t.Fatalf("ListTrustedAndroidDevices returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("devices = %#v, want pending response not trusted yet", devices)
	}

	completed, err := ConfirmPendingPairingResponse(paths, invitation.InvitationID, sas, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ConfirmPendingPairingResponse returned error: %v", err)
	}
	if completed.Device.Fingerprint != android.Fingerprint {
		t.Fatalf("completed = %#v, want Android trusted", completed)
	}
	pendingList, err := ListPendingPairingResponses(paths)
	if err != nil {
		t.Fatalf("ListPendingPairingResponses returned error: %v", err)
	}
	if len(pendingList) != 0 {
		t.Fatalf("pendingList = %#v, want completion to clear pending response", pendingList)
	}
}
