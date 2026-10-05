package compose

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/go-common/contextkey"
)

// forwardHookOutput reports every output line of the exec command id until
// its output ends or ctx is done.
func (s *Service) forwardHookOutput(ctx context.Context, run hookRun, id string) {
	reader, err := s.svc.Logs(ctx, cmdman.LogsRequest{IDOrName: id, Follow: true})
	if err != nil {
		if ctx.Err() == nil {
			contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx,
				"compose: follow hook output", "command", run.exec, "error", err)
		}
		return
	}
	defer reader.Close()
	var lines lineAssembler
	for rec := range reader.Records() {
		if rec.Err != nil {
			if ctx.Err() == nil {
				contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx,
					"compose: follow hook output", "command", run.exec, "error", rec.Err)
			}
			continue
		}
		if line, ok := lines.add(rec.Line); ok {
			s.reportHookOutput(run, rec.Line.Stream, line)
		}
	}
	for _, l := range lines.rest() {
		s.reportHookOutput(run, l.Stream, string(l.Line))
	}
}

// readResourceValue reads back the stdout of the stopped exec command id and
// returns its resource value (see [resourceValue]).
func (s *Service) readResourceValue(ctx context.Context, id string) (string, error) {
	reader, err := s.svc.Logs(ctx, cmdman.LogsRequest{IDOrName: id})
	if err != nil {
		return "", err
	}
	defer reader.Close()
	var (
		assembler lineAssembler
		stdout    []string
	)
	for rec := range reader.Records() {
		if rec.Err != nil {
			return "", rec.Err
		}
		if rec.Line.Stream != logdriver.StreamStdout {
			continue
		}
		if line, ok := assembler.add(rec.Line); ok {
			stdout = append(stdout, line)
		}
	}
	for _, l := range assembler.rest() {
		stdout = append(stdout, string(l.Line))
	}
	return resourceValue(stdout), nil
}

// resourceValue returns the last line of stdout that is not blank, with
// surrounding white space trimmed, or "" when every line is blank.
func resourceValue(stdout []string) string {
	for _, line := range slices.Backward(stdout) {
		if v := strings.TrimSpace(line); v != "" {
			return v
		}
	}
	return ""
}

// lineAssembler joins the partial records a log driver splits one line into,
// per stream.
type lineAssembler struct {
	pending map[logdriver.Stream][]byte
}

// add takes the next record and returns the line it completes, without its
// line terminator.
func (a *lineAssembler) add(l logdriver.LogLine) (string, bool) {
	if l.Partial {
		if a.pending == nil {
			a.pending = make(map[logdriver.Stream][]byte)
		}
		a.pending[l.Stream] = append(a.pending[l.Stream], l.Line...)
		return "", false
	}
	line := slices.Concat(a.pending[l.Stream], l.Line)
	delete(a.pending, l.Stream)
	return strings.TrimRight(string(line), "\r\n"), true
}

// rest returns the lines still unterminated when the output ended, ordered by
// stream.
func (a *lineAssembler) rest() []logdriver.LogLine {
	var out []logdriver.LogLine
	for stream, line := range a.pending {
		if len(line) > 0 {
			out = append(out, logdriver.LogLine{Stream: stream, Line: line})
		}
	}
	slices.SortFunc(out, func(x, y logdriver.LogLine) int {
		return cmp.Compare(x.Stream, y.Stream)
	})
	a.pending = nil
	return out
}
