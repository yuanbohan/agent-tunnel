package daemon

import (
	"errors"
	"os"
	"testing"
)

func TestReadOrCreateConnectivityIdentityPersistsStableFingerprint(t *testing.T) {
	paths := testPaths(t)

	created, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	loaded, err := ReadOrCreateConnectivityIdentity(paths)
	if err != nil {
		t.Fatalf("second ReadOrCreateConnectivityIdentity returned error: %v", err)
	}
	if loaded.Fingerprint == "" || loaded.Fingerprint != created.Fingerprint {
		t.Fatalf("fingerprint = %q, want stable %q", loaded.Fingerprint, created.Fingerprint)
	}
	if loaded.CreatedAt != created.CreatedAt {
		t.Fatalf("CreatedAt = %d, want %d", loaded.CreatedAt, created.CreatedAt)
	}

	info, err := os.Stat(paths.ConnectivityIdentityFile)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("identity file mode = %o, want 0600", mode)
	}
}

func TestReadConnectivityIdentityRejectsMalformedFile(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.StateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConnectivityIdentityFile, []byte(`{"private_key":"bad"}`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	_, err := ReadOrCreateConnectivityIdentity(paths)
	if !errors.Is(err, ErrInvalidConnectivityIdentity) {
		t.Fatalf("ReadOrCreateConnectivityIdentity error = %v, want ErrInvalidConnectivityIdentity", err)
	}
}
