//go:build !darwin && !linux

package daemon

import "os/exec"

func configureBackgroundProcess(cmd *exec.Cmd) {}

func processRunning(pid int) bool {
	return false
}
