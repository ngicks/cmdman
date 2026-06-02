package cmdman

import (
	"bytes"
	"slices"
	"strconv"
)

// replayableModes is the allowlist of DEC private modes worth replaying on
// attach: input-affecting modes (cursor-key mode, mouse tracking, focus
// reporting, bracketed paste) that a re-attaching multiplexer must observe to
// rebuild its pane flags. Output/screen modes (alternate-screen 47/1047/1049,
// cursor visibility 25, synchronized output 2026, ...) are deliberately
// excluded: re-emitting them after the scrollback could switch buffers or
// clear the screen the client was just sent.
var replayableModes = map[int]bool{
	1:    true, // DECCKM application cursor keys
	9:    true, // X10 mouse
	1000: true, // X11/VT200 mouse
	1001: true, // highlight mouse tracking
	1002: true, // button-event mouse
	1003: true, // any-event mouse
	1004: true, // focus in/out reporting
	1005: true, // UTF-8 mouse encoding
	1006: true, // SGR mouse encoding
	1015: true, // urxvt mouse encoding
	1016: true, // SGR-pixels mouse encoding
	2004: true, // bracketed paste
}

// terminalPaneState tracks terminal modes that multiplexers such as tmux infer
// from pane output rather than from process state. Replaying the active modes
// on attach lets a fresh mux pane rebuild flags such as mouse_any_flag and
// bracketed-paste state even when the enabling escape sequence has scrolled out
// of cmdman's byte scrollback.
type terminalPaneState struct {
	parser terminalStateParser

	decPrivate map[int]bool
	appKeypad  bool
}

func newTerminalPaneState() *terminalPaneState {
	return &terminalPaneState{
		decPrivate: make(map[int]bool),
	}
}

func (s *terminalPaneState) Observe(p []byte) {
	s.parser.observe(p, s)
}

func (s *terminalPaneState) Replay() []byte {
	var modes []int
	for mode, enabled := range s.decPrivate {
		if enabled && replayableModes[mode] {
			modes = append(modes, mode)
		}
	}
	slices.Sort(modes)

	var out []byte
	if len(modes) > 0 {
		var buf bytes.Buffer
		buf.WriteString("\x1b[?")
		for i, mode := range modes {
			if i > 0 {
				buf.WriteByte(';')
			}
			buf.WriteString(strconv.Itoa(mode))
		}
		buf.WriteByte('h')
		out = append(out, buf.Bytes()...)
	}
	if s.appKeypad {
		out = append(out, "\x1b="...)
	}
	return out
}

func (s *terminalPaneState) reset() {
	clear(s.decPrivate)
	s.appKeypad = false
	// Drop any half-parsed escape sequence too. reset() is reused as the
	// per-run hook (Monitor.runOnce) on a long-lived state, so a previous run
	// that ended mid-sequence must not bleed into the next run's parsing.
	s.parser.state = terminalParseNormal
	s.parser.csi = s.parser.csi[:0]
}

type terminalStateParser struct {
	state int
	csi   []byte
}

const (
	terminalParseNormal = iota
	terminalParseEsc
	terminalParseCSI
)

func (p *terminalStateParser) observe(data []byte, s *terminalPaneState) {
	for _, b := range data {
		switch p.state {
		case terminalParseNormal:
			if b == 0x1b {
				p.state = terminalParseEsc
			}
		case terminalParseEsc:
			switch b {
			case '[':
				p.csi = p.csi[:0]
				p.state = terminalParseCSI
			case '=':
				s.appKeypad = true
				p.state = terminalParseNormal
			case '>':
				s.appKeypad = false
				p.state = terminalParseNormal
			case 'c':
				s.reset()
				p.state = terminalParseNormal
			case 0x1b:
				p.state = terminalParseEsc
			default:
				p.state = terminalParseNormal
			}
		case terminalParseCSI:
			p.csi = append(p.csi, b)
			if len(p.csi) > 128 {
				p.state = terminalParseNormal
				p.csi = p.csi[:0]
				continue
			}
			if b >= 0x40 && b <= 0x7e {
				p.applyCSI(s)
				p.state = terminalParseNormal
				p.csi = p.csi[:0]
			}
		}
	}
}

func (p *terminalStateParser) applyCSI(s *terminalPaneState) {
	if len(p.csi) == 0 {
		return
	}
	final := p.csi[len(p.csi)-1]
	params := p.csi[:len(p.csi)-1]
	if final == 'p' && bytes.Equal(params, []byte("!")) {
		s.reset()
		return
	}
	if final != 'h' && final != 'l' {
		return
	}
	if len(params) == 0 || params[0] != '?' {
		return
	}
	enabled := final == 'h'
	for raw := range bytes.SplitSeq(params[1:], []byte{';'}) {
		if len(raw) == 0 {
			continue
		}
		mode, err := strconv.Atoi(string(raw))
		if err != nil {
			continue
		}
		s.decPrivate[mode] = enabled
	}
}
