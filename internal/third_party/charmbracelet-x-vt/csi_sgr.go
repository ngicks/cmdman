package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	xansi "github.com/charmbracelet/x/ansi"

	ansi "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-ansi"
)

// handleSgr handles SGR escape sequences.
// handleSgr handles Select Graphic Rendition (SGR) escape sequences.
func (e *Emulator) handleSgr(params ansi.Params) {
	// uv.ReadStyle takes the upstream x/ansi Params; the vendored parser
	// produces this fork's. Both Param types are int, so convert per element.
	up := make(xansi.Params, len(params))
	for i, p := range params {
		up[i] = xansi.Param(p)
	}
	uv.ReadStyle(up, &e.scr.cur.Pen)
}
