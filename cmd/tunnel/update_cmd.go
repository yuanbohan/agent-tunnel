package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"yuanbohan/tunnel/internal/tunnel/daemon"
	tunnelupdate "yuanbohan/tunnel/internal/tunnel/update"
)

type updaterEngine interface {
	UpdateAvailable(context.Context) (tunnelupdate.LatestManifest, bool, error)
	InstallLatest(context.Context) (tunnelupdate.InstallResult, error)
	InstallVersion(context.Context, string) (tunnelupdate.InstallResult, error)
}

type updaterCallbacks struct {
	beforeReplace    func(tunnelupdate.InstallResult) error
	onReplaceFailure func(tunnelupdate.InstallResult) error
}

var newUpdaterEngine = func(callbacks updaterCallbacks) updaterEngine {
	return tunnelupdate.NewEngine(tunnelupdate.Config{
		BeforeReplace:    callbacks.beforeReplace,
		OnReplaceFailure: callbacks.onReplaceFailure,
	})
}

var checkTmuxAvailable = daemon.EnsureTmuxAvailable

func runManualUpdate(ctx context.Context, stdout, stderr io.Writer) error {
	state, err := loadUpdaterStateForInstall(stderr)
	if err != nil {
		return err
	}

	engine := newUpdaterEngine(newUpdaterStateCallbacks(state, func(next *updaterState, result tunnelupdate.InstallResult) {
		next.RollbackVersion = result.RollbackVersion
		next.RollbackReason = result.RollbackUnavailableReason
	}))
	result, err := engine.InstallLatest(ctx)
	if err != nil {
		return err
	}
	if !result.Updated {
		_, err := fmt.Fprintf(stdout, "tunnel %s is already up to date\n", result.CurrentVersion)
		return err
	}
	if _, err := fmt.Fprintf(stdout, "updated tunnel from %s to %s\n", result.CurrentVersion, result.InstalledVersion); err != nil {
		return err
	}
	writeTmuxReadinessWarning(stdout)
	if strings.TrimSpace(result.RollbackUnavailableReason) != "" {
		_, err := fmt.Fprintf(stdout, "%s\n", result.RollbackUnavailableReason)
		return err
	}
	return nil
}

func writeTmuxReadinessWarning(stdout io.Writer) {
	if checkTmuxAvailable == nil {
		return
	}
	if err := checkTmuxAvailable(); errors.Is(err, daemon.ErrTmuxNotFound) {
		_, _ = fmt.Fprintf(stdout, "warning: %s\n", daemonTmuxInstallGuidance())
	}
}

func runManualRollback(ctx context.Context, stdout, stderr io.Writer) error {
	state, err := loadUpdaterState()
	if err != nil {
		return err
	}

	rollbackVersion := strings.TrimSpace(state.RollbackVersion)
	if rollbackVersion == "" {
		reason := strings.TrimSpace(state.RollbackReason)
		if reason == "" {
			reason = "no previous official version is available"
		}
		return fmt.Errorf("rollback unavailable: %s", reason)
	}

	engine := newUpdaterEngine(newUpdaterStateCallbacks(state, func(next *updaterState, _ tunnelupdate.InstallResult) {
		next.RollbackVersion = ""
		next.RollbackReason = ""
	}))
	result, err := engine.InstallVersion(ctx, rollbackVersion)
	if err != nil {
		return fmt.Errorf("rollback to %s: %w", rollbackVersion, err)
	}

	_, err = fmt.Fprintf(stdout, "rolled back tunnel from %s to %s\n", result.CurrentVersion, result.InstalledVersion)
	return err
}

func loadUpdaterStateForInstall(stderr io.Writer) (updaterState, error) {
	state, err := loadUpdaterState()
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, errRecoverableUpdaterState) {
		return updaterState{}, err
	}
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "warning: ignoring broken %s while preparing Tunnel update state: %v\n", tunnelUpdaterFileName, err)
	}
	return updaterState{Version: updaterSchemaVersion}, nil
}

func newUpdaterStateCallbacks(base updaterState, mutate func(*updaterState, tunnelupdate.InstallResult)) updaterCallbacks {
	return updaterCallbacks{
		beforeReplace: func(result tunnelupdate.InstallResult) error {
			next := base
			mutate(&next, result)
			return saveUpdaterState(next)
		},
		onReplaceFailure: func(tunnelupdate.InstallResult) error {
			return saveUpdaterState(base)
		},
	}
}
