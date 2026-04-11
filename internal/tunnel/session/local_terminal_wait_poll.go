//go:build !darwin

package session

import (
	"context"
	"time"

	"golang.org/x/sys/unix"
)

// Non-Darwin platforms keep the poll-based implementation. The Darwin select
// path is a targeted workaround for a darwin/arm64 stdin polling crash, not a
// general behavior change for the rest of the platforms we support.
func waitForInput(ctx context.Context, fd int) (bool, error) {
	fds := []unix.PollFd{{
		Fd:     int32(fd),
		Events: unix.POLLIN,
	}}

	for {
		if ctx.Err() != nil {
			return false, nil
		}

		n, err := unix.Poll(fds, int((100 * time.Millisecond).Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, nil
		}
		return fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0, nil
	}
}
