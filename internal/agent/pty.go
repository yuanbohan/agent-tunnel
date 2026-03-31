package agent

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// SpawnShell starts a shell via PTY. It tries zsh, then bash, then sh.
// Returns the PTY master file and the running Cmd.
func SpawnShell() (*os.File, *exec.Cmd, error) {
	shell := detectShell()
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	return ptmx, cmd, nil
}

// ResizePTY updates the PTY window size.
func ResizePTY(ptmx *os.File, cols, rows int) error {
	return pty.Setsize(ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

func detectShell() string {
	for _, s := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return "/bin/sh"
}
