//go:build !windows && !plan9 && !wasm

package eventlog

import (
	"io/fs"
	"syscall"
)

func inodeOf(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
