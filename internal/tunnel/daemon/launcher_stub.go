//go:build !linux && !darwin

package daemon

import "fmt"

func launchWithRecipe(recipe LauncherRecipe, command string) error {
	return fmt.Errorf("launch unsupported for current platform: %s %s", recipe.Strategy, command)
}
