package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchWithRecipeExecTemplatePassesShellCommandWithoutExtraFlag(t *testing.T) {
	root := t.TempDir()
	outputPath := filepath.Join(root, "args.txt")
	scriptPath := filepath.Join(root, "launcher.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_OUT\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	oldValue, hadValue := os.LookupEnv("ARGS_OUT")
	if err := os.Setenv("ARGS_OUT", outputPath); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	t.Cleanup(func() {
		if hadValue {
			_ = os.Setenv("ARGS_OUT", oldValue)
			return
		}
		os.Unsetenv("ARGS_OUT")
	})

	err := launchWithRecipe(LauncherRecipe{
		Strategy: strategyExecTemplate,
		Command:  scriptPath,
	}, "echo hello")
	if err != nil {
		t.Fatalf("launchWithRecipe returned error: %v", err)
	}

	payload, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(payload)), "\n")
	want := []string{"sh", "-lc", "echo hello"}
	if len(got) != len(want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %#v, want %#v", got, want)
		}
	}
}
