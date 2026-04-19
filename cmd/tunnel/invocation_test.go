package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInvocationCommandPreviewUsesOriginalArgv(t *testing.T) {
	ctx := withInvocationArgs(context.Background(), []string{
		"tunnel",
		"run",
		"--verbose",
		"--label",
		"api fix",
		"--base-url",
		"https://relay.example.com/base",
		"codex",
		"--profile",
		"prod",
		"--note",
		"hello world",
	})

	got := invocationCommandPreview(ctx)
	want := "tunnel run --verbose --label 'api fix' --base-url https://relay.example.com/base codex --profile prod --note 'hello world'"
	if got != want {
		t.Fatalf("invocationCommandPreview() = %q, want %q", got, want)
	}
}

func TestInvocationCommandPreviewReturnsEmptyWithoutContext(t *testing.T) {
	if got := invocationCommandPreview(context.Background()); got != "" {
		t.Fatalf("invocationCommandPreview() = %q, want empty", got)
	}
}

func TestGitBranchForDirReturnsEmptyOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()

	if got := gitBranchForDir(context.Background(), dir); got != "" {
		t.Fatalf("gitBranchForDir() = %q, want empty outside git repo", got)
	}
}

func TestGitBranchForDirReturnsEmptyForDetachedHead(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "README.md"), "hello\n")
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	runGit(t, dir, "checkout", "--detach", "HEAD")

	if got := gitBranchForDir(context.Background(), dir); got != "" {
		t.Fatalf("gitBranchForDir() = %q, want empty on detached HEAD", got)
	}
}

func TestGitBranchForDirHonorsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "README.md"), "hello\n")
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if got := gitBranchForDir(ctx, dir); got != "" {
		t.Fatalf("gitBranchForDir() = %q, want empty for canceled context", got)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) returned error: %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v returned error: %v, output=%s", args, err, output)
	}
}
