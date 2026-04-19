package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

var detectGitBranch = gitBranchForDir

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
