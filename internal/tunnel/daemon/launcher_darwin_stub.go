//go:build !darwin

package daemon

import (
	"fmt"
	"runtime"
)

func inferDarwinRecipe() (LauncherRecipe, error) {
	return LauncherRecipe{}, fmt.Errorf("inferDarwinRecipe is only supported on darwin (current: %s)", runtime.GOOS)
}
