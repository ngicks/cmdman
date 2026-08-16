package core

import (
	"context"

	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
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

	// ScaleIndex and ScaleCount are the command's replica identity within its
	// compose command: its 1-based index among the replicas, and the replica
	// count desired as of the create that stamped its labels. Both are zero for
	// a command that is not one replica among several — a standalone command,
	// or a compose command running a single instance — so the zero value reads
	// as "unscaled" and there is nothing for a scale badge to say.
	ScaleIndex int
	ScaleCount int

	// Title, Status, Detail and BellUnread are the runtime state a command
	// reports about its current run: the title it set, the status it reported
	// (working/waiting/done, "" when it reported none) with its detail, and
	// whether a bell is still unread.
	//
	// The production listing leaves all four zero — no store entry speaks for
	// them, and rows get them from the runtime-state streams the watcher holds
	// (see [RuntimeWatcher]). They are here so a Backend that does serve a
	// snapshot — the test fakes — can seed a row without a stream, and the zero
	// value reads as "nothing said so far" rather than as a claim about the
	// command.
	Title      string
	Status     string
	Detail     string
	BellUnread bool
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

	// Identity is the project's multiplexer ownership stamp — the key
	// [Backend.SwitchToProject] finds the project's window by, and stamps on the
	// one it creates. It is opaque here: only the backend can derive it, and it
	// is "" for a project no window could ever be addressed for (a named def
	// that has never run anywhere in particular).
	Identity string
}

// SwitchTarget is the project a switcher selection lands in. Identity addresses
// the window; WorkDir and Project describe the project well enough to build one
// when no window carries that stamp yet.
type SwitchTarget struct {
	// Identity is the multiplexer ownership stamp carried from
	// [ProjectInfo.Identity]. An empty Identity is not a switch: the landing
	// would fall back to the bare `cmdman-<project>` stamp, which a same-named
	// project under another work directory can wear, so the backend refuses it.
	Identity string
	// WorkDir is the project's directory — where a created window's shell opens.
	WorkDir string
	// Project is the compose project name, which names a created window.
	Project string
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
	// WatchRuntimeState subscribes to one command's monitor runtime-state
	// stream: an initial snapshot, then a push per change. The stream closes
	// when the monitor leaves an active state.
	WatchRuntimeState(ctx context.Context, id string) (RuntimeStateStream, error)
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

	// SwitchToProject puts the client in front of the target's window — the
	// docked switcher's enter/click (D6). A project with no window of its own
	// gets one built for it and lands in that; what it does not get is a
	// bring-up, which stays the launcher's gesture. A landing that fails comes
	// back as an error, which the switcher shows in place of its hint line.
	SwitchToProject(ctx context.Context, target SwitchTarget) error
	// HideFrame takes the frame down around the caller's current window — the
	// docked switcher's collapse gesture (D16/V8). A window with no frame up is
	// not an error: hiding what is already hidden is what was asked for.
	HideFrame(ctx context.Context) error

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

	// ListLaunchTargets returns the launcher's merged list: compose history,
	// the ListProjects merge, and the projects whose mux window is currently
	// running, grouped by target directory and ordered by recency (D7). Git
	// info is read per entry as the listing is built (D41).
	ListLaunchTargets(ctx context.Context) ([]LaunchLocation, error)
	// ResolveLaunchDir builds the location row for one directory on its own, so
	// a path the user typed can be selected even though it is not among the
	// listed locations (D28). dir is an absolute path to an existing directory;
	// the row is what ListLaunchTargets would have produced for it — the compose
	// projects discovered there merged with the directory's history, git info
	// read the same way. A directory with neither compose file nor history is
	// still a location: it comes back with no projects, and the caller decides
	// what that renders as. Only a failure that hides what is there is an error.
	ResolveLaunchDir(ctx context.Context, dir string) (LaunchLocation, error)
	// StartProject brings a project up in the background without moving focus —
	// the launcher's `s` (D4). It returns when the bring-up reached its terminal
	// phase, which is what stops the row's spinner.
	StartProject(ctx context.Context, target LaunchTarget) error
	// LaunchProject brings a project up and lands focus inside it — the
	// launcher's `S` (D4/D10). It returns once the caller is (or can be) looking
	// at the project's window; see LaunchOutcome for the two endings that are
	// not simply "done": D9's mux-less warning and D8's attach handoff.
	LaunchProject(ctx context.Context, target LaunchTarget) (LaunchOutcome, error)
	// ForgetLaunchTarget removes a project's compose-history entry, the offer
	// made when its compose file no longer resolves (D10/Q12). Forgetting a
	// project that was never recorded is not an error: the same offer is made
	// for a discovered project whose file has gone.
	ForgetLaunchTarget(ctx context.Context, target LaunchTarget) error
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

// RuntimeStateView is the pushed runtime state, mirroring the
// Title/Status/Detail/BellUnread fields on CommandInfo.
type RuntimeStateView struct {
	Title      string
	Status     string
	Detail     string
	BellUnread bool
}

// RuntimeStateUpdate is one stream message; a non-nil Err is a terminal read
// error (the channel closes after it).
type RuntimeStateUpdate struct {
	State RuntimeStateView
	Err   error
}

// RuntimeStateStream delivers runtime-state updates until closed.
type RuntimeStateStream interface {
	Updates() <-chan RuntimeStateUpdate
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

// RawChunk is one message from an attach stream: raw stdout bytes, a PTY size
// report (Resize != nil), or a read error (Err != nil).
type RawChunk struct {
	Bytes  []byte
	Resize *RawSize
	Err    error
}

// RawSize is a reported PTY window size, used to size the terminal-view emulator
// to the command's actual render dimensions.
type RawSize struct {
	Rows int
	Cols int
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
	// Widget restricts the run to a single widget view — no tab bar, no tab
	// switching, the widget owns the whole pane. The zero value, WidgetNone,
	// runs the full multi-tab TUI, so leaving it unset changes nothing.
	Widget Widget
	// NoQuit unbinds the quit keys (V6). A widget docked in a frame pane always
	// runs with it: a pane whose viewer exits leaves a dead hole in the fixture,
	// so quitting is a standalone-run affordance.
	NoQuit bool
}
