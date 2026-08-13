package core

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// Cells measures raw glyphs with East-Asian *Ambiguous* characters pinned to
// one cell. That pin is load-bearing, not tidiness: ● and ○ are Ambiguous, so
// under a CJK locale go-runewidth's package default calls them 2 while lipgloss
// (uniseg), which actually draws them, renders 1 — measuring a gap with one
// ruler and drawing the row with the other tears the columns apart. An explicit
// Condition also keeps the measurement independent of the ambient locale.
// Strings that have already been rendered carry ANSI escapes and are measured
// with lipgloss.Width instead, which knows to skip them.
var Cells = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// GlyphWidth measures a raw (unrendered) glyph.
func GlyphWidth(s string) int { return Cells.StringWidth(s) }

// PadCells pads or truncates a raw string to exactly w cells.
func PadCells(s string, w int) string {
	if d := w - Cells.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return Cells.Truncate(s, w, "")
}

// TruncateLeftCells cuts a raw string to at most w cells keeping its tail, with
// a leading ellipsis where it cut. Cells, not runes: a tail of double-width
// glyphs cut by rune count comes back up to twice as wide as the column that
// asked for it, which overruns the row and turns the padding that follows into
// a negative repeat. A wide rune that no longer fits is dropped whole, so the
// result may land a cell short of w rather than a cell over it.
func TruncateLeftCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Cells.StringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	width, i := 0, len(r)
	for i > 0 {
		cw := Cells.RuneWidth(r[i-1])
		if width+cw > w-1 { // one cell is the ellipsis'
			break
		}
		width += cw
		i--
	}
	return "…" + string(r[i:])
}

// The marker glyphs. They are named because their widths feed the layout, and
// those widths are measured rather than assumed — see MarkerSlot. Filled ● is
// a reported state (idle/working/blocked, told apart by color); hollow ○ is
// "nothing reported at all" (D24 amended) — same color, different shape,
// because color alone cannot carry the reported-vs-not distinction.
const (
	GlyphBell   = "🔔"
	GlyphFilled = "●"
	GlyphHollow = "○"
)

// The reported-status vocabulary (D12) as the backend-neutral CommandInfo
// spells it. They are the rendering of the wire enum, mirrored here rather than
// imported so the model stays exercisable without the service packages.
const (
	StatusWorking = "working"
	StatusWaiting = "waiting"
	StatusDone    = "done"
)

// MarkerSlot is where a group head's name starts, measured from the marker: the
// widest marker (the bell) plus one space. The width is taken from the glyph
// rather than written down, so a terminal that measures the bell differently
// moves the whole column instead of tearing it — and so the slot does not shift
// when runtime state starts putting a bell in it.
var MarkerSlot = GlyphWidth(GlyphBell) + 1

// MarkerMargin is the one space to the left of the marker.
const MarkerMargin = " "

// colorWeakBlock is the second cursor's block: dark enough to read as "the
// cursor is here too" without competing with the focused pane's indigo.
var colorWeakBlock = lipgloss.Color("237")

// RowBg is the background a rendered row carries. A selected switcher group is
// one solid block spanning its head and its command rows, so every piece of
// every line in it renders with the background rather than one outer style
// being laid over pre-colored text. BgWeak is the launcher's second cursor: a
// two-pane view shows where both panes stand, so the pane without the keyboard
// keeps a fainter block (D31).
type RowBg int

const (
	BgNone RowBg = iota
	BgWeak
	BgAccent
)

func (b RowBg) Style(st lipgloss.Style) lipgloss.Style {
	switch b {
	case BgAccent:
		return st.Background(ColorBorder)
	case BgWeak:
		return st.Background(colorWeakBlock)
	}
	return st
}

// Plain renders s carrying only the row background.
func (b RowBg) Plain(s string) string {
	return b.Style(lipgloss.NewStyle()).Render(s)
}

