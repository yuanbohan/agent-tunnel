package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connectivitypairing "yuanbohan/tunnel/internal/connectivity/pairing"
	"yuanbohan/tunnel/internal/connectivity/pairtest"
	"yuanbohan/tunnel/internal/protocol"
)

func TestRunWritesStatusAndAnswersControlRequests(t *testing.T) {
	paths := testPaths(t)
	oldTmuxLookPath := tmuxLookPathFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})

	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	readOrCreateDeviceIdentityFn = func(Paths) (DeviceIdentity, error) {
		return DeviceIdentity{DeviceID: "dev_test"}, nil
	}
	collectDeviceMetadataFn = func() DeviceMetadata {
		return DeviceMetadata{
			DisplayName:    "Test Device",
			Hostname:       "test-host",
			PlatformFamily: PlatformFamilyMacOS,
			PlatformID:     PlatformFamilyMacOS,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyReader, readyWriter := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, RuntimeOptions{
			Paths:     paths,
			BaseURL:   "https://relay.example.com",
			AuthToken: "token",
		}, readyWriter)
	}()

	buffer := make([]byte, 16)
	if _, err := readyReader.Read(buffer); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ready pipe read returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err := Status(context.Background(), paths)
		if err == nil {
			if !status.Running || status.DeviceID != "dev_test" || status.WorkspaceBackend != workspaceBackendTmux {
				t.Fatalf("status = %#v, want running daemon status", status)
			}
			if status.AuthContextFingerprint != AuthContextFingerprint("token") {
				t.Fatalf("AuthContextFingerprint = %q, want token fingerprint", status.AuthContextFingerprint)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Status never succeeded: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := Stop(context.Background(), paths); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	persisted, err := LoadStatus(paths)
	if err != nil {
		t.Fatalf("LoadStatus returned error after stop: %v", err)
	}
	if persisted.Running || persisted.RelayConnected {
		t.Fatalf("persisted status = %#v, want stopped state persisted before stop returns", persisted)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after stop request")
	}
}

func TestRunStartsWithoutTmuxAndReportsDegradedLaunchHealth(t *testing.T) {
	paths := testPaths(t)
	oldTmuxLookPath := tmuxLookPathFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})
	tmuxLookPathFn = func(string) (string, error) {
		return "", errors.New("not found")
	}
	readOrCreateDeviceIdentityFn = func(Paths) (DeviceIdentity, error) {
		return DeviceIdentity{DeviceID: "dev_tmuxless"}, nil
	}
	collectDeviceMetadataFn = func() DeviceMetadata {
		return DeviceMetadata{DisplayName: "No Tmux", Hostname: "no-tmux", PlatformFamily: PlatformFamilyLinux, PlatformID: "linux"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyReader, readyWriter := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, RuntimeOptions{
			Paths:     paths,
			BaseURL:   "https://relay.example.com",
			AuthToken: "token",
		}, readyWriter)
	}()

	buffer := make([]byte, 16)
	if _, err := readyReader.Read(buffer); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ready pipe read returned error: %v", err)
	}
	status, err := Status(context.Background(), paths)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if !status.Running || status.LaunchHealth != LaunchHealthDegraded || status.LastFailure != "tmux_not_found" {
		t.Fatalf("status = %#v, want running degraded tmux_not_found daemon", status)
	}
	if err := Stop(context.Background(), paths); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after stop request")
	}
}

