package main

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"
)

type invocationContextKey struct{}

var detectGitBranch = gitBranchForDir

func withInvocationArgs(ctx context.Context, args []string) context.Context {
	cloned := append([]string(nil), args...)
	return context.WithValue(ctx, invocationContextKey{}, cloned)
}

func invocationCommandPreview(ctx context.Context) string {
	args, ok := ctx.Value(invocationContextKey{}).([]string)
	if !ok || len(args) == 0 {
		return ""
	}
	return shellquote.Join(args...)
}

func gitBranchForDir(ctx context.Context, cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	output, err := exec.CommandContext(ctx, "git", "-C", cwd, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
