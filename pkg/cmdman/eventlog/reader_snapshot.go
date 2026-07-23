package eventlog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/pkg/cmdman/internal/flock"
)

// openSnapshot opens the archive and active file under the writer's lock.
// If either path is absent, its corresponding result is nil with a nil error.
// Unsupported locks degrade to an uncoordinated best-effort snapshot.
func (r *Reader) openSnapshot() (archive, active *os.File, err error) {
	dir := filepath.Dir(r.path)
	base := filepath.Base(r.path)
	lockPath := filepath.Join(dir, "."+base+".lock")

	// Failure here also degrades to an uncoordinated snapshot.
	_ = os.MkdirAll(dir, 0o755)

	if lockF, lerr := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644); lerr == nil {
		// Lock errors are non-fatal on unsupported platforms.
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
