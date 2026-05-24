package eventlog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/internal/flock"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// DefaultMaxSize is the active log file size at which the writer rotates.
const DefaultMaxSize int64 = 5 * 1024 * 1024 // 5 MiB

// ArchiveSuffix is appended to the active log path to form the single
// retained archive filename.
const ArchiveSuffix = ".1"

// Writer appends events to the on-disk log. Each Append takes an exclusive
// advisory lock on a sibling lock file (".<base>.lock") so multiple processes
// serialise around append+rotation. The lock file is a stable coordination
// sentinel — never renamed, never carrying data — so a writer cannot inherit
// a lock on an inode that has already been rotated out from under it.
type Writer struct {
	path     string
	lockPath string
	maxSize  int64
	now      func() time.Time

	mu sync.Mutex
}

// NewWriter constructs a Writer rooted at path. The active file is opened
// (and created if absent) on the first Append call.
func NewWriter(path string) (*Writer, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: log path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("eventlog: create log dir: %w", err)
	}
	return &Writer{
		path:     path,
		lockPath: filepath.Join(dir, "."+filepath.Base(path)+".lock"),
		maxSize:  DefaultMaxSize,
		now:      time.Now,
	}, nil
}

// Path returns the active log file path.
func (w *Writer) Path() string {
	return w.path
}

// Append writes one event under the sibling lock file. If appending the
// event would push the active file above the rotation threshold, the
// rotation marker is appended first, the file is rotated in-place, and the
// event is then written to the fresh file.
func (w *Writer) Append(e model.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	line, err := marshalEvent(e)
	if err != nil {
		return fmt.Errorf("eventlog: marshal event: %w", err)
	}

	// Acquire the cross-process lock first. The lock file's name is
	// stable, so a rename of w.path during rotation cannot strand us with
	// a lock on the old inode.
	lockF, err := os.OpenFile(w.lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("eventlog: open lock file: %w", err)
	}
	defer func() {
		_ = flock.Unlock(lockF)
		_ = lockF.Close()
	}()
	if err := flock.LockExclusive(lockF); err != nil {
		return fmt.Errorf("eventlog: lock log file: %w", err)
	}

	// Open the active file fresh after acquiring the lock so we always see
	// the post-rotation state.
	f, err := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("eventlog: open log file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("eventlog: stat log file: %w", err)
	}

	if w.maxSize > 0 && stat.Size()+int64(len(line)) > w.maxSize {
		newF, err := w.rotateLocked(f)
		if err != nil {
			return err
		}
		// rotateLocked closed the old fd on success; swap so the deferred
		// cleanup targets the new fd.
		f = newF
	}

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("eventlog: write event: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("eventlog: sync log file: %w", err)
	}
	return nil
}

// rotateLocked writes the rotation marker into f, removes any existing
// archive, renames the active path to the archive path, and returns a fresh
// fd opened on the active path. The caller must hold the writer's lock file
// across the entire rotation. On success ownership of f transfers to
// rotateLocked: it closes f before returning the new fd. On error f is left
// untouched and the caller retains ownership.
func (w *Writer) rotateLocked(f *os.File) (*os.File, error) {
	marker, err := rotationMarker(w.now())
	if err != nil {
		return nil, fmt.Errorf("eventlog: marshal rotation marker: %w", err)
	}
	if _, err := f.Write(marker); err != nil {
		return nil, fmt.Errorf("eventlog: write rotation marker: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("eventlog: sync before rotate: %w", err)
	}

	archive := w.path + ArchiveSuffix
	if err := os.Remove(archive); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("eventlog: remove archive %s: %w", archive, err)
	}
	if err := os.Rename(w.path, archive); err != nil {
		return nil, fmt.Errorf("eventlog: rename %s -> %s: %w", w.path, archive, err)
	}
	newF, err := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("eventlog: reopen log file after rotate: %w", err)
	}

	_ = f.Close()
	return newF, nil
}
