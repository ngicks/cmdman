//go:build unix

package eventlog

import (
	"io/fs"
	"syscall"
)

// fileIdent is a stable file identity used by the poll watcher to detect
// rotations on top of size+mtime tracking. On unix it combines the
// device number with the inode.
type fileIdent struct {
	dev   uint64
	inode uint64
}

// fileIdentOf returns the identity of the file referred to by fi. path
// is unused on unix. Zero value means the identity could not be
// determined; the caller falls back to size+mtime tracking.
func fileIdentOf(_ string, fi fs.FileInfo) fileIdent {
	s, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdent{}
	}
	// On darwin Dev is int32; keep the conversion.
	return fileIdent{dev: uint64(s.Dev), inode: uint64(s.Ino)}
}
