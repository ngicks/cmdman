package cmdman

import (
	"bytes"
	"sort"
	"strconv"
)

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
		if enabled {
			modes = append(modes, mode)
		}
	}
	sort.Ints(modes)

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
