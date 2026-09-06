---
tags: muxctl tmux stat cycle-scale bug
---

# StatWindow layout marker breaks on unmarked user-split panes

Recorded 2026-08-22 in the legacy backlog `issue.md`; migrated 2026-09-03.

`pkg/muxctl/tmux/stat.go:63-80`: one unmarked user-created split pane in a
managed window makes the window's layout marker read `-1`. Consequences:
`cycle-scale` fails with "marker -1 out of range [0,1)", and a cycling
`mux up` re-applies layout 0 instead of advancing. This contradicts
`pkg/muxctl/tmux/reuse.go:82`, which states user splits are supported.
Observed while writing the supervised-mux-op e2e suite; the tests work
around it (frame-pane invoker, single-layout spec) instead of pinning it.

Fix direction: decide whether StatWindow should ignore unmarked panes when
deriving the marker, then add e2e coverage for cycle-scale with a user
split present.
