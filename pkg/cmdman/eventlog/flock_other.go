//go:build plan9 || windows || wasm

package eventlog

import (
	"fmt"
	"os"
)

func flockExclusive(_ *os.File) error {
	return fmt.Errorf("eventlog: flock not supported on this platform")
}

func flockUnlock(_ *os.File) error {
	return nil
}