func TestRunCleansUpPersistedStateOnSocketStartupFailure(t *testing.T) {
	paths := testPaths(t)
	paths.SocketPath = filepath.Join(paths.RuntimeDir, "missing", "daemon.sock")
	oldTmuxLookPath := tmuxLookPathFn
	oldReadIdentity := readOrCreateDeviceIdentityFn
	oldCollectMetadata := collectDeviceMetadataFn
	t.Cleanup(func() {
		tmuxLookPathFn = oldTmuxLookPath
		readOrCreateDeviceIdentityFn = oldReadIdentity
		collectDeviceMetadataFn = oldCollectMetadata
	})
	tmuxLookPathFn = func(string) (string, error) { return "/usr/bin/tmux", nil }
	readOrCreateDeviceIdentityFn = func(Paths) (DeviceIdentity, error) { return DeviceIdentity{DeviceID: "dev_test"}, nil }
	collectDeviceMetadataFn = func() DeviceMetadata {
		return DeviceMetadata{DisplayName: "Test Device", Hostname: "test-host", PlatformFamily: PlatformFamilyLinux, PlatformID: "ubuntu"}
	}

	err := Run(context.Background(), RuntimeOptions{
		Paths:     paths,
		BaseURL:   "https://relay.example.com",
		AuthToken: "token",
	}, nil)
	if err == nil {
		t.Fatal("Run error = nil, want socket startup failure")
	}

	status, loadErr := LoadStatus(paths)
	if loadErr != nil {
		t.Fatalf("LoadStatus returned error: %v", loadErr)
	}
	if status.Running {
		t.Fatalf("status = %#v, want stopped persisted state after startup failure", status)
	}
	if _, statErr := os.Stat(paths.PIDFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("PID file stat error = %v, want not exists", statErr)
	}
}

func TestCurrentRunningStatusIgnoresLivePIDWhenControlSocketUnavailable(t *testing.T) {
	paths := testPaths(t)
	if err := writeJSONFile(paths.StatusFile, StatusInfo{
		Running: true,
		PID:     os.Getpid(),
	}); err != nil {
		t.Fatalf("writeJSONFile returned error: %v", err)
	}

	status, err := currentRunningStatus(context.Background(), paths)
	if err != nil {
		t.Fatalf("currentRunningStatus returned error: %v", err)
	}
	if status.Running {
		t.Fatalf("status = %#v, want stopped when control socket is unavailable", status)
	}
}

func TestSendConnectivityEventBlockingWaitsForConsumer(t *testing.T) {
	state := &runtimeState{
		connectivityEvents: make(chan protocol.ConnectivityFrame),
	}
	received := make(chan protocol.ConnectivityFrame, 1)
	go func() {
		received <- <-state.connectivityEvents
	}()

	frame := protocol.ConnectivityFrame{
		Type:               "client_revoked",
		AndroidFingerprint: "android-a",
	}
	if !state.sendConnectivityEventBlocking(context.Background(), frame) {
		t.Fatal("sendConnectivityEventBlocking returned false with an active consumer")
	}
	select {
	case got := <-received:
		if got.Type != frame.Type || got.AndroidFingerprint != frame.AndroidFingerprint {
			t.Fatalf("received frame = %#v, want %#v", got, frame)
		}
	case <-time.After(time.Second):
		t.Fatal("consumer did not receive connectivity event")
	}
}

