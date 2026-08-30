package cli

import (
	"context"
	"fmt"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// attachSvc is what attaching to a supervised command by id or name needs from
// the cmdman service: the command's config and state, a fresh attach stream,
// and the restart the sticky wait prompt offers.
type attachSvc interface {
	Inspect(ctx context.Context, idOrName string) (*cmdman.InspectOutput, error)
	OpenAttachSession(ctx context.Context, idOrName string) (*cmdman.Session, error)
	Restart(ctx context.Context, req cmdman.RestartRequest) ([]cmdman.RestartResult, error)
}

// AttachCommand opens one attach session against idOrName and runs [Attach] on
// it, closing the session on return.
//
// [ErrRemoteEOF] is passed through: whether a command that goes away ends the
// process quietly is the caller's policy.
func AttachCommand(
	ctx context.Context,
	svc attachSvc,
	idOrName string,
	opts AttachOptions,
) error {
	opts = completeWorkDir(ctx, svc, idOrName, opts)

	session, err := svc.OpenAttachSession(ctx, idOrName)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	return Attach(ctx, session, opts)
}

// AttachCommandSticky runs [AttachSticky] against idOrName with its hooks bound
// to svc, so a command that exits drops the viewer to the wait prompt instead of
// ending the session.
func AttachCommandSticky(
	ctx context.Context,
	svc attachSvc,
	idOrName string,
	opts AttachOptions,
) error {
	opts = completeWorkDir(ctx, svc, idOrName, opts)
	return AttachSticky(ctx, stickyHooksFor(svc, idOrName), opts)
}

// completeWorkDir fills an unset WorkDir from the command's configured
// directory, so every attach entry point moves the viewer there without
// repeating the lookup — and none of them can forget to.
//
// Only the pane path depends on it, so a command config that cannot be read
// leaves it empty rather than failing an attach that would otherwise work.
func completeWorkDir(
	ctx context.Context,
	svc attachSvc,
	idOrName string,
	opts AttachOptions,
) AttachOptions {
	if opts.WorkDir != "" {
		return opts
	}
	out, err := svc.Inspect(ctx, idOrName)
	if err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).DebugContext(
			ctx, "attach: resolve work dir", "command", idOrName, "error", err,
		)
		return opts
	}
	if out.Config == nil {
		return opts
	}
	opts.WorkDir = out.Config.Dir
	return opts
}

// stickyHooksFor binds [StickyHooks] to one command on svc.
func stickyHooksFor(svc attachSvc, idOrName string) StickyHooks {
	return StickyHooks{
		State: func(ctx context.Context) (StickyState, error) {
			out, err := svc.Inspect(ctx, idOrName)
			if err != nil {
				return StickyState{Status: fmt.Sprintf("inspect failed: %v", err)}, nil
			}
			return renderStickyState(out.State, out.ExitCode), nil
		},
		OpenSession: func(ctx context.Context) (AttachSession, error) {
			return svc.OpenAttachSession(ctx, idOrName)
		},
		Restart: func(ctx context.Context) error {
			results, err := svc.Restart(ctx, cmdman.RestartRequest{Targets: []string{idOrName}})
			if err != nil {
				return err
			}
			for _, r := range results {
				if r.Err != nil {
					return r.Err
				}
			}
			return nil
		},
	}
}

// renderStickyState turns a state + optional exit code into a [StickyState].
// Running is true only when the command is observably alive.
func renderStickyState(state model.EventType, exitCode *int) StickyState {
	switch state {
	case model.EventTypeRunning, model.EventTypeStarting:
		return StickyState{Running: true, Status: string(state)}
	case model.EventTypeExited, model.EventTypeFailed:
		if exitCode != nil {
			return StickyState{Status: fmt.Sprintf("%s (code %d)", state, *exitCode)}
		}
		return StickyState{Status: string(state)}
	default:
		// Created, Stopped, "" / unknown
		s := string(state)
		if s == "" {
			s = "not running"
		}
		return StickyState{Status: s}
	}
}
