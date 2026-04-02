package relayserver

import "testing"

func TestExtractPreviewStripsANSIAndReturnsLatestTextLine(t *testing.T) {
	raw := []byte("\x1b[32mPASS\x1b[0m retry backoff updated\n")
	got, ok := ExtractPreview(raw)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got != "PASS retry backoff updated" {
		t.Fatalf("preview = %q, want PASS retry backoff updated", got)
	}
}

func TestExtractPreviewRejectsBlankOrControlOnlyOutput(t *testing.T) {
	if got, ok := ExtractPreview([]byte("\x1b[2K\r")); ok || got != "" {
		t.Fatalf("preview = %q, ok = %v, want empty/false", got, ok)
	}
}
