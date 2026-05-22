package eventlog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/pkg/cmdman/internal/flock"
)

// openSnapshot opens the archive (if it exists) and the active file under a
// shared advisory lock on the writer's lock file, so the pair cannot be
// rotated out from under the reader between the two opens.
//
// On platforms or filesystems where the lock cannot be acquired the
// snapshot proceeds best-effort without coordination: this matches the
// writer's own behavior (its exclusive lock is also best-effort on
// unsupported platforms).
func (r *Reader) openSnapshot() (archive, active *os.File, err error) {
	dir := filepath.Dir(r.path)
	base := filepath.Base(r.path)
	lockPath := filepath.Join(dir, "."+base+".lock")

	// Best-effort: ensure the lock file's directory exists. If MkdirAll
	// fails (e.g. permission), fall through and try the open paths
	// without coordination.
	_ = os.MkdirAll(dir, 0o755)

	if lockF, lerr := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644); lerr == nil {
		// Acquire shared lock for the brief open window. Errors here
		// (unsupported platform, EINTR loops) are non-fatal: we proceed
		// without coordination.
		_ = flock.LockShared(lockF)
		defer func() {
			_ = flock.Unlock(lockF)
			_ = lockF.Close()
		}()
	}

	archive, aerr := os.Open(r.path + ArchiveSuffix)
	switch {
	case errors.Is(aerr, os.ErrNotExist):
		archive = nil
	case aerr != nil:
		return nil, nil, fmt.Errorf("eventlog: open archive: %w", aerr)
	}

	active, perr := os.Open(r.path)
	switch {
	case errors.Is(perr, os.ErrNotExist):
		active = nil
	case perr != nil:
		if archive != nil {
			_ = archive.Close()
		}
		return nil, nil, fmt.Errorf("eventlog: open active log file: %w", perr)
	}
	return archive, active, nil
}
