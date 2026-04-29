//go:build !darwin && !linux

package daemon

import "os"

func verifyBrokerSocketOwner(_ string, _ os.FileInfo) error {
	return nil
}
