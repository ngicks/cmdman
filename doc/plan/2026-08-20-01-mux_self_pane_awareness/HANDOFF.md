# Handoff ledger

## H1 — driver pane classification is floating-pane-blind (out-of-scope discovery, 2026-08-21)

What: tmux 3.7 floating panes (`new-pane`, `pane_floating_flag`) are full
member panes of their window — verified on 3.7b: they appear in plain
`list-panes -t <window>` and count in `window_panes`. The driver classifies
panes only by frame stamp (`listPanesByRole`,
`pkg/muxctl/tmux/frame.go:53-79`): an unstamped floating pane is treated as a
*project* pane, so `resetWindow` (`pkg/muxctl/tmux/apply.go:207-228`) will
kill a bystander floating pane during a rebuild, or — if it sorts first in
`panes.project` — adopt it as the layout anchor, which a floating pane cannot
geometrically serve.

Why not done here: no cmdman surface creates floating panes today; the widget
runs in a `display-popup`, which is *not* a window member and is unaffected.
The hazard becomes real only if popup usage is refactored to floating panes
or users float panes over a dashboard. Under D8 (supervised operation) any
*invoking* context is already safe, floating panes included — the worker
lives outside every pane tree. What is not covered is the bystander/anchor
classification above.

Follow-up: before any popup→floating-pane refactor (or general 3.7 floating
support), teach `listPanesByRole` the `pane_floating_flag` field and decide
floating panes' fate per verb: excluded from anchor adoption and layout
geometry always; absorbed-vs-preserved on reset is a new idea-level question
(D1 was decided for tiled extra panes). Owner: the future floating-pane /
widget-refactor plan.
