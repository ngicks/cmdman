package eventlog

import (
	"io/fs"
	"os"
	"syscall"
)

// fileIdent is a stable file identity used by the poll watcher to detect
// rotations on top of size+mtime tracking. On windows it combines the
// volume serial number with the by-handle file index pair.
type fileIdent struct {
	volumeSerialNumber          uint32
	fileIndexHigh, fileIndexLow uint32
}

// fileIdentOf opens path to query its handle-level identity. fi is
// unused on windows. Zero value means the identity could not be
// determined; the caller falls back to size+mtime tracking.
func fileIdentOf(path string, _ fs.FileInfo) fileIdent {
	f, err := os.Open(path)
	if err != nil {
		return fileIdent{}
	}
	defer f.Close()

	fd := f.Fd()
	if fd == ^(uintptr(0)) {
		return fileIdent{}
	}

	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(fd), &info); err != nil {
		return fileIdent{}
	}
	return fileIdent{
		volumeSerialNumber: info.VolumeSerialNumber,
		fileIndexHigh:      info.FileIndexHigh,
		fileIndexLow:       info.FileIndexLow,
	}
}
