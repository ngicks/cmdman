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

	tea "charm.land/bubbletea/v2"

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
	// Tty reports whether the command runs under a pseudo-terminal. The preview
	// pane uses it (with State) to decide between the vt terminal-view and the
	// sanitized log fallback.
	Tty bool
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

// LayoutsInfo is the Layout-tab data for the current project: its mux layout
// names in definition order plus the running dashboard's current layout marker.
// It is a backend-neutral projection so the model can be exercised without a
// live mux/tmux server.
type LayoutsInfo struct {
	// Project is the resolved current project name (the cwd-active mux project,
	// falling back to the Compose-tab selection).
	Project string
	// Path is the resolved compose file path, carried so an apply can target the
	// same file the listing came from.
	Path string
	// Names are the layout names in definition order.
	Names []string
	// Current is the 0-based index of the layout the running dashboard currently
	// displays, or -1 when no dashboard is running (or the marker is unknown).
	Current int
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

	// Start starts a command that is not currently running or starting.
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
	// RawView opens a read-only raw stdout stream (scrollback replay then live)
	// for the terminal-view preview of a running, TTY-backed command. It never
	// forwards stdin, so the previewed command is unaffected by the preview.
	RawView(ctx context.Context, id string) (RawStream, error)
	// Attach hands the terminal to an attach session for the command and
	// returns an outcome ("detached" or "exited") plus any real error. It is
	// invoked from a released-terminal context (tea.Exec).
	Attach(ctx context.Context, id string) (outcome string, err error)

	// CycleMux cycles the mux layout for a compose project by invoking the
	// existing compose mux path. The TUI does not track layout state; mux owns
	// its persisted tmux window marker. projectName identifies the project and
	// composeFile (may be empty) is its compose file path.
	CycleMux(ctx context.Context, projectName, composeFile string) error

	// ListLayouts returns the current project's mux layouts in definition order
	// plus the running dashboard's current layout marker (see LayoutsInfo). The
	// current project is the cwd-active mux project, falling back to the
	// Compose-tab selection identified by projectName/composeFile (which may be
	// empty when there is no selection).
	ListLayouts(ctx context.Context, projectName, composeFile string) (LayoutsInfo, error)
	// ApplyLayout applies the named layout to the project's running dashboard,
	// starting one at that layout when none is running. It wraps the same compose
	// mux path as CycleMux with an explicit layout selector. projectName/composeFile
	// identify the project as resolved by ListLayouts.
	ApplyLayout(ctx context.Context, projectName, composeFile, layoutName string) error

	// ProjectDefinition returns the raw compose YAML file text for a project, as
	// shown by the read-only definition viewer. projectName identifies the
	// project and composeFile (may be empty) is its compose file path; an empty
	// path is resolved on demand for never-run named projects.
	ProjectDefinition(ctx context.Context, projectName, composeFile string) (string, error)
	// ComposeFilePath resolves the compose file path for a project so the editor
	// handoff can open it. composeFile (may be empty) is used directly when set;
	// an empty path is resolved on demand for never-run named projects.
	ComposeFilePath(ctx context.Context, projectName, composeFile string) (string, error)

	// ComposeUp runs "compose up" for a project and streams per-service progress
	// events for the live progress overlay. projectName identifies the project
	// and composeFile (may be empty) is its compose file path; an empty path is
	// resolved on demand. The returned stream delivers events until the operation
	// finishes (its channel closes), at which point Err reports the
	// operation-level error.
	ComposeUp(ctx context.Context, projectName, composeFile string) (ComposeUpStream, error)
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

// RawChunk is one raw stdout chunk from an attach stream; a non-nil Err is a
// read error to surface in the preview.
type RawChunk struct {
	Bytes []byte
	Err   error
}

// RawStream delivers raw stdout chunks (scrollback replay then live) from a
// read-only attach session until closed. The chunks are fed verbatim into the
// terminal-view emulator; Close releases the underlying attach session.
type RawStream interface {
	Chunks() <-chan RawChunk
	Close() error
}

// ComposeUpEvent is one progress event from a compose up run, projected for the
// progress overlay so the model stays decoupled from the compose package. It
// mirrors a compose lifecycle event (command, phase token, exit code, error)
// plus the phase's terminal/failure classification, which the overlay uses to
// pick the per-service glyph.
type ComposeUpEvent struct {
	// Command is the compose command (service) name.
	Command string
	// Phase is the lifecycle phase token the command transitioned into, e.g.
	// "creating", "running", "exited", "failed".
	Phase string
	// Terminal reports whether Phase is a result rather than work in flight.
	Terminal bool
	// Failed reports whether Phase is a terminal failure.
	Failed bool
	// ExitCode is the observed exit code when known.
	ExitCode *int
	// Err carries the detail for a failure phase.
	Err error
}

// ComposeUpStream delivers compose up progress events for the live overlay until
// closed. The event channel closes when the operation reaches its terminal phase
// (the Up call returned); Err reports the operation-level error and is readable
// once the channel is observed closed.
type ComposeUpStream interface {
	Events() <-chan ComposeUpEvent
	Err() error
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
	// InitialTab selects the tab shown on startup. The zero value is
	// TabCommands, so leaving it unset keeps the default.
	InitialTab Tab
}

// Run starts the TUI program and blocks until it exits.
func Run(ctx context.Context, opts Options) error {
	m := New(opts)
	m.ctx = ctx
	// v2: the alternate screen is requested per-frame via View().AltScreen
	// (see Model.View), not as a program option.
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// New constructs the root model.
func New(opts Options) Model {
	return Model{
		backend:   opts.Backend,
		version:   opts.Version,
		popupMode: opts.PopupMode,
		altScreen: opts.AltScreen,
		active:    opts.InitialTab,
		commands: commandsTab{
			fold:  map[string]bool{},
			focus: paneList,
		},
		// -1 = no dashboard marker known until the first ListLayouts load.
		layout: layoutTab{current: -1},
	}
}