var (
	StyleWidgetTitle = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	StyleWidgetHead  = lipgloss.NewStyle().Bold(true)
	StyleWidgetBar   = lipgloss.NewStyle().Foreground(ColorOnAcc)
	// The traffic-light marker palette (D21): green nothing wants you, yellow
	// something is working, red something is blocked on you. The status words in
	// command rows share it, so a row and its project's dot say the same thing
	// twice rather than in two vocabularies. They are basic ANSI colors like the
	// rest of this TUI's markers (theme.go's StyleMark*), so they follow the
	// user's own theme.
	StyleMarkerIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	StyleMarkerWork    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	StyleMarkerBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// ReportedStatusStyle colors a reported status word or the dot standing for it.
// Anything unreported keeps idle's green: "nothing wants you" is what both
// mean, and the glyph — hollow rather than filled — carries the difference.
func ReportedStatusStyle(status string) lipgloss.Style {
	switch status {
	case StatusWaiting:
		return StyleMarkerBlocked
	case StatusWorking:
		return StyleMarkerWork
	default:
		return StyleMarkerIdle
	}
}

// ReportedStatusBadge renders a command's reported status: the word when it
// reported one, else the hollow circle standing for "nothing said so far", so
// every command carries a circle-or-word state instead of a blank (D24).
func ReportedStatusBadge(status string, bg RowBg) string {
	if status == "" {
		return bg.Style(StyleMarkerIdle).Render(GlyphHollow)
	}
	return bg.Style(ReportedStatusStyle(status)).Render(status)
}

// RowStateBadge is the one state word a command row carries. A live run shows
// what it reported about itself; anything else shows its lifecycle state, which
// is the truthful signal for a run that is over or not yet begun — an exited
// command must never show its last report (D13).
func RowStateBadge(c CommandRow, bg RowBg) string {
	if LiveReport(c) {
		return ReportedStatusBadge(c.Status, bg)
	}
	label := DisplayLabel(c.State, c.ExitCode)
	if c.Pending != "" {
		label = c.Pending + "…"
	}
	return bg.Style(StatusStyle(c.State, c.Pending)).Render(label)
}

// PadLine truncates and right-pads a rendered line to exactly w cells, so a
// highlighted group forms a solid block and no line overflows its pane.
func PadLine(line string, w int, bg RowBg) string {
	line = ansi.Truncate(line, w, "")
	if pad := w - lipgloss.Width(line); pad > 0 {
		line += bg.Plain(strings.Repeat(" ", pad))
	}
	return line
}

// StripANSI drops SGR escape sequences so a rendered line can be measured or
// searched as plain text. It handles exactly the escapes this TUI emits — CSI
// sequences ending in 'm' — rather than the whole ANSI grammar.
func StripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// WeakRatio is how far the app rows travel from the letter color toward the
// background. Much past this they stop being readable on low-contrast themes.
const WeakRatio = 0.55

// DeriveWeak recomputes the weak shade from whatever the terminal has reported
// so far, so the two answers may arrive in either order (D26). termFg is the
// terminal's letter color and termBg its background, either of which may be nil
// until the terminal answers — and some terminals never do. It returns nil while
// the letter color is unknown, which is the caller's cue to keep its fallback.
func DeriveWeak(termFg, termBg color.Color, fgDark bool) color.Color {
	if termFg == nil {
		return nil
	}
	bg := termBg
	if bg == nil {
		// Background unanswered: assume the end opposite the letters, so the
		// blend still moves away from them rather than into them.
		bg = color.Black
		if fgDark {
			bg = color.White
		}
	}
	return Blend(termFg, bg, WeakRatio)
}

// WeakStyle is the subdued rows' foreground: the terminal's own letter color
// pulled toward its background, so a group reads as bright head plus subdued
// detail on light and dark terminals alike. Faint is the fallback for terminals
// that never answer the query.
func WeakStyle(weak color.Color) lipgloss.Style {
	if weak == nil {
		return StyleActive
	}
	return lipgloss.NewStyle().Foreground(weak)
}

// Blend mixes a toward b along a straight line in RGB. RGBA reports 16-bit
// alpha-premultiplied channels and every color here is opaque, so scaling to
// 8 bits is both correct and enough to keep this dependency-free.
func Blend(a, b color.Color, t float64) color.RGBA {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8(min(math.Round((float64(x)*(1-t)+float64(y)*t)/257), 255))
	}
	return color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}
}
