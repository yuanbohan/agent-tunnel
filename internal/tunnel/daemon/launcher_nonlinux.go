//go:build !linux

package daemon

import "fmt"

func inferLinuxRecipe() (LauncherRecipe, error) {
	return LauncherRecipe{}, fmt.Errorf("linux launcher inference unavailable")
}

func detectLinuxPlatformID() string {
	return PlatformIDUnknown
}
