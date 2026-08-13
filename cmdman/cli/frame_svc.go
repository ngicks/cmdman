package cli

import (
	"context"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/mux"
)

// FrameSvc is what the frame verbs (D19/V7) are handed in place of a cmdman
// service: a *cmdman.Service seen through [mux.FrameSvc].
//
// It lives here because this is the layer that may import both halves. The mux
// package cannot name cmdman types — cmdman imports it — and the cmdman service
// answers in its own types on purpose, so something has to translate a
// [mux.CommandSpec] into a create request and a command listing into the
// triples the verbs read. That translation is all this is: no decision about
// which command a frame entry gets, when it is started, or what it is created
// with is made here. Those live in cmdman/mux, with the rest of frame
// semantics.
type FrameSvc struct {
	svc *cmdman.Service
}

// The verbs' seam and its one implementation, checked against each other.
var _ mux.FrameSvc = (*FrameSvc)(nil)

// NewFrameSvc adapts svc for the frame verbs.
//
// It returns the interface rather than the adapter so that a nil svc yields a
// genuine nil [mux.FrameSvc] — the value meaning "no service behind the verbs",
// which a def of components and ephemeral commands is still fully usable with —
// instead of a non-nil interface holding a typed-nil pointer, which would pass
// the verbs' nil check and then dereference.
func NewFrameSvc(svc *cmdman.Service) mux.FrameSvc {
	if svc == nil {
		return nil
	}
	return &FrameSvc{svc: svc}
}

// Config reports the resolved configuration the service runs against, whose
// DataDir/RuntimeDir a frame pane's viewer is pointed at.
func (a *FrameSvc) Config() cmdman.CmdmanConfig {
	return a.svc.Config()
}

// ListCommands projects the service's listing onto the triples the frame verbs
// read. AllStates because the verbs decide what a state means: a command that
// is not running is theirs to start again, so it must be in the list to be
// found at all.
func (a *FrameSvc) ListCommands(ctx context.Context) ([]mux.CommandStatus, error) {
	entries, err := a.svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return nil, err
	}
	out := make([]mux.CommandStatus, len(entries))
	for i, e := range entries {
		out[i] = mux.CommandStatus{ID: e.ID, Name: e.Name, State: e.State}
	}
	return out, nil
}

// CreateCommand creates the command spec describes. The fields spec does not
// carry stay at the service defaults; the ones it does were chosen by the
// caller.
func (a *FrameSvc) CreateCommand(ctx context.Context, spec mux.CommandSpec) (string, error) {
	res, err := a.svc.Create(ctx, cmdman.CreateRequest{
		Name:  spec.Name,
		Argv:  spec.Argv,
		Tty:   spec.Tty,
		Hooks: spec.Hooks,
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// Start starts the command with the given id.
func (a *FrameSvc) Start(ctx context.Context, idOrName string) error {
	return a.svc.Start(ctx, idOrName)
}
