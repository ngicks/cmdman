package core

import (
	"context"
	"fmt"
	"maps"

	tea "charm.land/bubbletea/v2"
)

// CommandsLoadedMsg carries the result of a ListCommands load.
type CommandsLoadedMsg struct {
	Infos []CommandInfo
	Err   error
}

// ProjectsLoadedMsg carries the result of a ListProjects load.
type ProjectsLoadedMsg struct {
	Infos []ProjectInfo
	Err   error
}

// ActiveIdentityLoadedMsg carries the active project's mux ownership stamp (see
// Backend.ActiveIdentity). OK false means no probe answered and the consumer is
// back to matching the working directory.
type ActiveIdentityLoadedMsg struct {
	Identity string
	OK       bool
}

// ProjectSwitchedMsg reports a switcher selection: the client either moved to
// the project's window or came back with the reason it did not.
type ProjectSwitchedMsg struct {
	Name string
	Err  error
}

// ProjectManagerSummonedMsg reports a summon: the popup ran to its end, or came
// back with the reason there was no popup to run it in (D4).
type ProjectManagerSummonedMsg struct {
	Name string
	Err  error
}

// DownTarget is the project a teardown acts on: its name plus the compose file
// and work directory that complete it, since a compose file names a project
// only together with the directory it stands in.
//
// The zero value names no project, which is what a widget holds while no
// teardown is waiting to be confirmed — no row a widget can act on has an empty
// project name, so the two states cannot be confused.
type DownTarget struct {
	Project string
	Path    string
	WorkDir string
}

// MuxDownMsg reports a dashboard teardown reaching its end.
//
// Target repeats what the teardown acted on. Name is the line's wording and a
// project name alone does not pick out a row, so a consumer that has to find
// the row again reads Target instead of matching on Name.
type MuxDownMsg struct {
	Name   string
	Target DownTarget
	Err    error
}

// Status is the line a widget puts on its status line for a finished dashboard
// teardown. It says what is still up: the dashboard is only a viewer of the
// project's commands, so tearing it down leaves them running, and a user who
// reads "down" alone would think otherwise.
func (msg MuxDownMsg) Status() string {
	if msg.Err != nil {
		return fmt.Sprintf("mux down %s: %v", msg.Name, msg.Err)
	}
	return "mux down " + msg.Name + " — commands still running"
}

// ComposeDownMsg reports a compose teardown reaching its end, with what it did.
// Target carries the project it acted on for the same reason MuxDownMsg does.
//
// Earlier marks the end of a teardown that was over before the widget asked
// about it: the last down of a project a widget opened on. It is a report
// rather than news, and a listing the widget loaded before asking already
// shows what it left behind.
type ComposeDownMsg struct {
	Name    string
	Target  DownTarget
	Summary DownSummary
	Err     error
	Earlier bool
}

// Status is the finished teardown's line. The counts come first and are said
// whether or not it failed: a teardown that gave up part-way still tore the
// rest down, and hiding that behind the error would leave the user guessing
// what is left. The forced kills are said only when there were any: a stop that
// resorted to SIGKILL may have left detached processes behind. The unreleased
// resources are said only when there were any too. A teardown that did not fail
// can still have some, since on_error continue or ignore lets a failed release
// pass.
func (msg ComposeDownMsg) Status() string {
	line := fmt.Sprintf("compose down %s: stopped %d, removed %d",
		msg.Name, msg.Summary.Stopped, msg.Summary.Removed)
	if msg.Summary.ForceKilled > 0 {
		line += fmt.Sprintf(", force-killed %d", msg.Summary.ForceKilled)
	}
	if msg.Summary.Unreleased > 0 {
		line += fmt.Sprintf(", unreleased %d", msg.Summary.Unreleased)
	}
	if msg.Err != nil {
		return line + ": " + msg.Err.Error()
	}
	return line
}

// ComposeDownProgressMsg reports a compose teardown that is still under way,
// with what it has got through so far. Next keeps following it.
type ComposeDownProgressMsg struct {
	Name    string
	Target  DownTarget
	Summary DownSummary

	stream DownStream
}

// Status is the line of a teardown still under way. It says the stops alone:
// the removals come after every stop, and a line that already read like the
// finished one would pass for it.
func (msg ComposeDownProgressMsg) Status() string {
	return fmt.Sprintf("compose down %s: running… stopped %d", msg.Name, msg.Summary.Stopped)
}

// Next waits for the teardown's next report: another ComposeDownProgressMsg, or
// the ComposeDownMsg of its end. A widget returns it from Update so the
// following goes on, and the job runs to its end whether or not it does.
func (msg ComposeDownProgressMsg) Next() tea.Cmd {
	if msg.stream == nil {
		return nil
	}
	return func() tea.Msg { return nextComposeDown(msg.stream, msg.Target, false) }
}

// DownFollows is what a widget knows of the compose teardowns it follows that
// are still under way: the latest summary of each, by the project it tears
// down. The zero value follows none.
//
// It is a value: a change returns a new one rather than writing into a map
// every copy of the model shares. bubbletea hands Update a copy of the model,
// and a change written into a shared map would reach a copy that was meant to
// stay as it was.
type DownFollows struct {
	running map[DownTarget]DownSummary
}

// Progress records a teardown still under way.
func (f DownFollows) Progress(msg ComposeDownProgressMsg) DownFollows {
	running := maps.Clone(f.running)
	if running == nil {
		running = map[DownTarget]DownSummary{}
	}
	running[msg.Target] = msg.Summary
	return DownFollows{running: running}
}

