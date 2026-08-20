# Handoff ledger

## H2 — k8s-file writer can split entries larger than 4096 bytes (2026-08-21)

What: `cmdman/logdriver/k8sfile/writer.go:113` uses `bufio.NewWriter`'s
default 4096-byte buffer; an entry larger than that splits into several
`write(2)` calls and could interleave with a concurrent appender to the same
file. Normal-length lines are one write (assemble + single `Flush()` at
`writer.go:179`).

Why not done here: fixing it means restructuring the writer's hot path
around `CurrentOffset`/rotation — not small or local. In practice one mux op
log file has one writer at a time: same identity is serialized by the
deterministic op-command name, different identities use different files.

Follow-up: enlarge/replace the buffering strategy if multi-writer log files
ever become a supported pattern.

## H3 — mux op name lock created-record TOCTOU (RESOLVED in-run, 2026-08-21)

Resolved during this run: `muxOpRecordIsLive`
(`cmdman/cli/mux_op_supervise.go`) treats pre-`running` records younger
than a 10s grace as live, so a racing invocation reports "already running"
instead of removing the record under a live worker. Residual: the
List→Remove window between two conflicting invocations remains a
theoretical TOCTOU, made practically unreachable by the grace; no further
action planned.

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
