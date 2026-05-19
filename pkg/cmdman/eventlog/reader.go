package eventlog

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
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
type Reader struct {
	path    string
	opt     ReaderOption
	ctx     context.Context
	cancel  context.CancelFunc
	watcher Watcher
	rec     chan Record
	done    chan struct{}
}

// NewReader opens path and starts a follower goroutine. Callers must
// invoke Close to release the watcher and goroutine.
func NewReader(ctx context.Context, path string, opt ReaderOption) (*Reader, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: reader path is empty")
	}
	watcher, err := NewWatcher(ctx, opt.WatcherKind, path, opt.PollInterval)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithCancel(ctx)
	r := &Reader{
		path:    path,
		opt:     opt,
		ctx:     rctx,
		cancel:  cancel,
		watcher: watcher,
		rec:     make(chan Record, 16),
		done:    make(chan struct{}),
	}
	go r.run()
	return r, nil
}

// Events returns the record channel. It is closed when the reader stops.
func (r *Reader) Events() <-chan Record {
	return r.rec
}

// Close stops the reader and releases the underlying watcher.
func (r *Reader) Close() error {
	r.cancel()
	werr := r.watcher.Close()
	<-r.done
	return werr
}

func (r *Reader) run() {
	defer close(r.rec)
	defer close(r.done)

	for {
		if err := r.ctx.Err(); err != nil {
			return
		}
		f, err := os.Open(r.path)
		if errors.Is(err, os.ErrNotExist) {
			if !r.opt.Follow {
				return
			}
			if !r.wait() {
				return
			}
			continue
		}
		if err != nil {
			r.sendErr(fmt.Errorf("eventlog: open log file: %w", err))
			return
		}
		if r.opt.FromEnd {
			if _, err := f.Seek(0, io.SeekEnd); err != nil {
				_ = f.Close()
				r.sendErr(fmt.Errorf("eventlog: seek end: %w", err))
				return
			}
			// FromEnd is honored only for the first open. Subsequent reopens
			// (e.g. after rotation) start at the file head so the reader
			// doesn't miss the new file's content.
			r.opt.FromEnd = false
		}
		stop, rotated := r.scan(f)
		_ = f.Close()
		if stop {
			return
		}
		if rotated {
			// The active path now resolves to the post-rotation file.
			// Loop back and reopen.
			continue
		}
		if !r.opt.Follow {
			return
		}
		if !r.wait() {
			return
		}
	}
}

// scan reads from f until EOF (and waits on the watcher when follow is
// set) or a rotation marker. Returns (stop, rotated):
//   - stop=true means the run loop must exit (ctx cancelled, terminal error,
//     or non-follow EOF after a span completes).
//   - rotated=true means f had its trailing rotation marker; the run loop
//     should reopen the active path.
func (r *Reader) scan(f *os.File) (bool, bool) {
	br := bufio.NewReader(f)
	for {
		if err := r.ctx.Err(); err != nil {
			return true, false
		}
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			ev, rotation, perr := parseLine(line)
			if perr != nil {
				if !r.send(Record{Err: perr}) {
					return true, false
				}
				continue
			}
			if rotation {
				return false, true
			}
			if !r.opt.Until.IsZero() && !ev.Time.Before(r.opt.Until) {
				return true, false
			}
			if !r.opt.Since.IsZero() && ev.Time.Before(r.opt.Since) {
				continue
			}
			if !r.send(Record{Event: ev}) {
				return true, false
			}
			continue
		}
		if err != nil && err != io.EOF {
			if !r.send(Record{Err: fmt.Errorf("eventlog: read log file: %w", err)}) {
				return true, false
			}
			return true, false
		}
		// EOF on partial line: stop on non-follow, otherwise wait for more.
		if !r.opt.Follow {
			return true, false
		}
		if !r.wait() {
			return true, false
		}
	}
}

func (r *Reader) wait() bool {
	select {
	case <-r.ctx.Done():
		return false
	case _, ok := <-r.watcher.Events():
		if !ok {
			return false
		}
		return true
	}
}

func (r *Reader) send(rec Record) bool {
	select {
	case <-r.ctx.Done():
		return false
	case r.rec <- rec:
		return true
	}
}

func (r *Reader) sendErr(err error) {
	r.send(Record{Err: err})
}

// parseLine decodes one JSONL line. It reports rotation=true when the line
// is the internal rotation marker.
func parseLine(line []byte) (Event, bool, error) {
	if len(line) == 0 {
		return Event{}, false, nil
	}
	// Strip optional trailing newline; json.Unmarshal tolerates it but we
	// also want to skip empty padding lines.
	trim := line
	for len(trim) > 0 && (trim[len(trim)-1] == '\n' || trim[len(trim)-1] == '\r') {
		trim = trim[:len(trim)-1]
	}
	if len(trim) == 0 {
		return Event{}, false, nil
	}
	var ev Event
	if err := json.Unmarshal(trim, &ev); err != nil {
		return Event{}, false, fmt.Errorf("eventlog: decode line: %w", err)
	}
	if ev.Type == eventTypeRotation {
		return Event{}, true, nil
	}
	return ev, false, nil
}
