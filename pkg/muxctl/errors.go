package muxctl

import "errors"

// Sentinel errors returned by [MuxSpec.Validate] (wrapped with %w plus
// positional context). Drivers may wrap these too or return driver-specific
// errors for runtime failures.
var (
	ErrLayoutNameRequired  = errors.New("muxctl: layout name required")
	ErrDuplicateLayoutName = errors.New("muxctl: duplicate layout name")
	ErrLeafNameRequired    = errors.New("muxctl: leaf pane name required")
	ErrDuplicatePaneName   = errors.New("muxctl: duplicate pane name in layout")
	ErrInvalidDirection    = errors.New("muxctl: dir must be \"h\" or \"v\"")
	ErrSplitsMismatch      = errors.New("muxctl: len(splits) must equal len(panes)")
	ErrLeafXorContainer    = errors.New(
		"muxctl: pane must be a leaf (cmd) or a container (dir+splits+panes), not both/neither",
	)
	ErrMultipleFocus  = errors.New("muxctl: more than one pane has focus: true in layout")
	ErrEmptyContainer = errors.New("muxctl: container pane must have at least one child")
	ErrInvalidSize    = errors.New("muxctl: invalid size")
)
