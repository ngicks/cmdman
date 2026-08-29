package cmdman

import (
	"context"
	"fmt"
	"strconv"

	cmdmanv1pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/cmdman/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// CaptureScreenRequest selects what a capture renders.
type CaptureScreenRequest struct {
	Escapes                bool
	AltScreen              bool
	Quiet                  bool
	PreserveTrailingSpaces bool
	// StartLine and EndLine are the -S/-E range as spelled on the CLI. Both
	// share one line index space: 0 is the topmost visible row and negative
	// numbers reach into history. An empty string leaves that end unset, and
	// "-" asks for the extreme end - the start of history for StartLine, the
	// bottom of the visible screen for EndLine.
	StartLine string
	EndLine   string
}

// captureLineSpec is one parsed end of the -S/-E range.
type captureLineSpec struct {
	set     bool
	extreme bool
	line    int32
}

// parseCaptureLine parses one end of the line range. flag names the flag as the
// user typed it so the error points at what to fix.
func parseCaptureLine(flag, s string) (captureLineSpec, error) {
	switch s {
	case "":
		return captureLineSpec{}, nil
	case "-":
		return captureLineSpec{set: true, extreme: true}, nil
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return captureLineSpec{}, fmt.Errorf(
			`invalid %s value %q: want a line number - 0 is the top visible row,`+
				` negative numbers reach into history - or "-" for the extreme end`,
			flag, s,
		)
	}
	return captureLineSpec{set: true, line: int32(n)}, nil
}

// CaptureScreen renders a snapshot of a running command's terminal screen.
//
// Capturing is deliberately TTY-only: the screen is a terminal emulator the
// monitor feeds from the command's PTY, so a command started without one has no
// screen at all, only a byte stream to read back with logs. That mirror also
// lives in the monitor process of the current run, which makes a stopped
// command an error rather than an empty capture.
func (s *Service) CaptureScreen(
	ctx context.Context,
	idOrName string,
	req CaptureScreenRequest,
) ([]byte, error) {
	start, err := parseCaptureLine("-S/--start-line", req.StartLine)
	if err != nil {
		return nil, err
	}
	end, err := parseCaptureLine("-E/--end-line", req.EndLine)
	if err != nil {
		return nil, err
	}

	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	id, _, cfg, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	if !cfg.Tty {
		return nil, fmt.Errorf(
			"command %q has no terminal screen: it runs without a TTY, so there is"+
				" nothing to capture; read its output with `cmdman logs` instead",
			idOrName,
		)
	}
	state, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return nil, fmt.Errorf("get state: %w", err)
	}
	if state != model.EventTypeStarting && state != model.EventTypeRunning {
		return nil, errNoScreenToCapture(idOrName)
	}

	conn, err := s.connectMonitor(ctx, stateJSON)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", idOrName, err)
	}
	defer conn.Close()

	resp, err := cmdmanv1pb.NewCommandMonitorServiceClient(conn).CaptureScreen(
		ctx,
		&cmdmanv1pb.CaptureScreenRequest{
			Escapes:                req.Escapes,
			AltScreen:              req.AltScreen,
			Quiet:                  req.Quiet,
			PreserveTrailingSpaces: req.PreserveTrailingSpaces,
			HasStart:               start.set,
			StartLine:              start.line,
			StartExtreme:           start.extreme,
			HasEnd:                 end.set,
			EndLine:                end.line,
			EndExtreme:             end.extreme,
		},
		// A whole-history capture of a wide, styled screen can exceed gRPC's
		// default 4MiB receive cap; the emulator holds at most 10k history
		// lines, so 64MiB comfortably bounds the worst case.
		grpc.MaxCallRecvMsgSize(64<<20),
	)
	if err != nil {
		return nil, captureScreenRPCError(idOrName, err)
	}
	return resp.Content, nil
}

// captureScreenRPCError names what a failed capture means to the user. An
// unreachable monitor is a run that ended between the state read and the call,
// which is the same answer as a command that was already stopped. The monitor's
// own precondition failures - no alternate screen, screen unavailable - are
// already written as sentences for the end user, so they are passed through
// rather than buried under the gRPC status text.
func captureScreenRPCError(idOrName string, err error) error {
	switch grpcstatus.Code(err) {
	case codes.Unavailable:
		return errNoScreenToCapture(idOrName)
	case codes.FailedPrecondition:
		return fmt.Errorf(
			"capture screen of %q: %s", idOrName, grpcstatus.Convert(err).Message(),
		)
	}
	return fmt.Errorf("capture screen of %q: %w", idOrName, err)
}

func errNoScreenToCapture(idOrName string) error {
	return fmt.Errorf(
		"command %q is not running: its terminal screen lives only in the monitor"+
			" of the current run",
		idOrName,
	)
}
