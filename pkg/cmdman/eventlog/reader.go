package eventlog

import (
	"context"
	"fmt"
	"io"
	"time"

	"golang.org/x/sync/errgroup"
)

// ReaderOption configures Reader behavior.
type ReaderOption struct {
	// Follow keeps the reader tailing the file after it reaches EOF.
	Follow bool
	// Since drops events whose Time is strictly before this value. Zero
	// means no lower bound.
	Since time.Time
	// Until terminates the reader once an event at-or-after this value is
	// observed (the boundary event is not delivered). Zero means follow
	// without an upper bound.
	Until time.Time
	// FromEnd skips existing content and only delivers new events. Useful
	// for live subscribers that don't want history replay.
	FromEnd bool
	// WatcherKind selects the change-notification backend. When empty,
	// the poll watcher is used.
	WatcherKind WatcherKind
	// PollInterval overrides the stat-poll cadence. Zero means default.
	PollInterval time.Duration
}

// Record is one item emitted by a Reader. Err carries fatal read errors;
// the reader closes its channel after emitting a terminal error.
type Record struct {
	Event Event
	Err   error
}

// Reader follows the JSONL event log, switching files when it observes a
// rotation marker.
//
// The caller drives the reader's lifetime: spawn Run in its own goroutine
// and cancel ctx to tear it down. Events is closed once Run returns.
type Reader struct {
	path    string
	opt     ReaderOption
	watcher Watcher
	rec     chan Record

	// seeked tracks whether the FromEnd seek has already been
	// performed on the initial open. Subsequent reopens (e.g. after
	// rotation) read the new file from its head.
	seeked bool

	// lastIdent is the inode identity of the log file the reader most
	// recently drained. It may be either an active file or a replayed
	// archive; scanLoop uses it to avoid replaying already-consumed
	// archives while still detecting intermediate archives produced by a
	// second rotation.
	lastIdent fileIdent
}

// NewReader builds a Reader rooted at path. It does not start any goroutines
// — the caller invokes Run on its own goroutine.
//
// A change-notification Watcher is only constructed when opt.Follow is true.
// One-shot reads (Follow=false) scan the file once and exit on EOF, so they
// neither need nor want the failure modes of inotify/poll setup.
func NewReader(path string, opt ReaderOption) (*Reader, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: reader path is empty")
	}
	var watcher Watcher
	if opt.Follow {
		w, err := NewWatcher(opt.WatcherKind, path, opt.PollInterval)
		if err != nil {
			return nil, err
		}
		watcher = w
	}
	return &Reader{
		path:    path,
		opt:     opt,
		watcher: watcher,
		rec:     make(chan Record, 16),
	}, nil
}

// Events returns the record channel. It is closed when Run returns.
func (r *Reader) Events() <-chan Record {
	return r.rec
}

// Run drives the reader (and its underlying watcher) until ctx is cancelled
// or a terminal condition is reached (non-follow EOF, Until reached, fatal
// read error). It must be called exactly once. The Events channel is closed
// before Run returns.
func (r *Reader) Run(ctx context.Context) error {
	defer close(r.rec)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	eg, egctx := errgroup.WithContext(runCtx)
	if r.watcher != nil {
		eg.Go(func() error {
			defer cancel()
			return r.watcher.Run(egctx)
		})
	}
	eg.Go(func() error {
		defer cancel()
		r.scanLoop(egctx)
		return nil
	})
	return eg.Wait()
}

func (r *Reader) scanLoop(ctx context.Context) {
	// lastIdent is the inode identity of the last file the reader drained.
	// On every iteration we take a fresh coordinated snapshot of {archive,
	// active} and compare the archive's inode against this value:
	//
	//   - zero (initial pass) + !FromEnd: replay the archive (history).
	//   - zero (initial pass) + FromEnd:  skip the archive (history).
	//   - non-zero, archive == last:      normal rotation; don't replay
	//                                     the file we just finished.
	//   - non-zero, archive != last:      a second rotation completed
	//                                     before we noticed the first
	//                                     marker — the current archive is
	//                                     an intermediate file whose
	//                                     contents we have never seen.
	//                                     Replay it.
	//
	// This narrows the multi-rotation race to a single retained archive:
	// any data older than that is gone by writer policy (only one .1 is
	// kept). It also subsumes the original startup snapshot — every
	// iteration takes its own snapshot, so we never re-race against a
	// rotation between "see marker" and "open the next active".

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		archive, active, err := r.openSnapshot()
		if err != nil {
			r.sendErr(ctx, err)
			return
		}

		if archive != nil {
			astat, _ := archive.Stat()
			var archiveIdent fileIdent
			if astat != nil {
				archiveIdent = fileIdentOf(r.path+ArchiveSuffix, astat)
			}
			initialPass := r.lastIdent == (fileIdent{})
			intermediatePass := !initialPass &&
				archiveIdent != (fileIdent{}) &&
				archiveIdent != r.lastIdent
			shouldReplay := (initialPass && !r.opt.FromEnd) || intermediatePass

			if shouldReplay {
				outcome := r.scanFile(ctx, archive, false)
				_ = archive.Close()
				if outcome == scanStop {
					if active != nil {
						_ = active.Close()
					}
					return
				}
				if archiveIdent != (fileIdent{}) {
					r.lastIdent = archiveIdent
				}
				// scanContinue (marker-less archive) and scanRotated
				// (normal end-of-archive marker) both mean "archive
				// consumed; carry on with active".
			} else {
				_ = archive.Close()
			}
		}

		if active == nil {
			if !r.opt.Follow {
				return
			}
			// The active file did not exist at the time of the snapshot;
			// suppress the FromEnd seek so when it appears we read it
			// from byte 0 instead of dropping the writer's first burst.
			r.seeked = true
			if !r.wait(ctx) {
				return
			}
			continue
		}

		if r.opt.FromEnd && !r.seeked {
			if _, err := active.Seek(0, io.SeekEnd); err != nil {
				_ = active.Close()
				r.sendErr(ctx, fmt.Errorf("eventlog: seek end: %w", err))
				return
			}
			// FromEnd is honored only for the first open. Subsequent
			// reopens (e.g. after rotation) start at the file head so
			// the reader doesn't miss the new file's content.
			r.seeked = true
		}

		if vstat, statErr := active.Stat(); statErr == nil {
			r.lastIdent = fileIdentOf(r.path, vstat)
		}

		outcome := r.scan(ctx, active)
		_ = active.Close()
		switch outcome {
		case scanRotated:
			continue
		case scanStop:
			return
		case scanContinue:
			// Reached on Follow=false (non-tailable scan reached EOF).
			return
		}
	}
}

func (r *Reader) wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case _, ok := <-r.watcher.Events():
		if !ok {
			return false
		}
		return true
	}
}

func (r *Reader) send(ctx context.Context, rec Record) bool {
	select {
	case <-ctx.Done():
		return false
	case r.rec <- rec:
		return true
	}
}

func (r *Reader) sendErr(ctx context.Context, err error) {
	r.send(ctx, Record{Err: err})
}
