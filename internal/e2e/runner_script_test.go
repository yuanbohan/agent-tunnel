package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalE2ERunnerPreservesPostgresStartupFailureOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}

	tempDir := t.TempDir()
	outputFile := filepath.Join(tempDir, "latest.log")
	pgScript := filepath.Join(tempDir, "fake-postgres.sh")

	writeExecutable(t, pgScript, `#!/bin/sh
set -eu
case "${1:-}" in
  reset)
    echo "reset ok"
    ;;
  up)
    echo "postgres failed to start"
    echo "  dsn: postgres://agentunnel:supersecret@127.0.0.1:55432/agent_tunnel_e2e?sslmode=disable"
    exit 1
    ;;
  dsn)
    echo "postgres://agentunnel:supersecret@127.0.0.1:55432/agent_tunnel_e2e?sslmode=disable"
    ;;
  logs)
    echo "fake postgres logs"
    ;;
  *)
    echo "unexpected command: $1" >&2
    exit 1
    ;;
esac
`)

	err := runLocalE2ERunner(t, outputFile, pgScript, "")
	if err == nil {
		t.Fatal("runLocalE2ERunner returned nil, want failure")
	}

	logText := readTextFile(t, outputFile)
	if !strings.Contains(logText, "failed_stage: postgres_up") {
		t.Fatalf("log missing postgres_up failure stage:\n%s", logText)
	}
	if !strings.Contains(logText, "postgres failed to start") {
		t.Fatalf("log missing postgres startup failure output:\n%s", logText)
	}
	if !strings.Contains(logText, "dsn: <redacted>") {
		t.Fatalf("log missing redacted DSN:\n%s", logText)
	}
	if strings.Contains(logText, "supersecret@127.0.0.1") {
		t.Fatalf("log leaked raw DSN secret:\n%s", logText)
	}
}

func TestLocalE2ERunnerFailsWhenCleanupResetFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}

	tempDir := t.TempDir()
	outputFile := filepath.Join(tempDir, "latest.log")
	pgScript := filepath.Join(tempDir, "fake-postgres.sh")
	goBinDir := filepath.Join(tempDir, "bin")
	resetCountFile := filepath.Join(tempDir, "reset-count")

	if err := os.MkdirAll(goBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll goBinDir: %v", err)
	}

	writeExecutable(t, pgScript, `#!/bin/sh
set -eu
count_file="`+resetCountFile+`"
case "${1:-}" in
  reset)
    count=0
    if [ -f "$count_file" ]; then
      count="$(cat "$count_file")"
    fi
    count=$((count + 1))
    printf '%s\n' "$count" >"$count_file"
    if [ "$count" -eq 1 ]; then
      echo "reset ok"
      exit 0
    fi
    echo "cleanup failed" >&2
    exit 1
    ;;
  up)
    echo "postgres ready"
    echo "  dsn: postgres://agentunnel:supersecret@127.0.0.1:55432/agent_tunnel_e2e?sslmode=disable"
    ;;
  dsn)
    echo "postgres://agentunnel:supersecret@127.0.0.1:55432/agent_tunnel_e2e?sslmode=disable"
    ;;
  logs)
    echo "fake postgres logs"
    ;;
  *)
    echo "unexpected command: $1" >&2
    exit 1
    ;;
esac
`)

	writeExecutable(t, filepath.Join(goBinDir, "go"), `#!/bin/sh
set -eu
if [ "${1:-}" = "test" ]; then
  echo "ok   ./internal/e2e"
  exit 0
fi
echo "unexpected go command: $*" >&2
exit 1
`)

	err := runLocalE2ERunner(t, outputFile, pgScript, goBinDir)
	if err == nil {
		t.Fatal("runLocalE2ERunner returned nil, want cleanup failure")
	}

	logText := readTextFile(t, outputFile)
	if !strings.Contains(logText, "result: PASS") {
		t.Fatalf("log missing test pass result:\n%s", logText)
	}
	if !strings.Contains(logText, "cleanup_result: FAIL") {
		t.Fatalf("log missing cleanup failure marker:\n%s", logText)
	}
	if !strings.Contains(logText, "cleanup_exit_code: 1") {
		t.Fatalf("log missing cleanup exit code:\n%s", logText)
	}
	if !strings.Contains(logText, "cleanup failed") {
		t.Fatalf("log missing cleanup stderr:\n%s", logText)
	}
}

func runLocalE2ERunner(t *testing.T, outputFile, pgScript, goBinDir string) error {
	t.Helper()

	root, err := filepath.Abs(repoRoot(t))
	if err != nil {
		t.Fatalf("Abs repoRoot: %v", err)
	}
	runner := filepath.Join(root, "scripts", "local-e2e-run.sh")
	cmd := exec.Command("/bin/sh", runner)
	cmd.Dir = root
	cmd.Env = append([]string(nil), os.Environ()...)
	cmd.Env = append(cmd.Env,
		"LOCAL_E2E_OUTPUT_FILE="+outputFile,
		"LOCAL_E2E_PG_SCRIPT="+pgScript,
	)
	if strings.TrimSpace(goBinDir) != "" {
		cmd.Env = append(cmd.Env, "PATH="+goBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(data)
}
