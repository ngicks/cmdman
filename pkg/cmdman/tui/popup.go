package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// popupKind identifies which confirmation popup is open.
type popupKind int

const (
	popupNone popupKind = iota
	popupAttach
	popupRemove
	popupForceRemove
)

// popupState is the pending confirmation dialog. Confirmation popups use a
// two-button selection (an action button and <cancel>); they do not use y/n
// hotkeys.
type popupState struct {
	kind     popupKind
	project  string
	command  string
	targetID string
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
	default:
		return ""
	}
}

// confirmed reports whether the action button (not <cancel>) is selected.
func (p popupState) confirmed() bool { return p.choice == 0 }

// openAttachPopup opens the attach confirmation, defaulting to <yes>.
func openAttachPopup(project, command, id string) popupState {
	return popupState{kind: popupAttach, project: project, command: command, targetID: id, choice: 0}
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
	fmt.Fprintf(&b, "project: %s\n", p.project)
	fmt.Fprintf(&b, "command: %s\n", p.command)
	if p.kind == popupForceRemove {
		b.WriteString("\nThis sends SIGKILL before removing the command.\n")
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
	if m.active == tabCommands {
		b.WriteString("Commands tab\n\n")
	} else {
		b.WriteString("Compose tab\n\n")
	}
	b.WriteString("Navigation\n")
	b.WriteString("  tab/shift-tab  switch tab\n")
	b.WriteString("  j/k, ↓/↑       move selection\n")
	b.WriteString("  h/l, ←/→       fold project / move pane focus\n")
	b.WriteString("  enter          activate selected item\n")
	b.WriteString("\nFilter\n")
	b.WriteString("  /              focus filter input\n")
	b.WriteString("  esc            leave filter / cancel popup\n")
	if m.active == tabCommands {
		b.WriteString("\nCommand lifecycle\n")
		b.WriteString("  s  start    S  stop    r  restart\n")
		b.WriteString("  a  attach   x  remove\n")
	} else {
		b.WriteString("\nCompose mux\n")
		b.WriteString("  enter  open project in Commands tab\n")
		b.WriteString("  c      cycle mux layout\n")
		b.WriteString("  r      refresh project list\n")
	}
	b.WriteString("\nPopups\n")
	b.WriteString("  ←/→ or tab   move between buttons\n")
	b.WriteString("  enter        confirm    esc  cancel\n")
	b.WriteString("\n  ?  close help    q  quit\n")
	return stylePopup.Render(b.String())
}
