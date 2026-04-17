package daemon

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

const (
	CheckStatusOK   = "ok"
	CheckStatusWarn = "warn"
	CheckStatusFail = "fail"
)

var (
	loadRecipeFn = LoadRecipe
	loadConfigFn = LoadConfig
	lookPathFn   = exec.LookPath
)

type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

func (r DoctorReport) ExitCode() int {
	for _, check := range r.Checks {
		if check.Status != CheckStatusOK {
			return 1
		}
	}
	return 0
}

func BuildDoctorReport(ctx context.Context, paths Paths, status StatusInfo) DoctorReport {
	checks := []DoctorCheck{
		daemonProcessCheck(status),
		relayConnectivityCheck(status),
		desktopSessionCheck(),
		launcherRecipeCheck(paths),
		tunnelBinaryCheck(),
		daemonConfigCheck(paths),
		lastLaunchFailureCheck(status),
	}
	return DoctorReport{Checks: checks}
}

func daemonProcessCheck(status StatusInfo) DoctorCheck {
	if status.Running {
		return DoctorCheck{Name: "daemon process", Status: CheckStatusOK, Detail: "daemon is running"}
	}
	return DoctorCheck{Name: "daemon process", Status: CheckStatusFail, Detail: "daemon is not running"}
}

func relayConnectivityCheck(status StatusInfo) DoctorCheck {
	if status.RelayConnected {
		return DoctorCheck{Name: "relay connectivity", Status: CheckStatusOK, Detail: "daemon is connected to relay"}
	}
	return DoctorCheck{Name: "relay connectivity", Status: CheckStatusWarn, Detail: "daemon is not connected to relay"}
}

func desktopSessionCheck() DoctorCheck {
	if hasDesktopSession() {
		return DoctorCheck{Name: "desktop session", Status: CheckStatusOK, Detail: "desktop session detected"}
	}
	return DoctorCheck{Name: "desktop session", Status: CheckStatusFail, Detail: "no desktop session detected"}
}

func launcherRecipeCheck(paths Paths) DoctorCheck {
	recipe, err := loadRecipeFn(paths)
	if err == nil && strings.TrimSpace(recipe.Strategy) != "" {
		return DoctorCheck{Name: "launcher recipe", Status: CheckStatusOK, Detail: recipe.Strategy}
	}
	if inferred, inferErr := inferRecipeFn(); inferErr == nil {
		return DoctorCheck{Name: "launcher recipe", Status: CheckStatusWarn, Detail: inferred.Strategy}
	}
	return DoctorCheck{Name: "launcher recipe", Status: CheckStatusFail, Detail: "no healthy launcher recipe"}
}

func tunnelBinaryCheck() DoctorCheck {
	if _, err := lookPathFn("tunnel"); err == nil {
		return DoctorCheck{Name: "tunnel binary", Status: CheckStatusOK, Detail: "tunnel found in PATH"}
	}
	return DoctorCheck{Name: "tunnel binary", Status: CheckStatusFail, Detail: "tunnel not found in PATH"}
}

func daemonConfigCheck(paths Paths) DoctorCheck {
	if _, err := loadConfigFn(paths); err == nil {
		return DoctorCheck{Name: "daemon config", Status: CheckStatusOK, Detail: "config readable"}
	}
	return DoctorCheck{Name: "daemon config", Status: CheckStatusFail, Detail: "config unreadable"}
}

func lastLaunchFailureCheck(status StatusInfo) DoctorCheck {
	if strings.TrimSpace(status.LastFailure) == "" {
		return DoctorCheck{Name: "last launch failure", Status: CheckStatusOK, Detail: "no recorded launch failure"}
	}
	return DoctorCheck{Name: "last launch failure", Status: CheckStatusWarn, Detail: status.LastFailure}
}

func hasDesktopSession() bool {
	switch getenv("GOOS_OVERRIDE_FOR_TESTS") {
	case "darwin":
		return strings.TrimSpace(getenv("TERM_PROGRAM")) != "" || strings.TrimSpace(getenv("SSH_TTY")) == ""
	case "linux":
		return strings.TrimSpace(getenv("DISPLAY")) != "" || strings.TrimSpace(getenv("WAYLAND_DISPLAY")) != ""
	}

	switch runtimeGOOS() {
	case "darwin":
		return strings.TrimSpace(getenv("SSH_TTY")) == ""
	case "linux":
		return strings.TrimSpace(os.Getenv("DISPLAY")) != "" || strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
	default:
		return false
	}
}
