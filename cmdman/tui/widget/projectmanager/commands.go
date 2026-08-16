package projectmanager

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// managerLoadedMsg carries the result of one aggregate load — the whole view,
// which is also what every action's reload asks for again.
type managerLoadedMsg struct {
	info core.ProjectManagerInfo
	err  error
}

// scaleSetMsg reports a replica-count change reaching its end. service and
// replicas are what was asked for, so the outcome can name it without reading
// a selection that may have moved on.
type scaleSetMsg struct {
	service  string
	replicas int
	err      error
}

// scaleCycledMsg reports a shown-replica cycle reaching its end.
type scaleCycledMsg struct {
	service string
	err     error
}

// layoutAppliedMsg and layoutCycledMsg report the two layout actions.
type layoutAppliedMsg struct {
	layout string
	err    error
}

type layoutCycledMsg struct {
	err error
}

// The commands take their backend and the project rather than a model, the way
// core's own do: the widget issues them off the update loop and the reply is
// matched against what was asked for, not against the model that asked.
//
// Every action names the project the load resolved — file, name and work
// directory all three (D20). The panel is summoned into, or bound to, a window
// that stands somewhere else entirely, so an action that named less than the
// load did would act on a different project than the one it is showing.
func loadCmd(ctx context.Context, backend core.Backend, project, path string) tea.Cmd {
	return func() tea.Msg {
		info, err := backend.ProjectManager(ctx, project, path)
		return managerLoadedMsg{info: info, err: err}
	}
}

func setScaleCmd(
	ctx context.Context,
	backend core.Backend,
	project, path, workDir, service string,
	replicas int,
) tea.Cmd {
	return func() tea.Msg {
		return scaleSetMsg{
			service:  service,
			replicas: replicas,
			err:      backend.SetScale(ctx, project, path, workDir, service, replicas),
		}
	}
}

func cycleScaleCmd(
	ctx context.Context,
	backend core.Backend,
	project, path, workDir, service string,
	set int,
) tea.Cmd {
	return func() tea.Msg {
		return scaleCycledMsg{
			service: service,
			err:     backend.CycleScale(ctx, project, path, workDir, service, set),
		}
	}
}

func applyLayoutCmd(
	ctx context.Context,
	backend core.Backend,
	project, path, workDir, name string,
) tea.Cmd {
	return func() tea.Msg {
		return layoutAppliedMsg{
			layout: name,
			err:    backend.ApplyLayout(ctx, project, path, workDir, name),
		}
	}
}

func cycleLayoutCmd(
	ctx context.Context,
	backend core.Backend,
	project, path, workDir string,
) tea.Cmd {
	return func() tea.Msg {
		return layoutCycledMsg{err: backend.CycleMux(ctx, project, path, workDir)}
	}
}
