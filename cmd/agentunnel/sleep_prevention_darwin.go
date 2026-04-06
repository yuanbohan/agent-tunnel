//go:build darwin

package main

import (
	"io"
	"strconv"
)

func startSleepPreventionForProcess(pid int) sleepPrevention {
	cmd := newSleepCommand("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(pid))
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return newSleepPrevention(sleepPreventionFailed, nil)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return newSleepPrevention(sleepPreventionActive, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
}
