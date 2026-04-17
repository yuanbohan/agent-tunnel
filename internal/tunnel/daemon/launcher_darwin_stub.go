//go:build !darwin

package daemon

import "fmt"

func inferDarwinRecipe() (LauncherRecipe, error) {
	return LauncherRecipe{}, fmt.Errorf("unsupported platform: darwin")
}
