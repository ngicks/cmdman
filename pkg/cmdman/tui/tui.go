// Package tui implements the interactive terminal UI for `cmdman tui`.
//
// The TUI is a multi-tab browser over compose-managed commands. It uses
// bubbletea as the renderer. Unlike pkg/cmdman/cli/progress_tty.go (which
// deliberately avoids a full TUI framework because framework startup queries
// the terminal for the whole binary and can corrupt the PTY of sibling
// subcommands such as `compose attach`), the tui subcommand is its own
// standalone process and does not spawn sibling subcommands that share its
// PTY, so the framework-startup concern does not apply here. Attach is handled
// through an explicit terminal handoff (see attach handling in the runtime
// layer), not by spawning a sibling command under the TUI's PTY.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// CommandInfo is the compose-scoped command row data the model renders. It is
// a backend-neutral projection of a store command entry so the model can be
// exercised without a live service.
type CommandInfo struct {
	ID        string
	Name      string
	Project   string
	Workdir   string
	State     model.EventType
	ExitCode  *int
	LogDriver logdriver.LogDriver
}

// ProjectInfo is the Compose-tab row data for a discovered compose project.
type ProjectInfo struct {
	Name     string
	Path     string
	Workdir  string
	Commands int
	Running  int
	Exited   int
	Failed   int
	Active   bool
	HasMux   bool
	Modified string
}

// Backend abstracts the cmdman/compose services the TUI talks to. It exists so
// the model can be exercised without a live service. Methods that perform I/O
// take a context and run off the bubbletea update loop; their results are
// delivered back as messages.
//
// The interface grows across the TUI feature set: the core shell only needs
// list loading and the working directory; runtime (events, preview, actions,
// attach) and mux methods are layered on by their respective concerns.
type Backend interface {
	// ListCommands returns compose-scoped command entries (already filtered to
	// entries carrying the compose project/workdir labels).
	ListCommands(ctx context.Context) ([]CommandInfo, error)
	// ListProjects returns compose project summaries for the Compose tab.
	ListProjects(ctx context.Context) ([]ProjectInfo, error)
	// Cwd returns the normalized current working directory used for
	// active-project detection. It returns "" when the working directory
	// cannot be determined.
	Cwd() string

	// Start starts a command that is not currently started or starting.
	Start(ctx context.Context, id string) error
	// Stop stops a running command using service defaults for signal/timeout.
	Stop(ctx context.Context, id string) error
	// Restart restarts a command using service defaults for signal/timeout.
	Restart(ctx context.Context, id string) error
	// Remove removes a command. force sends SIGKILL before removal and is
	// required when the command is running.
	Remove(ctx context.Context, id string, force bool) error

	// Events subscribes to lifecycle change signals. Each signal is a cue to
	// re-list; the stream is a local event-log tail, not a network stream.
	Events(ctx context.Context) (EventStream, error)
	// Logs opens a Tail+Follow reader for the preview pane. tail sizes the
	// initial snapshot.
	Logs(ctx context.Context, id string, tail int) (LogStream, error)
	// Attach hands the terminal to an attach session for the command and
	// returns an outcome ("detached" or "exited") plus any real error. It is
	// invoked from a released-terminal context (tea.Exec).
	Attach(ctx context.Context, id string) (outcome string, err error)

	// CycleMux cycles the mux layout for a compose project by invoking the
	// existing compose mux path. The TUI does not track layout state; mux owns
	// its persisted tmux window marker. projectName identifies the project and
	// composeFile (may be empty) is its compose file path.
	CycleMux(ctx context.Context, projectName, composeFile string) error
}

// EventSignal is one lifecycle change cue. A non-nil Err is a local event-tail
// error to surface in the footer without closing the TUI.
type EventSignal struct {
	Err error
}

// EventStream delivers lifecycle change signals until closed.
type EventStream interface {
	Signals() <-chan EventSignal
	Close() error
}

// LogLine is one preview line; a non-nil Err is a read error to surface.
type LogLine struct {
	Text string
	Err  error
}

// LogStream delivers preview lines (Tail snapshot then Follow) until closed.
type LogStream interface {
	Lines() <-chan LogLine
	Close() error
}

// Attach outcomes.
const (
	AttachDetached = "detached"
	AttachExited   = "exited"
)

// Options configures a TUI run.
type Options struct {
	// Backend is the data/command source. Required.
	Backend Backend
	// Version is the version string shown at the right edge of the footer.
	Version string
	// AltScreen runs the program in the alternate screen buffer. Direct mode
	// uses the alternate screen; popup mode may opt out.
	AltScreen bool
	// PopupMode indicates the TUI runs inside a multiplexer popup. In popup
	// mode, mux layout actions rearrange the underlying window safely; in
	// direct mode they would clobber the TUI, so they require a warning first.
	PopupMode bool
}

// Run starts the TUI program and blocks until it exits.
func Run(ctx context.Context, opts Options) error {
	m := New(opts)
	m.ctx = ctx
	var teaOpts []tea.ProgramOption
	teaOpts = append(teaOpts, tea.WithContext(ctx))
	if opts.AltScreen {
		teaOpts = append(teaOpts, tea.WithAltScreen())
	}
	p := tea.NewProgram(m, teaOpts...)
	_, err := p.Run()
	return err
}

// New constructs the root model.
func New(opts Options) Model {
	return Model{
		backend:   opts.Backend,
		version:   opts.Version,
		popupMode: opts.PopupMode,
		active:    tabCommands,
		commands: commandsTab{
			fold:  map[string]bool{},
			focus: paneList,
		},
	}
}
