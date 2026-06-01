package tui

import (
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

func TestStatusGlyphMatchesComposeMarkers(t *testing.T) {
	cases := []struct {
		state   model.EventType
		pending string
		want    string
	}{
		{model.EventTypeCreated, "", "◌"},
		{model.EventTypeStarted, "", "●"},
		{model.EventTypeExited, "", "✔"},
		{model.EventTypeFailed, "", "✘"},
	}
	for _, c := range cases {
		if got := statusGlyph(c.state, c.pending, 0); got != c.want {
			t.Errorf("statusGlyph(%s) = %q, want %q", c.state, got, c.want)
		}
	}
	// Transient persisted state animates with the spinner.
	if got := statusGlyph(model.EventTypeStarting, "", 0); got != spinnerFrames[0] {
		t.Errorf("starting should use the spinner, got %q", got)
	}
	// A pending TUI action overrides the persisted state with the spinner.
	if got := statusGlyph(model.EventTypeExited, "stopping", 3); got != spinnerFrames[3] {
		t.Errorf("pending action should show the spinner frame, got %q", got)
	}
}

func TestCommandRowShowsStatusMarker(t *testing.T) {
	m := seed()
	out := stripANSI(m.renderCommandList("Commands", 44, 12))
	if !strings.Contains(out, "● watcher") {
		t.Fatalf("running command should show the ● marker left of its name:\n%s", out)
	}
	if !strings.Contains(out, "✔ seed-db") {
		t.Fatalf("exited command should show the ✔ marker left of its name:\n%s", out)
	}
}

func TestPressingStartBeginsSpinnerAnimation(t *testing.T) {
	m := seed()
	selectCmd(&m, 2) // seed-db, exited
	if m.anyInProgress() {
		t.Fatalf("precondition: nothing in progress")
	}
	m, cmd := upd(m, kr("s"))
	if !m.spinning {
		t.Fatalf("pressing start should begin the spinner animation")
	}
	if got := m.pendingOf("2"); got != "starting" {
		t.Fatalf("start should set a pending marker, got %q", got)
	}
	foundTick := false
	for _, msg := range drain(cmd) {
		if _, ok := msg.(spinnerTickMsg); ok {
			foundTick = true
		}
	}
	if !foundTick {
		t.Fatalf("a spinner tick should be scheduled while the action is in flight")
	}
}

func TestSpinnerStopsWhenIdle(t *testing.T) {
	m := seed()
	m.spinning = true
	m, cmd := m2tuple(m.onSpinnerTick())
	if m.spinning {
		t.Fatalf("spinner should stop when no command is in progress")
	}
	if cmd != nil {
		t.Fatalf("no further tick should be scheduled when idle")
	}
}

func TestSpinnerKeepsTickingWhileStarting(t *testing.T) {
	m := seed()
	m.commands.groups[0].commands[0].state = model.EventTypeStarting
	if !m.anyInProgress() {
		t.Fatalf("a starting command should count as in progress")
	}
	before := m.spinner
	m2, cmd := m2tuple(m.onSpinnerTick())
	if cmd == nil {
		t.Fatalf("spinner should reschedule while a command is starting")
	}
	if m2.spinner != before+1 {
		t.Fatalf("tick should advance the frame, got %d want %d", m2.spinner, before+1)
	}
}

func TestRefreshWithStartingCommandStartsSpinner(t *testing.T) {
	m := seed()
	// Simulate an external start cascade surfacing a starting command via a
	// debounced re-list.
	infos := []CommandInfo{
		{
			ID:      "1",
			Name:    "watcher",
			Project: "local-dev",
			Workdir: "/work/local-dev",
			State:   model.EventTypeStarting,
		},
	}
	nm, cmd := m.Update(commandsLoadedMsg{infos: infos})
	m = nm.(Model)
	if !m.spinning {
		t.Fatalf("a refresh surfacing a starting command should start the spinner")
	}
	foundTick := false
	for _, msg := range drain(cmd) {
		if _, ok := msg.(spinnerTickMsg); ok {
			foundTick = true
		}
	}
	if !foundTick {
		t.Fatalf("refresh should schedule a spinner tick for the in-progress cascade")
	}
}