// Done forgets a teardown that came to its end.
func (f DownFollows) Done(target DownTarget) DownFollows {
	if _, ok := f.running[target]; !ok {
		return f
	}
	running := maps.Clone(f.running)
	delete(running, target)
	return DownFollows{running: running}
}

// Status is the line of the teardown of target still under way, which is what
// a widget says in place of asking to tear the project down again.
func (f DownFollows) Status(target DownTarget) (string, bool) {
	summary, ok := f.running[target]
	if !ok {
		return "", false
	}
	return ComposeDownProgressMsg{Name: target.Project, Target: target, Summary: summary}.Status(),
		true
}

// ComposeDownPrompt is the question a widget's status line asks before it tears
// a project's commands down; y goes ahead and any other key takes it back.
func ComposeDownPrompt(name string) string { return "compose down " + name + "? y/n" }

// ComposeDownCancelled is what the status line says once it has been taken
// back, so a key that only meant "not that" still gets an answer.
func ComposeDownCancelled(name string) string { return "compose down " + name + " cancelled" }

// ListCommandsCmd and ListProjectsCmd take their backend rather than a model so
// the single-widget model issues the very same loads as the full model.
func ListCommandsCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		infos, err := backend.ListCommands(ctx)
		return CommandsLoadedMsg{Infos: infos, Err: err}
	}
}

func ListProjectsCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		infos, err := backend.ListProjects(ctx)
		return ProjectsLoadedMsg{Infos: infos, Err: err}
	}
}

// ActiveIdentityCmd asks which project the caller is sitting in (D3). The probe
// talks to the multiplexer, so it runs off the update loop beside the listings
// rather than as the plain accessor Cwd() is.
func ActiveIdentityCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		identity, ok := backend.ActiveIdentity(ctx)
		return ActiveIdentityLoadedMsg{Identity: identity, OK: ok}
	}
}

// SwitchProjectCmd stands free of a model for the same reason the list commands
// do: the widget model issues it off the update loop with no model of its own
// to carry.
func SwitchProjectCmd(
	ctx context.Context,
	backend Backend,
	target SwitchTarget,
	name string,
) tea.Cmd {
	return func() tea.Msg {
		return ProjectSwitchedMsg{Name: name, Err: backend.SwitchToProject(ctx, target)}
	}
}

// SummonProjectManagerCmd opens the project-manager popup for one project. The
// popup owns the screen for as long as it is up, so the call blocks until it
// closes — which is what makes the reply the cue to re-read what it changed.
func SummonProjectManagerCmd(
	ctx context.Context,
	backend Backend,
	projectName, composeFile, workDir, label string,
) tea.Cmd {
	return func() tea.Msg {
		return ProjectManagerSummonedMsg{
			Name: label,
			Err:  backend.SummonProjectManager(ctx, projectName, composeFile, workDir),
		}
	}
}

// MuxDownCmd and ComposeDownCmd are the two teardowns every project-listing
// widget offers. They live here rather than in one widget because all three
// spell the gesture the same way — d tears the dashboard down, D asks first and
// then takes the commands away — and a widget that worded its own would drift
// from the others one release at a time.
func MuxDownCmd(ctx context.Context, backend Backend, target DownTarget) tea.Cmd {
	return func() tea.Msg {
		return MuxDownMsg{
			Name:   target.Project,
			Target: target,
			Err:    backend.MuxDown(ctx, target.Project, target.Path, target.WorkDir),
		}
	}
}

// ComposeDownCmd launches the project's compose down job and follows it. The
// reply is the job's first report: a ComposeDownProgressMsg whose Next follows
// on, or the ComposeDownMsg of a job that is already over.
func ComposeDownCmd(ctx context.Context, backend Backend, target DownTarget) tea.Cmd {
	return func() tea.Msg {
		job, err := backend.LaunchComposeDown(ctx, target)
		if err != nil {
			return ComposeDownMsg{Name: target.Project, Target: target, Err: err}
		}
		return followComposeDown(ctx, backend, target, job, false)
	}
}

// LastComposeDownCmd reports the latest compose down of the project a widget
// opened on, following it when it is still under way. A project that has had
// no down replies with nothing, and so does a lookup that failed: the widget
// asked out of courtesy rather than for an action, and the listings it loaded
// beside it report a store that cannot be read.
func LastComposeDownCmd(ctx context.Context, backend Backend, target DownTarget) tea.Cmd {
	return func() tea.Msg {
		job, ok, err := backend.FindComposeDown(ctx, target)
		if err != nil || !ok {
			return nil
		}
		return followComposeDown(ctx, backend, target, job, job.Finished)
	}
}

// followComposeDown opens the job's stream and waits for its first report.
// earlier marks the end the stream reports as one that came before the widget
// asked (see ComposeDownMsg.Earlier).
func followComposeDown(
	ctx context.Context,
	backend Backend,
	target DownTarget,
	job DownJob,
	earlier bool,
) tea.Msg {
	stream, err := backend.FollowComposeDown(ctx, job)
	if err != nil {
		return ComposeDownMsg{Name: target.Project, Target: target, Err: err, Earlier: earlier}
	}
	return nextComposeDown(stream, target, earlier)
}

func nextComposeDown(stream DownStream, target DownTarget, earlier bool) tea.Msg {
	summary, ok := <-stream.Summaries()
	if ok {
		return ComposeDownProgressMsg{
			Name:    target.Project,
			Target:  target,
			Summary: summary,
			stream:  stream,
		}
	}
	final, err := stream.Result()
	_ = stream.Close()
	return ComposeDownMsg{
		Name:    target.Project,
		Target:  target,
		Summary: final,
		Err:     err,
		Earlier: earlier,
	}
}
