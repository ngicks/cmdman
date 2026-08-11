package eventlog

import (
	"io/fs"
	"syscall"
)

// fileIdent is a stable file identity used by the poll watcher to detect
// rotations on top of size+mtime tracking. On plan9 it combines server
// type, dev, and Qid.
type fileIdent struct {
	typ uint16
	dev uint32
	qid syscall.Qid
}

// fileIdentOf returns the identity of the file referred to by fi. path
// is unused on plan9. Zero value means the identity could not be
// determined; the caller falls back to size+mtime tracking.
func fileIdentOf(_ string, fi fs.FileInfo) fileIdent {
	s, ok := fi.Sys().(*syscall.Dir)
	if !ok {
		return fileIdent{}
	}
	return fileIdent{typ: s.Type, dev: s.Dev, qid: s.Qid}
}
