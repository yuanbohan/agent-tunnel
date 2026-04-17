package daemon

import "testing"

func TestReadOrCreateDeviceIdentityPersistsStableID(t *testing.T) {
	paths := testPaths(t)

	first, err := ReadOrCreateDeviceIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateDeviceIdentity returned error: %v", err)
	}
	second, err := ReadOrCreateDeviceIdentity(paths)
	if err != nil {
		t.Fatalf("ReadOrCreateDeviceIdentity returned error on second read: %v", err)
	}
	if first.DeviceID == "" {
		t.Fatal("DeviceID = empty, want stable generated identifier")
	}
	if second.DeviceID != first.DeviceID {
		t.Fatalf("DeviceID changed from %q to %q", first.DeviceID, second.DeviceID)
	}
}
