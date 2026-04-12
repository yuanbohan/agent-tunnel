package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsReadyPromptAndRepliesToCarriageReturn(t *testing.T) {
	var output bytes.Buffer
	if err := run(strings.NewReader("ping\r"), &output); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "READY e2e-launcher") {
		t.Fatalf("output = %q, want ready banner", got)
	}
	if !strings.Contains(got, "REPLY ping") {
		t.Fatalf("output = %q, want reply for ping", got)
	}
	if !strings.Contains(got, prompt) {
		t.Fatalf("output = %q, want prompt", got)
	}
}

func TestRunTreatsCRLFAsOneSubmit(t *testing.T) {
	var output bytes.Buffer
	if err := run(strings.NewReader("ping\r\n"), &output); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if got := strings.Count(output.String(), "REPLY ping"); got != 1 {
		t.Fatalf("reply count = %d, want 1", got)
	}
}

func TestRunPrintsEmptyForBlankSubmit(t *testing.T) {
	var output bytes.Buffer
	if err := run(strings.NewReader("\r"), &output); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(output.String(), "EMPTY") {
		t.Fatalf("output = %q, want EMPTY", output.String())
	}
}
