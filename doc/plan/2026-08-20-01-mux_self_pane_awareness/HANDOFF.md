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

## H4 — StatWindow marker breaks on unmarked user-split panes (2026-08-21)

What: `pkg/muxctl/tmux/stat.go:63-80` — one unmarked user-split pane makes
the window's layout marker read `-1`, which breaks `cycle-scale`
("marker -1 out of range [0,1)") and makes a cycling `mux up` re-apply
layout 0. Contradicts `pkg/muxctl/tmux/reuse.go:82`'s stated support for
user splits. e2e worked around it (frame-pane invoker, single-layout spec)
rather than pinning it.

Follow-up: decide whether StatWindow should ignore unmarked panes when
deriving the marker; then pin cycle-scale with a user split in e2e.

## H5 — monitor hard-death paths append no exit event (2026-08-21)

What: `monitor.MarkMonitorDied` (cmdman/monitor/mon_clean.go:74-103)
deletes auto-remove records without appending any event, and `emitEvent`
(mon.go:162) warns-and-continues on append failure. The mux-op follower
covers this via its liveness poll + grace, but any other event-log consumer
sees such commands vanish silently.

Follow-up: consider appending a synthetic failed/exited event in
MarkMonitorDied.

## H6 — follower fallback replay reprints earlier runs' output (2026-08-21)

What: the mux op log file is shared per identity by design; when the op
record is already gone the follower falls back to `replayMuxOpLog`
(cmdman/cli/mux_op_follow.go), which reads from the start of the file and
so reprints previous runs' lines.

Follow-up: remember the log offset at op start (or replay only since the
last run marker) if the reprint proves confusing.

## H7 — worker mechanism undocumented in doc/man (2026-08-21)

What: the supervised-worker execution of mux verbs — including that a spec
piped on stdin runs in-process and so gives up surviving its own pane, and
the `<runtime-dir>/mux/*.log` diagnostic location — is not described in
doc/man (cmdman-mux.1.md, cmdman-compose-mux.1.md). Nothing existing is
invalidated; the new behavior is just undocumented.

Follow-up: a docs pass adding the worker model, the stdin exception, and
where failure logs land.

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
