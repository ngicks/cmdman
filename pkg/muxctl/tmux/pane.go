package tmux

import "github.com/ngicks/cmdman/pkg/muxctl"

// Pane is a runtime pane returned from [Session.ApplyLayout]. It satisfies
// [muxctl.Pane].
type Pane struct {
	id   string
	name string
}

var _ muxctl.Pane = (*Pane)(nil)

// PaneId returns the tmux pane id (e.g. "%42").
func (p *Pane) PaneId() string { return p.id }

// Name returns the pane name (matches the source [muxctl.PaneSpec.Name]).
func (p *Pane) Name() string { return p.name }
