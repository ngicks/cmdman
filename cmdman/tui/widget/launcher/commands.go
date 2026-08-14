package launcher

import (
	"context"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// launchTargetsLoadedMsg carries the result of the launcher's one listing.
type launchTargetsLoadedMsg struct {
	locs []core.LaunchLocation
	err  error
}

// launcherStartedMsg reports a background bring-up (`s`) reaching its end.
type launcherStartedMsg struct {
	target core.LaunchTarget
	err    error
}

// launcherLandedMsg reports a launch + focus (`S`) reaching its end. A nil error
// means the caller is looking at the project; outcome carries the two endings
// that still need the launcher (D9's warning, D8's attach handoff).
type launcherLandedMsg struct {
	target  core.LaunchTarget
	outcome core.LaunchOutcome
	err     error
}

// launcherAttachedMsg reports the terminal coming back from D8's attach handoff.
type launcherAttachedMsg struct {
	err error
}

// launcherForgotMsg reports the removal of a stale history entry.
type launcherForgotMsg struct {
	target core.LaunchTarget
	err    error
}

// launcherResolvedMsg carries the row a typed path resolved to. dir is the
// directory that was asked for, so a reply the input has since moved past can be
// dropped instead of shown against a query that no longer names it.
type launcherResolvedMsg struct {
	dir string
	loc core.LaunchLocation
	err error
}

func listLaunchTargetsCmd(ctx context.Context, backend core.Backend) tea.Cmd {
	return func() tea.Msg {
		locs, err := backend.ListLaunchTargets(ctx)
		return launchTargetsLoadedMsg{locs: locs, err: err}
	}
}

func resolveLaunchDirCmd(ctx context.Context, backend core.Backend, dir string) tea.Cmd {
	return func() tea.Msg {
		loc, err := backend.ResolveLaunchDir(ctx, dir)
		return launcherResolvedMsg{dir: dir, loc: loc, err: err}
	}
}

func startProjectCmd(ctx context.Context, backend core.Backend, target core.LaunchTarget) tea.Cmd {
	return func() tea.Msg {
		return launcherStartedMsg{target: target, err: backend.StartProject(ctx, target)}
	}
}

func launchProjectCmd(ctx context.Context, backend core.Backend, target core.LaunchTarget) tea.Cmd {
	return func() tea.Msg {
		outcome, err := backend.LaunchProject(ctx, target)
		return launcherLandedMsg{target: target, outcome: outcome, err: err}
	}
}

// attachCmd hands the terminal to the multiplexer (D8). tea.ExecProcess wires
// the real terminal fds to the child and restores them on exit, the same handoff
// the compose tab's editor uses — a launcher summoned from a bare shell has no
// client to switch, so becoming one is the landing.
func attachCmd(argv []string) tea.Cmd {
	cmd := exec.Command(argv[0], argv[1:]...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return launcherAttachedMsg{err: err}
	})
}

func forgetTargetCmd(ctx context.Context, backend core.Backend, target core.LaunchTarget) tea.Cmd {
	return func() tea.Msg {
		return launcherForgotMsg{target: target, err: backend.ForgetLaunchTarget(ctx, target)}
	}
}
