package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// popupKind identifies which confirmation popup is open.
type popupKind int

const (
	popupNone popupKind = iota
	popupAttach
	popupRemove
	popupForceRemove
	popupMuxWarn
	popupComposeUp
)

// popupState is the pending confirmation dialog. Confirmation popups use a
// two-button selection (an action button and <cancel>); they do not use y/n
// hotkeys.
type popupState struct {
	kind     popupKind
	project  string
	command  string
	targetID string
	path     string // compose file path (mux warn popup)
	layout   string // layout name to apply (mux warn popup); empty cycles instead
	// choice selects between the action button (0) and <cancel> (1).
	choice int
}

func (p popupState) open() bool { return p.kind != popupNone }

// actionLabel returns the label of the popup's action button.
func (p popupState) actionLabel() string {
	switch p.kind {
	case popupAttach:
		return "<yes>"
	case popupRemove:
		return "<yes>"
	case popupForceRemove:
		return "<force remove>"
	case popupMuxWarn:
		return "<continue>"
	case popupComposeUp:
		return "<up>"
	default:
		return "<yes>"
	}
}

func (p popupState) title() string {
	switch p.kind {
	case popupAttach:
		return "Attach to command?"
	case popupRemove:
		return "Remove command?"
	case popupForceRemove:
		return "Force remove running command?"
	case popupMuxWarn:
		return "Rearrange the current tmux window?"
	case popupComposeUp:
		return "Compose up project?"
	default:
		return ""
	}
}

// confirmed reports whether the action button (not <cancel>) is selected.
func (p popupState) confirmed() bool { return p.choice == 0 }

// openAttachPopup opens the attach confirmation, defaulting to <yes>.
func openAttachPopup(project, command, id string) popupState {
	return popupState{
		kind:     popupAttach,
		project:  project,
		command:  command,
		targetID: id,
		choice:   0,
	}
}

// openRemovePopup opens the remove confirmation, defaulting to <cancel>. When
// the command is running, the force variant is used and the body makes the
// SIGKILL behavior explicit.
func openRemovePopup(project, command, id string, running bool) popupState {
	kind := popupRemove
	if running {
		kind = popupForceRemove
	}
	return popupState{kind: kind, project: project, command: command, targetID: id, choice: 1}
}

// openMuxWarnPopup opens the non-popup mux warning, defaulting to <cancel>.
func openMuxWarnPopup(project, path string) popupState {
	return popupState{kind: popupMuxWarn, project: project, path: path, choice: 1}
}

// openLayoutWarnPopup opens the non-popup mux warning for applying a specific
// layout (Layout tab), defaulting to <cancel>. The layout name is carried so the
// confirm path applies that layout instead of cycling.
func openLayoutWarnPopup(project, path, layout string) popupState {
	return popupState{kind: popupMuxWarn, project: project, path: path, layout: layout, choice: 1}
}

// openComposeUpPopup opens the compose-up confirmation, defaulting to <up>
// (running the project is the explicit intent of pressing `a`).
func openComposeUpPopup(project, path string) popupState {
	return popupState{kind: popupComposeUp, project: project, path: path, choice: 0}
}

// toggleChoice moves the popup selection between the action button and <cancel>.
func (p *popupState) toggleChoice() {
	if p.choice == 0 {
		p.choice = 1
	} else {
		p.choice = 0
	}
}

// renderPopup renders the confirmation dialog box.
func (m Model) renderPopup() string {
	p := m.popup
	var b strings.Builder
	b.WriteString(p.title())
	b.WriteString("\n\n")
	switch p.kind {
	case popupMuxWarn:
		fmt.Fprintf(&b, "project: %s\n", p.project)
		if p.layout != "" {
			fmt.Fprintf(&b, "layout: %s\n", p.layout)
		}
		b.WriteString("\nApplying a mux layout will rearrange the current tmux window,\n")
		b.WriteString("including this TUI. Continue?\n")
	case popupComposeUp:
		fmt.Fprintf(&b, "project: %s\n", p.project)
		b.WriteString("\nCreate and start this project's commands (compose up)?\n")
	default:
		fmt.Fprintf(&b, "project: %s\n", p.project)
		fmt.Fprintf(&b, "command: %s\n", p.command)
		if p.kind == popupForceRemove {
			b.WriteString("\nThis sends SIGKILL before removing the command.\n")
		}
	}
	b.WriteByte('\n')

	action := p.actionLabel()
	cancel := "<cancel>"
	var actionR, cancelR string
	if p.choice == 0 {
		actionR = stylePopupSel.Render(action)
		cancelR = stylePopupBtn.Render(cancel)
	} else {
		actionR = stylePopupBtn.Render(action)
		cancelR = stylePopupSel.Render(cancel)
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, actionR, "   ", cancelR))
	return stylePopup.Render(b.String())
}

// renderHelp renders the read-only help overlay for the active tab.
func (m Model) renderHelp() string {
	var b strings.Builder
	b.WriteString("Help — ")
	switch m.active {
	case TabCommands:
		b.WriteString("Commands tab\n\n")
	case TabCompose:
		b.WriteString("Compose tab\n\n")
	default:
		b.WriteString("Layout tab\n\n")
	}
	b.WriteString("Navigation\n")
	b.WriteString("  tab/shift-tab  switch tab\n")
	b.WriteString("  j/k, ↓/↑       move selection\n")
	b.WriteString("  h/l, ←/→       fold project / move pane focus\n")
	b.WriteString("  enter          activate selected item\n")
	b.WriteString("\nFilter\n")
	b.WriteString("  /              focus filter input\n")
	b.WriteString("  esc            leave filter / cancel popup\n")
	switch m.active {
	case TabCommands:
		b.WriteString("\nCommand lifecycle\n")
		b.WriteString("  s  start    S  stop    r  restart\n")
		b.WriteString("  a  attach   x  remove\n")
	case TabCompose:
		b.WriteString("\nCompose\n")
		b.WriteString("  enter  view definition (read-only; j/k scroll, esc close)\n")
		b.WriteString("  e      edit compose file in $VISUAL/$EDITOR/vim\n")
		b.WriteString("  a      compose up (create + start, with confirmation)\n")
		b.WriteString("  c      cycle mux layout\n")
		b.WriteString("  r      refresh project list\n")
	default:
		b.WriteString("\nLayout\n")
		b.WriteString("  enter  apply the selected layout (● marks the current one)\n")
		b.WriteString("  r      refresh layouts\n")
	}
	b.WriteString("\nPopups\n")
	b.WriteString("  ←/→ or tab   move between buttons\n")
	b.WriteString("  enter        confirm    esc  cancel\n")
	b.WriteString("\n  ?  close help    q  quit\n")
	return stylePopup.Render(b.String())
}
