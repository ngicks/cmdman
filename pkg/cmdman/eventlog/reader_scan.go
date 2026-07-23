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

type scanOutcome int

const (
	// scanContinue means a non-tailable file reached EOF.
	scanContinue scanOutcome = iota
	// scanRotated means the file ended with a rotation marker.
	scanRotated
	// scanStop means the reader must exit.
	scanStop
)

// scan reads until EOF or a rotation marker, following when configured.
func (r *Reader) scan(ctx context.Context, f *os.File) scanOutcome {
	return r.scanFile(ctx, f, r.opt.Follow)
}

// scanFile waits at EOF when tailable is true; otherwise it returns
// scanContinue for archive replay or a one-shot read.
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
		if !tailable {
			return scanContinue
		}
		if !r.wait(ctx) {
			return scanStop
		}
	}
}

// parseLine decodes one JSONL line. skip means blank input with no event or error;
// rotation means an internal marker with no event; err means malformed JSON.
// Otherwise ev is deliverable.
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
