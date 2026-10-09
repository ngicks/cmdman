package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// readComposeDownOutput folds a compose down job's output into a summary, from
// its start.
// With follow it reads on until the job's run is over, handing publish the
// running summary each time a line changes it.
//
// A read that fails is logged and ends the read: the summary is read again from
// the stored output once the job is over, so nothing is lost for good.
func readComposeDownOutput(
	ctx context.Context,
	svc *cmdman.Service,
	id string,
	follow bool,
	publish func(tui.DownSummary),
) downProgress {
	var p downProgress
	r, err := svc.Logs(ctx, cmdman.LogsRequest{IDOrName: id, Follow: follow})
	if err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).DebugContext(
			ctx, "compose down job: read output", "id", id, "error", err,
		)
		return p
	}
	defer func() { _ = r.Close() }()
	for rec := range r.Records() {
		if rec.Err != nil {
			contextkey.ValueSlogLoggerDefault(ctx).DebugContext(
				ctx, "compose down job: read output", "id", id, "error", rec.Err,
			)
			continue
		}
		if p.add(rec.Line) && publish != nil {
			summary := p.summary
			summary.Running = true
			publish(summary)
		}
	}
	return p
}

// readStoredComposeDownOutput reads the whole stored output of a job that is
// over. ok is false when the output cannot be read at all.
func readStoredComposeDownOutput(
	ctx context.Context,
	svc *cmdman.Service,
	id string,
) (downProgress, bool) {
	r, err := svc.Logs(ctx, cmdman.LogsRequest{IDOrName: id})
	if err != nil {
		return downProgress{}, false
	}
	defer func() { _ = r.Close() }()
	var p downProgress
	for rec := range r.Records() {
		if rec.Err != nil {
			return downProgress{}, false
		}
		p.add(rec.Line)
	}
	return p, true
}

// downProgress is what a compose down job's output says it did so far: the
// JSON progress records on its stdout (see progressLine), and the last error
// line on its stderr.
type downProgress struct {
	summary tui.DownSummary
	lastErr string
	// partial holds a line per stream that has not been written to its end yet.
	// The output is stored and streamed as the writes came, so a long line can
	// arrive in pieces.
	partial map[logdriver.Stream][]byte
}

// add takes in one record of the output, and reports whether the summary
// changed.
func (p *downProgress) add(line logdriver.LogLine) bool {
	buf := slices.Concat(p.partial[line.Stream], line.Line)
	if line.Partial {
		if p.partial == nil {
			p.partial = map[logdriver.Stream][]byte{}
		}
		p.partial[line.Stream] = buf
		return false
	}
	delete(p.partial, line.Stream)
	text := strings.TrimRight(string(buf), "\r\n")
	if line.Stream == logdriver.StreamStderr {
		// main writes the error a command returned this way. Other lines on
		// stderr are warnings and log records, which say nothing about why the
		// down failed.
		if msg, ok := strings.CutPrefix(text, "error: "); ok {
			p.lastErr = msg
		}
		return false
	}
	return p.count(text)
}

// count folds one progress record into the summary: a stop that went through,
// a removal, and a resource left unreleased. A stop that failed is reported in
// a phase of its own, so it does not count as stopped. An already-exited
// command does not either: the stop phase receives only the running commands,
// so it emits no stopped record for one.
func (p *downProgress) count(text string) bool {
	var line progressLine
	if err := json.Unmarshal([]byte(text), &line); err != nil {
		return false
	}
	switch compose.Phase(line.Phase) {
	case compose.PhaseStopped:
		if line.Error != "" {
			return false
		}
		p.summary.Stopped++
		if line.ForceKilled {
			p.summary.ForceKilled++
		}
	case compose.PhaseRemoved:
		p.summary.Removed++
	case compose.PhaseUnreleased:
		p.summary.Unreleased++
	default:
		return false
	}
	return true
}
