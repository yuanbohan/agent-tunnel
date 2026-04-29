//go:build darwin || linux

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

func verifyBrokerSocketOwner(socketPath string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("broker socket owner mismatch: %s", socketPath)
	}
	return nil
}
