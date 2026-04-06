package main

import (
	"os/exec"
	"sync"
)

type sleepPreventionStatus string

const (
	sleepPreventionActive      sleepPreventionStatus = "sleep prevented"
	sleepPreventionFailed      sleepPreventionStatus = "sleep prevention failed"
	sleepPreventionUnsupported sleepPreventionStatus = "sleep unsupported"
)

type sleepPrevention struct {
	status   sleepPreventionStatus
	stop     func()
	stopOnce sync.Once
}

func newSleepPrevention(status sleepPreventionStatus, stop func()) sleepPrevention {
	if stop == nil {
		stop = func() {}
	}
	return sleepPrevention{
		status: status,
		stop:   stop,
	}
}

func (s *sleepPrevention) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(s.stop)
}

var (
	startSleepPrevention = startSleepPreventionForProcess
	newSleepCommand      = exec.Command
)
