//go:build !darwin

package main

func startSleepPreventionForProcess(int) sleepPrevention {
	return newSleepPrevention(sleepPreventionUnsupported, nil)
}
