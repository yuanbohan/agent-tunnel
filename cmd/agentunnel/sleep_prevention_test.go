package main

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestSleepPreventionStopIsIdempotent(t *testing.T) {
	calls := 0
	sleep := newSleepPrevention(sleepPreventionActive, func() {
		calls++
	})

	sleep.Stop()
	sleep.Stop()

	if calls != 1 {
		t.Fatalf("stop calls = %d, want 1", calls)
	}
}

func TestStartSleepPreventionForProcessUsesCaffeinateOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only test")
	}

	oldNewSleepCommand := newSleepCommand
	t.Cleanup(func() {
		newSleepCommand = oldNewSleepCommand
	})

	var gotName string
	var gotArgs []string
	newSleepCommand = func(name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return exec.Command("/bin/sh", "-c", "sleep 30")
	}

	sleep := startSleepPreventionForProcess(4242)
	t.Cleanup(func() {
		sleep.Stop()
	})

	if sleep.status != sleepPreventionActive {
		t.Fatalf("status = %q, want %q", sleep.status, sleepPreventionActive)
	}
	if gotName != "/usr/bin/caffeinate" {
		t.Fatalf("command = %q, want /usr/bin/caffeinate", gotName)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "-i" || gotArgs[1] != "-w" || gotArgs[2] != "4242" {
		t.Fatalf("args = %#v, want [-i -w 4242]", gotArgs)
	}
}

func TestStartSleepPreventionForProcessReturnsFailedStatusWhenHelperCannotStartOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only test")
	}

	oldNewSleepCommand := newSleepCommand
	t.Cleanup(func() {
		newSleepCommand = oldNewSleepCommand
	})

	newSleepCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/path/that/does/not/exist")
	}

	sleep := startSleepPreventionForProcess(4242)

	if sleep.status != sleepPreventionFailed {
		t.Fatalf("status = %q, want %q", sleep.status, sleepPreventionFailed)
	}
	sleep.Stop()
}

func TestStartSleepPreventionForProcessReturnsUnsupportedOnNonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-darwin-only test")
	}

	sleep := startSleepPreventionForProcess(4242)

	if sleep.status != sleepPreventionUnsupported {
		t.Fatalf("status = %q, want %q", sleep.status, sleepPreventionUnsupported)
	}
	sleep.Stop()
}
