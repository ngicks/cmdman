//go:build !plan9 && !windows && !wasm

package cmdman

import (
	"os"
	"syscall"
)

func flock_trylock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func flock_unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