func TestSendConnectivityEventBlockingReturnsFalseWhenCanceled(t *testing.T) {
	state := &runtimeState{
		connectivityEvents: make(chan protocol.ConnectivityFrame),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if state.sendConnectivityEventBlocking(ctx, protocol.ConnectivityPairCompletedFrame("android-a")) {
		t.Fatal("sendConnectivityEventBlocking returned true after context cancellation")
	}
}

func TestConfirmPendingPairingReturnsCompletionWhenConnectivityQueueUnavailable(t *testing.T) {
	paths := testPaths(t)
	invitation, sas, fingerprint := storeRuntimeTestPendingPairing(t, paths)
	state := &runtimeState{}

	response := handleConfirmPendingPairing(context.Background(), paths, state, Request{
		InvitationID: invitation.InvitationID,
		SAS:          sas,
	})
	if response.Error != "" {
		t.Fatalf("response error = %q, want pairing completion", response.Error)
	}
	if response.PairCompletion == nil {
		t.Fatal("PairCompletion is nil, want trusted pairing completion")
	}
	if response.PairCompletion.Device.Fingerprint != fingerprint {
		t.Fatalf("completion fingerprint = %q, want %q", response.PairCompletion.Device.Fingerprint, fingerprint)
	}
	if response.PairCompletion.Warning != connectivityEventQueueWarning {
		t.Fatalf("completion warning = %q, want queue warning", response.PairCompletion.Warning)
	}
	devices, err := ListTrustedAndroidDevices(paths)
	if err != nil {
		t.Fatalf("ListTrustedAndroidDevices returned error: %v", err)
	}
	if len(devices) != 1 || devices[0].Fingerprint != fingerprint {
		t.Fatalf("trusted devices = %#v, want completed pairing persisted", devices)
	}
}

func TestRevokeTrustedDeviceReturnsRevokedDeviceWhenConnectivityQueueUnavailable(t *testing.T) {
	paths := testPaths(t)
	fingerprint := strings.Repeat("a", 64)
	if err := UpsertTrustedAndroidDevice(paths, TrustedAndroidDevice{
		Fingerprint: fingerprint,
		PublicKey:   strings.Repeat("b", 64),
		DisplayName: "Pixel",
		PairedAt:    time.Now().UTC().Unix(),
	}); err != nil {
		t.Fatalf("UpsertTrustedAndroidDevice returned error: %v", err)
	}
	state := &runtimeState{}

	response := handleRevokeTrustedDevice(context.Background(), paths, state, Request{DeviceFingerprint: fingerprint})
	if response.Error != "" {
		t.Fatalf("response error = %q, want revoked device", response.Error)
	}
	if response.TrustedDevice == nil {
		t.Fatal("TrustedDevice is nil, want revoked device")
	}
	if response.TrustedDevice.Fingerprint != fingerprint {
		t.Fatalf("revoked fingerprint = %q, want %q", response.TrustedDevice.Fingerprint, fingerprint)
	}
	if response.TrustedDevice.Warning != connectivityEventQueueWarning {
		t.Fatalf("revoked warning = %q, want queue warning", response.TrustedDevice.Warning)
	}
	devices, err := ListTrustedAndroidDevices(paths)
	if err != nil {
		t.Fatalf("ListTrustedAndroidDevices returned error: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("trusted devices = %#v, want revoked device removed locally", devices)
	}
}

func storeRuntimeTestPendingPairing(t *testing.T, paths Paths) (PairInvitation, string, string) {
	t.Helper()
	identity, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	now := time.Now().UTC()
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
	if _, err := StorePendingPairingResponse(paths, response, now.Add(time.Minute)); err != nil {
		t.Fatalf("StorePendingPairingResponse returned error: %v", err)
	}
	return invitation, sas, android.Fingerprint
}

func TestAcquireStartupLockWaitsForRelease(t *testing.T) {
	paths := testPaths(t)
	if err := EnsureRuntimeDirs(paths); err != nil {
		t.Fatalf("EnsureRuntimeDirs returned error: %v", err)
	}
	oldTimeout := daemonStartupLockTimeout
	daemonStartupLockTimeout = 2 * time.Second
	t.Cleanup(func() {
		daemonStartupLockTimeout = oldTimeout
	})

	releaseFirst, err := acquireStartupLock(context.Background(), paths)
	if err != nil {
		t.Fatalf("first acquireStartupLock returned error: %v", err)
	}
	acquired := make(chan func(), 1)
	errCh := make(chan error, 1)
	go func() {
		release, err := acquireStartupLock(context.Background(), paths)
		if err != nil {
			errCh <- err
			return
		}
		acquired <- release
	}()

	select {
	case release := <-acquired:
		release()
		t.Fatal("second acquireStartupLock succeeded before first release")
	case err := <-errCh:
		t.Fatalf("second acquireStartupLock returned error before release: %v", err)
	case <-time.After(120 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case err := <-errCh:
		t.Fatalf("second acquireStartupLock returned error after release: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second acquireStartupLock did not acquire after release")
	}
}
