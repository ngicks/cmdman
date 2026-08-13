package mux

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/model"
)

// FrameCommandName returns V7's identity for the i-th entry of the named def:
// the name of the supervised command a `managed: true` entry is shown through.
//
// The name is the whole identity — an entry whose command: changed adopts the
// process already running under it (V7's recorded trade-off), because a frame
// verb has no way to tell "the same fixture, edited" from "a different one" and
// killing the user's long-lived process is the worse guess of the two.
func FrameCommandName(defName string, i int) string {
	return "frame-" + defName + "-" + strconv.Itoa(i)
}

// CommandSpec is a supervised command [FrameSvc.CreateCommand] is asked to
// make. It carries what the frame verbs decide and nothing else: everything it
// leaves out is the service's default.
type CommandSpec struct {
	// Name is the command's cmdman name, its identity for every later lookup.
	Name string
	// Argv is the command line run under cmdman supervision.
	Argv []string
	// Tty runs the command on a terminal rather than on pipes.
	Tty bool
	// Hooks is the command's own hooks: config (D17/D40), the layer that beats
	// the config-level default_hooks.
	Hooks model.HookSet
}

// CommandStatus is one supervised command as [FrameSvc.ListCommands] reports
// it: the name it answers to, the id every other call names it by, and the
// state it is in.
type CommandStatus struct {
	ID    string
	Name  string
	State model.EventType
}

// FrameSvc is what the frame verbs need from cmdman's supervised commands: the
// three primitives a managed entry's command is ensured with (see
// [ensureFrameCommand]), and the resolved configuration a viewer spawned in a
// frame pane must be pointed at.
//
// It is declared at the consumer per the small-interface rule, and it must be:
// naming *cmdman.Service in [FrameOptions] would close an import cycle, since
// cmdman imports this package. That is also why the primitives are stated in
// terms this package can name rather than in the service's own request types.
//
// No cmdman type answers to it directly. The service's API is stated in the
// service's own types, and translating the two — a spec into a create request,
// a listing into these triples — belongs to the layer that may import both:
// cmdman/cli, whose FrameSvc is what the verbs are handed.
//
// Note what it cannot express: nothing here stops, removes, or signals a
// command. A managed entry outliving the frame that shows it (D19) is not a
// discipline the frame verbs keep — it is all they are able to do.
type FrameSvc interface {
	// Config reports the resolved cmdman configuration the service runs
	// against. DataDir/RuntimeDir go into the viewer's argv so a frame pane
	// talks to the same cmdman runtime as the verb that docked it.
	Config() config.Config
	// ListCommands reports every command the service holds, in every state.
	// Which of them answers to a managed entry's name is [ensureFrameCommand]'s
	// to decide, not the service's — a whole listing is the most the seam asks
	// for, and the least that can be read as a frame-shaped query.
	ListCommands(ctx context.Context) ([]CommandStatus, error)
	// CreateCommand creates the command spec describes and returns its id. It
	// does not start it.
	CreateCommand(ctx context.Context, spec CommandSpec) (id string, err error)
	// Start starts the command with the given id. It is cmdman's own verb,
	// reused under its own name: the seam adds no wrapper for what the service
	// already exposes.
	Start(ctx context.Context, idOrName string) error
}

// frameCommand is what a `managed: true` frame entry (D19) asks for: an
// identity and the command line behind it. It is what [ensureFrameCommand]
// resolves to a running supervised command.
type frameCommand struct {
	// Name is the command's cmdman name — V7's identity for a managed entry,
	// which [FrameCommandName] forms out of the def name and the entry index.
	Name string
	// Argv is the entry's command:, run under cmdman supervision instead of in
	// the pane.
	Argv []string
	// Hooks is the entry's hooks: override (D17/V5), destined for the created
	// command's own config — which is what makes it beat the config-level
	// default_hooks, through the per-command precedence that already exists.
	Hooks model.HookSet
}

// ensureFrameCommand gives a managed frame entry the supervised command it is
// shown through: the one already named req.Name — adopted as it stands when it
// is running, restarted when it is not — else a freshly created and started one
// (V7). Either way the returned id is what the entry's frame pane attaches to.
//
// Identity is the name and nothing else: an entry whose command: was edited
// adopts the process still running under the old argv until it is stopped by
// hand. That is V7's recorded trade-off — a frame verb cannot tell an edited
// fixture from a different one, and killing a long-lived process on that guess
// is the worse half of it; `cmdman stop frame-<def>-<i>` is the way out, and
// content-hash identity the upgrade path if it hurts in practice.
func ensureFrameCommand(ctx context.Context, svc FrameSvc, req frameCommand) (string, error) {
	if req.Name == "" {
		return "", errors.New("frame command: name is required")
	}

	// A listing, not a per-name query: a service listing sweeps commands whose
	// monitor is gone into failed first, so a running state read here means a
	// live process rather than a stale row, and a command that was never created
	// is a name missing from the list rather than an error to match against.
	commands, err := svc.ListCommands(ctx)
	if err != nil {
		return "", fmt.Errorf("frame command %q: %w", req.Name, err)
	}
	i := slices.IndexFunc(commands, func(c CommandStatus) bool { return c.Name == req.Name })
	if i < 0 {
		return createFrameCommand(ctx, svc, req)
	}

	existing := commands[i]
	switch existing.State {
	case model.EventTypeRunning, model.EventTypeStarting:
		return existing.ID, nil
	}
	// Created, exited or failed: the record is the identity, so it is started
	// again rather than replaced — a frame entry that outlived its process comes
	// back under the same name, with the same history behind it.
	if err := svc.Start(ctx, existing.ID); err != nil {
		return "", fmt.Errorf("frame command %q: start: %w", req.Name, err)
	}
	return existing.ID, nil
}

func createFrameCommand(ctx context.Context, svc FrameSvc, req frameCommand) (string, error) {
	id, err := svc.CreateCommand(ctx, frameCommandSpec(req))
	if err != nil {
		return "", fmt.Errorf("frame command %q: create: %w", req.Name, err)
	}
	if err := svc.Start(ctx, id); err != nil {
		return "", fmt.Errorf("frame command %q: start: %w", req.Name, err)
	}
	return id, nil
}

// frameCommandSpec is the command a managed frame entry becomes.
//
// Tty because the entry is a display fixture: it is watched through `cmdman
// attach` in a pane sized by the frame, so it gets a terminal the way an
// interactive command does rather than plain pipes. Hooks carry the entry's
// hooks: override into the command's own config, which is what makes it beat
// the config-level default_hooks (D17/V5) — through the per-command precedence
// that already exists, with no frame-shaped layer of its own.
//
// Everything else is left at the service defaults, working directory included:
// a frame is a per-window fixture with no project behind it, so the directory
// the verb happened to run in is not the fixture's.
func frameCommandSpec(req frameCommand) CommandSpec {
	return CommandSpec{
		Name:  req.Name,
		Argv:  req.Argv,
		Tty:   true,
		Hooks: req.Hooks,
	}
}
