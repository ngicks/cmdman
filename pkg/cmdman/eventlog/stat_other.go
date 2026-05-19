//go:build windows || plan9 || wasm

package eventlog

import "io/fs"

func inodeOf(fi fs.FileInfo) uint64 {
	// Best-effort: fall back to ModTime-only tracking on platforms whose
	// FileInfo.Sys() does not expose an inode. Returning 0 means the
	// poll watcher will rely on size+mtime to detect changes.
	_ = fi
	return 0
}
