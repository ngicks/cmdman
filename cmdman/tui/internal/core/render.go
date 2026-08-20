package core

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	ansi "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-ansi"
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

// ClampCells cuts a raw string to at most w cells keeping its head, with a
// trailing ellipsis where it cut. A string that already fits comes back
// untouched, so nothing pays for an ellipsis it does not need. Like
// TruncateLeftCells this counts cells rather than runes, and a wide glyph that
// no longer fits is dropped whole — the result may land a cell short of w
// rather than a cell over it, because a cell over is what overruns the row.
func ClampCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return Cells.Truncate(s, w, "…")
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

// RowNameStyle colors a command row's own name by the state the row is in, so
// the row carries its state in the one word it always shows instead of spending
// a column on a status word next to it. It is the marker palette again — a row
// and its project's dot then say the same thing in the same colors — with two
// shades the dot has no room for: a blocked row is bold because it is the one
// state that wants the user, and a live command that has reported nothing at all
// is faint, which is idle's green minus the claim that something said so.
//
// A row that is not live has no state of its own worth coloring — an exited
// command must never wear its last report — so it drops to the weak shade the
// rest of the subdued rows use.
func RowNameStyle(c CommandRow, weak color.Color) lipgloss.Style {
	if !LiveReport(c) {
		return WeakStyle(weak)
	}
	if c.Status == "" {
		return StyleMarkerIdle.Faint(true)
	}
	if c.Status == StatusWaiting {
		return ReportedStatusStyle(c.Status).Bold(true)
	}
	return ReportedStatusStyle(c.Status)
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

// RowPayload is what a command row puts in its one title slot, and whether that
// text is the command speaking. A live run fills the slot with the title it set
// — the reason the listing shows commands at all. Anything else fills it with
// its lifecycle word instead, which is the truthful signal for a run that is
// over or not yet begun: a dead row shows no report and no title, only where in
// its life it stands.
func RowPayload(c CommandRow) (text string, live bool) {
	if LiveReport(c) {
		return c.Title, true
	}
	if c.Pending != "" {
		return c.Pending + "…", false
	}
	return DisplayLabel(c.State, c.ExitCode), false
}

// ScaleCell is the replica identity column: which of several replicas this row
// is, right-aligned in two cells, or two spaces for a command that is not
// scaled. The column is unconditional so the names that follow it line up
// whether or not a project scales anything — an alignment a bracketed badge
// appended only to replicas cannot give.
//
// The count decides whether there is an index to show — it is what tells a
// scaled command from an unscaled one — but it is not shown: a replica's stored
// count is the desired count as of its own start, which a later `compose scale`
// leaves behind, while its index is what it is for as long as it lives.
func ScaleCell(c CommandRow) string {
	if c.ScaleCount <= 1 || c.ScaleIndex <= 0 {
		return "  "
	}
	return fmt.Sprintf("%2d", c.ScaleIndex)
}

// ScaleBadge is the replica identity a command row carries when the command is
// one replica among several: " [i]", appended after the row's state word by
// every listing that renders commands (D44). It sits next to the state rather
// than at the end of the row because the row's tail is what a narrow pane cuts
// first, and it is styled by its caller in the row's weak shade so a title
// following it stays visibly the command's own words rather than the badge's.
//
// The count still decides whether there is a badge at all — it is what tells a
// scaled command from an unscaled one — but it is not shown: a replica's stored
// count is the desired count as of its own start, which a later `compose scale`
// leaves behind, while its index is what it is for as long as it lives.
//
// An unscaled command — the zero ScaleIndex/ScaleCount pair, which is also how
// a single-instance compose command arrives — has nothing to say and renders
// exactly as it did before there was a badge to append.
func ScaleBadge(c CommandRow) string {
	if c.ScaleCount <= 1 || c.ScaleIndex <= 0 {
		return ""
	}
	return fmt.Sprintf(" [%d]", c.ScaleIndex)
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
