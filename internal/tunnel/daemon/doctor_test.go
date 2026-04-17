package daemon

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestBuildDoctorReportReturnsNonZeroForWarnOrFail(t *testing.T) {
	paths := testPaths(t)

	oldLoadRecipe := loadRecipeFn
	oldInferRecipe := inferRecipeFn
	oldLoadConfig := loadConfigFn
	oldLookPath := lookPathFn
	t.Cleanup(func() {
		loadRecipeFn = oldLoadRecipe
		inferRecipeFn = oldInferRecipe
		loadConfigFn = oldLoadConfig
		lookPathFn = oldLookPath
	})

	loadRecipeFn = func(Paths) (LauncherRecipe, error) {
		return LauncherRecipe{}, errors.New("missing")
	}
	inferRecipeFn = func() (LauncherRecipe, error) {
		return LauncherRecipe{}, errors.New("missing")
	}
	loadConfigFn = func(Paths) (Config, error) {
		return DefaultConfig(), nil
	}
	lookPathFn = func(string) (string, error) {
		return "/usr/bin/tunnel", nil
	}

	report := BuildDoctorReport(context.Background(), paths, StatusInfo{Running: false})
	if report.ExitCode() == 0 {
		t.Fatal("ExitCode() = 0, want non-zero when report contains warn/fail checks")
	}
}

func TestDoctorReturnsHealthyExitCodeWhenChecksAreOK(t *testing.T) {
	paths := testPaths(t)
	if err := os.Setenv("GOOS_OVERRIDE_FOR_TESTS", "darwin"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	if err := os.Setenv("TERM_PROGRAM", "Apple_Terminal"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		os.Unsetenv("GOOS_OVERRIDE_FOR_TESTS")
		os.Unsetenv("TERM_PROGRAM")
	})

	oldLoadRecipe := loadRecipeFn
	oldLoadConfig := loadConfigFn
	oldLookPath := lookPathFn
	t.Cleanup(func() {
		loadRecipeFn = oldLoadRecipe
		loadConfigFn = oldLoadConfig
		lookPathFn = oldLookPath
	})

	loadRecipeFn = func(Paths) (LauncherRecipe, error) {
		return LauncherRecipe{Strategy: "terminal_app"}, nil
	}
	loadConfigFn = func(Paths) (Config, error) {
		return DefaultConfig(), nil
	}
	lookPathFn = func(string) (string, error) {
		return "/usr/bin/tunnel", nil
	}

	report := BuildDoctorReport(context.Background(), paths, StatusInfo{Running: true, RelayConnected: true})
	if report.ExitCode() != 0 {
		t.Fatalf("ExitCode() = %d, want 0", report.ExitCode())
	}
}
