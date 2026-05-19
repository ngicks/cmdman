package eventlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultMaxSize is the active log file size at which the writer rotates.
const DefaultMaxSize int64 = 5 * 1024 * 1024 // 5 MiB

// ArchiveSuffix is appended to the active log path to form the single
// retained archive filename.
const ArchiveSuffix = ".1"

// Writer appends events to the on-disk log. Each Append acquires an
// advisory exclusive lock for the duration of the write so multiple
// processes can append concurrently.
type Writer struct {
	path    string
	maxSize int64
	now     func() time.Time

	mu sync.Mutex
}

// NewWriter constructs a Writer rooted at path. The active file is opened
// (and created if absent) on the first Append call.
func NewWriter(path string) (*Writer, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: log path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("eventlog: create log dir: %w", err)
	}
	return &Writer{
		path:    path,
		maxSize: DefaultMaxSize,
		now:     time.Now,
	}, nil
}

// SetMaxSize overrides the rotation threshold. Values <= 0 disable rotation.
func (w *Writer) SetMaxSize(n int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.maxSize = n
}

// Path returns the active log file path.
func (w *Writer) Path() string {
	return w.path
}

// Append writes one event, taking an exclusive flock for the duration of
// the call. If the active file is at or above the rotation threshold the
// rotation marker is appended first, then the file is rotated in-place
// before the event is written to the fresh file.
func (w *Writer) Append(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	line, err := marshalEvent(e)
	if err != nil {
		return fmt.Errorf("eventlog: marshal event: %w", err)
	}

	f, err := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("eventlog: open log file: %w", err)
	}
	defer f.Close()

	if err := flockExclusive(f); err != nil {
		return fmt.Errorf("eventlog: lock log file: %w", err)
	}
	defer func() {
		_ = flockUnlock(f)
	}()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("eventlog: stat log file: %w", err)
	}

	if w.maxSize > 0 && stat.Size() >= w.maxSize {
		newF, err := w.rotateLocked(f)
		if err != nil {
			return err
		}
		// rotateLocked returns a new fd (lock not transferred); the caller's
		// `defer f.Close()` will close the renamed, now-archive file.
		// Swap the active fd so the deferred unlock/close target is the
		// new file.
		oldF := f
		f = newF
		defer oldF.Close()
		if err := flockExclusive(f); err != nil {
			return fmt.Errorf("eventlog: relock log file after rotate: %w", err)
		}
	}

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("eventlog: write event: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("eventlog: sync log file: %w", err)
	}
	return nil
}

// rotateLocked writes the rotation marker into f (assumed locked), removes
// any existing archive, renames the active path to the archive path, and
// returns a fresh fd opened on the active path. The returned fd is NOT
// locked.
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
	if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("eventlog: remove archive %s: %w", archive, err)
	}
	if err := os.Rename(w.path, archive); err != nil {
		return nil, fmt.Errorf("eventlog: rename %s -> %s: %w", w.path, archive, err)
	}
	newF, err := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("eventlog: reopen log file after rotate: %w", err)
	}
	return newF, nil
}
