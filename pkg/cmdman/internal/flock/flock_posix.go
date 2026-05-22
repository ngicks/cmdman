//go:build !plan9 && !windows && !wasm

package flock

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryLockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func lockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func lockShared(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_SH)
}

func unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
