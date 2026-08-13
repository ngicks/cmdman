package core

import (
	"fmt"

	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
)

// CommandRow is a single compose-managed command as the lists render it.
type CommandRow struct {
	ID        string
	Name      string
	Project   string
	Workdir   string
	State     model.EventType
	ExitCode  *int
	LogDriver logdriver.LogDriver
	Tty       bool   // command runs under a pseudo-terminal (preview predicate)
	Pending   string // pending action label; empty when no action is in flight

	// Runtime state as of the last load (see CommandInfo): what the command
	// said about itself, as opposed to what the store knows about its process.
	Title  string
	Status string // working, waiting, done, or "" when nothing was reported
	Detail string
	Bell   bool
}

// ProjectGroup groups command rows under a compose project.
type ProjectGroup struct {
	Name    string
	Workdir string
	// Identity is the project's multiplexer ownership stamp, carried from
	// ProjectInfo so the switcher can address the project's window. It is empty
	// for a group the project listing never claimed, which is a group with no
	// window to switch to.
	Identity string
	Active   bool
	Commands []CommandRow
}

func (g ProjectGroup) Key() string { return g.Workdir + "\x00" + g.Name }

// BoolFirst returns -1 when v is true so that active entries sort before
// inactive ones in a stable three-way comparator. Only call it when the two
// compared booleans differ.
func BoolFirst(v bool) int {
	if v {
		return -1
	}
	return 1
}

// DisplayLabel maps a persisted state to a friendly display label. The label is
// the state value itself, except an exited command annotates its exit code. All
// logic elsewhere uses the real state values; this is presentation only.
func DisplayLabel(state model.EventType, exitCode *int) string {
	if state == model.EventTypeExited && exitCode != nil {
		return fmt.Sprintf("exited(%d)", *exitCode)
	}
	return string(state)
}

// LiveReport reports whether a row's runtime state may speak for it. Only a
// running command's does: a run that is over shows its exit state, never its
// last report (D13), and a command with an action in flight is about to change
// state anyway. Every renderer gates on this one predicate so no two surfaces
// disagree about a dead command.
func LiveReport(c CommandRow) bool {
	return c.Pending == "" && c.State == model.EventTypeRunning
}

// GroupFromInfos builds project groups from flat command infos, grouping by
// (workdir, project).
func GroupFromInfos(infos []CommandInfo) []ProjectGroup {
	idx := map[string]int{}
	var groups []ProjectGroup
	for _, ci := range infos {
		k := ci.Workdir + "\x00" + ci.Project
		gi, ok := idx[k]
		if !ok {
			gi = len(groups)
			idx[k] = gi
			groups = append(groups, ProjectGroup{Name: ci.Project, Workdir: ci.Workdir})
		}
		groups[gi].Commands = append(groups[gi].Commands, CommandRow{
			ID:        ci.ID,
			Name:      ci.Name,
			Project:   ci.Project,
			Workdir:   ci.Workdir,
			State:     ci.State,
			ExitCode:  ci.ExitCode,
			LogDriver: ci.LogDriver,
			Tty:       ci.Tty,
			Title:     ci.Title,
			Status:    ci.Status,
			Detail:    ci.Detail,
			Bell:      ci.BellUnread,
		})
	}
	return groups
}
