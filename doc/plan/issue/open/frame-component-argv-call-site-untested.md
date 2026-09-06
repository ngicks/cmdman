---
tags: mux frame test
---

# frameComponentArgv call site untested with a real window id

Recorded 2026-08-30 in the legacy backlog `issue.md`; migrated 2026-09-03.

`cmdman/mux/frame.go:212` passes `t.windowID` into `frameComponentArgv`,
which appends `--mux-token <windowID>` to the switcher frame pane's argv.
The unit test covers the function in isolation with a literal token, and
`frame_managed_test.go` hardcodes empty arguments — so passing the wrong
field at the call site (the whole point of the fix: the pane must get the
window it is docked in, not the client-relative one) would pass every
test.

Fix direction: a test through `openFrameTarget`/the frame build path
asserting the resolved window id lands in the component argv.
