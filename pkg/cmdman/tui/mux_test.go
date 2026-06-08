package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// composeSeed builds a model on the Compose tab with two projects: one with a
// mux section, one without.
func composeSeed(popupMode bool) Model {
	m := New(Options{Backend: &fakeBackend{cwd: "/work/local-dev"}, PopupMode: popupMode})
	m.cwd = "/work/local-dev"
	m.active = tabCompose
	m.setComposeRows([]composeRow{
		{
			name:     "local-dev",
			workdir:  "/work/local-dev",
			path:     "/cfg/local-dev.yaml",
			commands: 2,
			hasMux:   true,
		},
		{
			name:     "tools",
			workdir:  "/other",
			path:     "/cfg/tools.yaml",
			commands: 0,
			modified: "modified 2026-05-20",
		},
	})
	return m
}

func TestComposeTabShowsMuxBadge(t *testing.T) {
	m := composeSeed(false)
	out := m.renderComposeBody(90, 8)
	if !strings.Contains(out, "mux") {
		t.Fatalf(
			"compose tab should render the mux badge for projects with a mux section:\n%s",
			out,
		)
	}
	if !strings.Contains(out, "modified 2026-05-20") {
		t.Fatalf("non-mux project should show its compact metadata instead:\n%s", out)
	}
}

func TestCycleMuxNoSectionReportsStatus(t *testing.T) {
	m := composeSeed(true)
	// Select the "tools" row (no mux). local-dev is active so it sorts first.
	m.compose.selected = 1
	row, _ := m.compose.selectedComposeRow()
	if row.name != "tools" {
		t.Fatalf("precondition: tools should be selected, got %q", row.name)
	}
	m, cmd := upd(m, kr("c"))
	if cmd != nil {
		t.Fatalf("cycling a project without mux should not invoke mux")
	}
	if !strings.Contains(m.status, "no mux section") {
		t.Fatalf("status should explain the missing mux section, got %q", m.status)
	}
}

func TestCycleMuxPopupModeInvokesImmediately(t *testing.T) {
	m := composeSeed(true) // popup mode
	m.compose.selected = 0 // local-dev (has mux)
	m, cmd := upd(m, kr("c"))
	if m.popup.open() {
		t.Fatalf("popup mode should not show a warning before cycling")
	}
	if cmd == nil {
		t.Fatalf("c should invoke the mux cycle in popup mode")
	}
	msg := cmd()
	done, ok := msg.(muxDoneMsg)
	if !ok || done.project != "local-dev" {
		t.Fatalf("expected a muxDoneMsg for local-dev, got %#v", msg)
	}
	fb := m.backend.(*fakeBackend)
	if len(fb.muxCycled) != 1 || fb.muxCycled[0] != "local-dev" {
		t.Fatalf("CycleMux should target local-dev, got %v", fb.muxCycled)
	}
}

func TestCycleMuxDirectModeWarnsFirst(t *testing.T) {
	m := composeSeed(false) // direct mode
	m.compose.selected = 0  // local-dev (has mux)
	m, cmd := upd(m, kr("c"))
	if cmd != nil {
		t.Fatalf("direct mode should not invoke mux before confirmation")
	}
	if m.popup.kind != popupMuxWarn {
		t.Fatalf("direct-mode mux should warn before rearranging the window")
	}
	if m.popup.confirmed() {
		t.Fatalf("mux warning should default to <cancel>")
	}
	fb := m.backend.(*fakeBackend)
	if len(fb.muxCycled) != 0 {
		t.Fatalf("no mux invocation should happen before confirmation")
	}

	// Confirm: move to <continue> and press enter.
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m, cmd = upd(m, kEnter)
	if cmd == nil {
		t.Fatalf("confirming the warning should invoke the mux cycle")
	}
	if _, ok := cmd().(muxDoneMsg); !ok {
		t.Fatalf("confirmed warning should produce a muxDoneMsg")
	}
}

func TestMuxWarnCancelDoesNotCycle(t *testing.T) {
	m := composeSeed(false)
	m.compose.selected = 0
	m, _ = upd(m, kr("c"))   // opens warning (default cancel)
	m, cmd := upd(m, kEnter) // confirm default <cancel>
	if cmd != nil {
		t.Fatalf("cancelling the mux warning should not invoke mux")
	}
	if m.popup.open() {
		t.Fatalf("popup should close after the choice")
	}
}

func TestMuxDoneReportsStatus(t *testing.T) {
	m := composeSeed(true)
	m, _ = m2tuple(m.onMuxDone(muxDoneMsg{project: "local-dev"}))
	if !strings.Contains(m.status, "cycled") {
		t.Fatalf("successful cycle should report status, got %q", m.status)
	}
	m, _ = m2tuple(m.onMuxDone(muxDoneMsg{project: "local-dev", err: errors.New("not in tmux")}))
	if !strings.Contains(m.status, "not in tmux") {
		t.Fatalf("mux error should be surfaced, got %q", m.status)
	}
}

func TestLayoutSelectorReportsCycleOnly(t *testing.T) {
	m := composeSeed(false)
	m.compose.selected = 0
	m, _ = upd(m, kr("l"))
	if !strings.Contains(m.status, "cycle") {
		t.Fatalf("l should explain that only cycling is available, got %q", m.status)
	}
}
