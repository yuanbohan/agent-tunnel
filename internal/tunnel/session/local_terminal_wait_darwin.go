//go:build darwin

package session

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// On Darwin we intentionally avoid unix.Poll for stdin readiness checks.
// A long-running interactive tunnel session crashed on go1.25.0 darwin/arm64
// while polling fd 0 (stdin) with the following core stack:
//
//	fatal error: fault
//	[signal SIGBUS: bus error code=0x1 addr=0x0 pc=...]
//	runtime.asyncPreempt()
//	golang.org/x/sys/unix.poll(...)
//	golang.org/x/sys/unix.Poll(...)
//	yuanbohan/tunnel/internal/tunnel/session.waitForInput(...)
//
// The failure was inside the poll path rather than inside hub/input logic, so
// Darwin uses select(2) here as a narrow crash workaround. The short timeout
// keeps context cancellation responsive without blocking forever in the syscall.
func waitForInput(ctx context.Context, fd int) (bool, error) {
	if !fdFitsSelectSet(fd) {
		return false, fmt.Errorf("stdin fd %d exceeds darwin select capacity", fd)
	}

	for {
		if ctx.Err() != nil {
			return false, nil
		}

		timeout := unix.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
		var readfds unix.FdSet
		fdSet(fd, &readfds)

		n, err := unix.Select(fd+1, &readfds, nil, nil, &timeout)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, nil
		}
		return fdIsSet(fd, &readfds), nil
	}
}

// select(2) on Darwin uses a fixed-size bitset. Guard the fd before writing
// into the set so an unexpectedly large descriptor fails clearly instead of
// corrupting the bitset math or panicking on an out-of-range access.
func fdSet(fd int, set *unix.FdSet) {
	const bitsPerWord = 32
	set.Bits[fd/bitsPerWord] |= 1 << (uint(fd) % bitsPerWord)
}

// fdIsSet mirrors fdSet and is kept local so the Darwin-only bitset layout is
// obvious at the call site that replaces the crashing Poll path.
func fdIsSet(fd int, set *unix.FdSet) bool {
	const bitsPerWord = 32
	return set.Bits[fd/bitsPerWord]&(1<<(uint(fd)%bitsPerWord)) != 0
}

// fdFitsSelectSet documents the main tradeoff of the Darwin workaround:
// select(2) is safer for this crash, but unlike poll(2) it has a bounded fd set.
func fdFitsSelectSet(fd int) bool {
	const bitsPerWord = 32
	return fd >= 0 && fd/bitsPerWord < len(unix.FdSet{}.Bits)
}
