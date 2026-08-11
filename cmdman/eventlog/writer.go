package eventlog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ngicks/cmdman/cmdman/internal/flock"
	"github.com/ngicks/cmdman/cmdman/model"
)

// DefaultMaxSize is the active log file size at which the writer rotates.
const DefaultMaxSize int64 = 5 * 1024 * 1024 // 5 MiB

// ArchiveSuffix forms the single retained archive filename.
const ArchiveSuffix = ".1"

// Writer appends events under a sibling advisory lock. The lock file remains
// stable while the data file rotates.
type Writer struct {
	path     string
	lockPath string
	maxSize  int64
	now      func() time.Time

	mu sync.Mutex
}

// NewWriter requires a nonempty path and creates its parent directories. The
// active log file is created lazily by the first Append.
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

	// Open after locking to observe any preceding rotation.
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

// rotateLocked rotates f and returns a fresh active descriptor. The caller
// must hold the writer lock. On success it closes f; on error ownership stays
// with the caller.
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
