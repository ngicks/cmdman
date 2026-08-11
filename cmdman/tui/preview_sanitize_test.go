package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSanitizePreviewLineKeepsSGRAndText(t *testing.T) {
	got := sanitizePreviewLine("plain \x1b[31mred\x1b[0m text")
	want := "plain \x1b[31mred\x1b[0m text" + ansi.ResetStyle
	if got != want {
		t.Fatalf("sanitized line = %q, want %q", got, want)
	}
}

func TestSanitizePreviewLineDropsTerminalStateControls(t *testing.T) {
	in := "start\x1b[2J\x1b[H\x1b[?1049h\x1b]52;c;AAAA\aend\rhidden\b!"
	got := sanitizePreviewLine(in)
	if strings.ContainsAny(got, "\x1b\a\r\b") {
		t.Fatalf("sanitized line still contains unsafe controls: %q", got)
	}
	if got != "startendhidden!" {
		t.Fatalf("sanitized line = %q, want %q", got, "startendhidden!")
	}
}

func TestPreviewLineIsSanitizedBeforeStorage(t *testing.T) {
	m := seed()
	stream := &fakeLogStream{ch: make(chan LogLine, 4)}
	m.commands.preview = previewState{cmdID: "1", status: previewLoading, stream: stream}

	m, _ = m2tuple(m.onPreviewLine(previewLineMsg{
		cmdID: "1",
		line:  "\x1b[31mok\x1b[0m\x1b[2J",
	}))

	if got := m.commands.preview.lines[0]; strings.Contains(got, "\x1b[2J") {
		t.Fatalf("preview stored unsafe control sequence: %q", got)
	}
}
