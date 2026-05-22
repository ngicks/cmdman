//go:build plan9 || windows || wasm

package flock

import (
	"fmt"
	"os"
)

func tryLockExclusive(_ *os.File) error {
	return fmt.Errorf("flock: not supported on this platform")
}

func lockExclusive(_ *os.File) error {
	return fmt.Errorf("flock: not supported on this platform")
}

func lockShared(_ *os.File) error {
	return fmt.Errorf("flock: not supported on this platform")
}

func unlock(_ *os.File) error {
	return nil
}
