package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// sanitizePreviewLine keeps printable text and SGR styling while dropping
// terminal-state controls such as cursor movement, erase commands, OSC, DCS,
// and alternate-screen toggles. The preview pane is a text viewport, not a
// terminal emulator, so forwarding those controls can corrupt Bubble Tea's
// frame or the user's terminal state.
func sanitizePreviewLine(s string) string {
	var b strings.Builder
	var state byte
	p := ansi.NewParser()
	styled := false

	for len(s) > 0 {
		seq, width, n, newState := ansi.DecodeSequence(s, state, p)
		if n <= 0 {
			break
		}
		state = newState
		s = s[n:]

		if width > 0 {
			b.WriteString(seq)
			continue
		}
		switch seq {
		case "\t":
			b.WriteString("    ")
		default:
			if isSGRSequence(seq, p) {
				b.WriteString(seq)
				styled = true
			}
		}
	}

	if styled {
		b.WriteString(ansi.ResetStyle)
	}
	return b.String()
}

func isSGRSequence(seq string, p *ansi.Parser) bool {
	if len(seq) < 2 {
		return false
	}
	if !strings.HasPrefix(seq, "\x1b[") && seq[0] != ansi.CSI {
		return false
	}
	cmd := ansi.Cmd(p.Command())
	return cmd.Prefix() == 0 && cmd.Intermediate() == 0 && cmd.Final() == 'm'
}
