package eventlog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// scanOutcome is what scanFile reports to its caller.
type scanOutcome int

const (
	// scanContinue means the file was fully consumed (EOF) on a non-tailable
	// scan; the caller may proceed to the next file (e.g. active after archive)
	// or, for one-shot reads of the active file, exit cleanly.
	scanContinue scanOutcome = iota
	// scanRotated means the file ended with a rotation marker; the caller
	// should close this fd and reopen the active path.
	scanRotated
	// scanStop means the reader must exit entirely: ctx cancelled, send to
	// channel failed, Until boundary reached, or a fatal read error already
	// surfaced on the channel.
	scanStop
)

// scan reads from f until EOF (and waits on the watcher when follow is
// set) or a rotation marker. See scanOutcome for the result enum.
func (r *Reader) scan(ctx context.Context, f *os.File) scanOutcome {
	return r.scanFile(ctx, f, r.opt.Follow)
}

// scanFile is the parameterized core of scan(). When tailable is true and
// EOF is reached it waits on the watcher for more content; when false it
// returns scanContinue immediately (used for archive replay and one-shot
// reads where there is no "more content" to wait for).
func (r *Reader) scanFile(ctx context.Context, f *os.File, tailable bool) scanOutcome {
	br := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return scanStop
		}
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			ev, skip, rotation, perr := parseLine(line)
			if perr != nil {
				if !r.send(ctx, Record{Err: perr}) {
					return scanStop
				}
				continue
			}
			if skip {
				continue
			}
			if rotation {
				return scanRotated
			}
			if !r.opt.Until.IsZero() && !ev.Time.Before(r.opt.Until) {
				return scanStop
			}
			if !r.opt.Since.IsZero() && ev.Time.Before(r.opt.Since) {
				continue
			}
			if !r.send(ctx, Record{Event: ev}) {
				return scanStop
			}
			continue
		}
		if err != nil && err != io.EOF {
			if !r.send(ctx, Record{Err: fmt.Errorf("eventlog: read log file: %w", err)}) {
				return scanStop
			}
			return scanStop
		}
		// EOF on partial line: caller decides whether to wait for more.
		if !tailable {
			return scanContinue
		}
		if !r.wait(ctx) {
			return scanStop
		}
	}
}

// parseLine decodes one JSONL line. It reports:
//   - skip=true for empty/blank lines (no record to deliver, no error).
//   - rotation=true for the internal rotation marker (no record to deliver).
//   - err for malformed JSON.
//
// When skip and rotation are both false and err is nil, ev is the decoded
// event ready for delivery.
func parseLine(line []byte) (ev model.Event, skip, rotation bool, err error) {
	if len(line) == 0 {
		return model.Event{}, true, false, nil
	}
	trim := line
	for len(trim) > 0 && (trim[len(trim)-1] == '\n' || trim[len(trim)-1] == '\r') {
		trim = trim[:len(trim)-1]
	}
	if len(trim) == 0 {
		return model.Event{}, true, false, nil
	}
	if err := json.Unmarshal(trim, &ev); err != nil {
		return model.Event{}, false, false, fmt.Errorf("eventlog: decode line: %w", err)
	}
	if ev.Type == model.EventTypeRotation {
		return model.Event{}, false, true, nil
	}
	return ev, false, false, nil
}
