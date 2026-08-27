package compose

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/cmdman"
)

// CaptureScreenOption configures a compose CaptureScreen operation. The capture
// fields mirror [cmdman.CaptureScreenRequest] one for one.
type CaptureScreenOption struct {
	// CommandName selects the compose command to capture (required).
	CommandName string
	// ScaleIndex selects the replica (1-based); 0 means "the sole replica".
	ScaleIndex int

	Escapes               bool
	AltScreen             bool
	Quiet                 bool
	PreserveTrailingSpace bool
	// StartLine and EndLine are the -S/-E range as spelled on the CLI; see
	// [cmdman.CaptureScreenRequest] for the line index space they share.
	StartLine string
	EndLine   string
}

// CaptureScreen renders a snapshot of one compose command's terminal screen.
//
// Unlike the project-wide compose operations, this targets exactly one replica:
// a capture is a single screen, so there is nothing to aggregate. The
// preconditions - the command must have a TTY and be running - belong to
// [cmdman.Service.CaptureScreen] and are surfaced as it words them, with the
// compose command name added since the resolved cmdman ID means nothing to a
// caller who addressed a service.
func (s *Service) CaptureScreen(
	ctx context.Context,
	selection ProjectSelection,
	opts CaptureScreenOption,
) ([]byte, error) {
	id, err := s.ResolveCommandID(ctx, selection, opts.CommandName, opts.ScaleIndex)
	if err != nil {
		return nil, err
	}

	content, err := s.svc.CaptureScreen(ctx, id, cmdman.CaptureScreenRequest{
		Escapes:               opts.Escapes,
		AltScreen:             opts.AltScreen,
		Quiet:                 opts.Quiet,
		PreserveTrailingSpace: opts.PreserveTrailingSpace,
		StartLine:             opts.StartLine,
		EndLine:               opts.EndLine,
	})
	if err != nil {
		return nil, fmt.Errorf("capture-screen command %q (%s): %w", opts.CommandName, id, err)
	}
	return content, nil
}
