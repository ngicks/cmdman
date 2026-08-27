package core

import (
	"context"
	"fmt"

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
type ComposeDownMsg struct {
	Name    string
	Target  DownTarget
	Summary DownSummary
	Err     error
}

// Status is the finished teardown's line. The counts come first and are said
// whether or not it failed: a teardown that gave up part-way still tore the
// rest down, and hiding that behind the error would leave the user guessing
// what is left. They are worded as what the phases covered rather than as what
// was running, since a command that had already exited counts as stopped.
func (msg ComposeDownMsg) Status() string {
	line := fmt.Sprintf("compose down %s: stopped %d, removed %d",
		msg.Name, msg.Summary.Stopped, msg.Summary.Removed)
	if msg.Err != nil {
		return line + ": " + msg.Err.Error()
	}
	return line
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

func ComposeDownCmd(ctx context.Context, backend Backend, target DownTarget) tea.Cmd {
	return func() tea.Msg {
		summary, err := backend.ComposeDown(ctx, target.Project, target.Path, target.WorkDir)
		return ComposeDownMsg{
			Name:    target.Project,
			Target:  target,
			Summary: summary,
			Err:     err,
		}
	}
}
