# Issue backlog

Durable, standalone issues discovered during implementation work and
deferred. Append new entries at the bottom; do not rewrite or reorder
existing ones.

## k8s-file writer can split log entries larger than 4096 bytes (2026-08-22)

`cmdman/logdriver/k8sfile/writer.go` buffers through `bufio.NewWriter`'s
default 4096-byte buffer; an entry larger than that splits into several
`write(2)` calls and can interleave with a concurrent appender to the same
file. Normal-length lines are safe: the whole entry is assembled and flushed
in one write. Today the only shared-file appenders are mux op workers, and
those are serialized per file by the deterministic `muxop-<identity>`
command-name lock, so the hazard is latent.

Fix direction: enlarge or replace the buffering strategy if multi-writer
log files ever become a supported pattern. The fix touches the writer's hot
path around `CurrentOffset`/rotation accounting, which is why it was
deferred.

## StatWindow layout marker breaks on unmarked user-split panes (2026-08-22)

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

## Monitor hard-death paths append no exit event (2026-08-22)

`monitor.MarkMonitorDied` (`cmdman/monitor/mon_clean.go:74-103`) deletes
auto-remove command records without appending any event to the event log,
and `emitEvent` (`cmdman/monitor/mon.go:162`) warns-and-continues when an
append fails. Event-log consumers therefore see such commands vanish
silently. The mux op follower tolerates this via its liveness poll plus a
grace period, but any other consumer relying on a terminal event will hang
or misreport.

Fix direction: append a synthetic failed/exited event in `MarkMonitorDied`
(and consider surfacing repeated `emitEvent` append failures louder).

## Supervised mux-op worker mechanism is undocumented in doc/man (2026-08-22)

The pane-destroying mux verbs (`mux up`, `compose mux up`, `mux down`,
`compose mux down`, `cycle-scale`, `mux frame hide|show|cycle`) now run as
detached supervised worker commands so they survive their own invoking
pane being consumed. None of this is described in `doc/man/cmdman-mux.1.md`
or `doc/man/cmdman-compose-mux.1.md`: the worker model, the
`muxop-<identity>` concurrency lock and its "mux op already running for
this window" error, the `<runtime-dir>/mux/*.log` diagnostic location, and
the exception that a spec piped on stdin runs in-process and therefore
gives up surviving its own pane. Nothing existing is invalidated; the new
behavior is just missing.

Fix direction: a docs pass over both man pages adding the worker model,
the lock/error, the log location, and the stdin exception.

## compose.GenerateName joins name halves ambiguously (2026-08-22)

`compose.GenerateName` (`cmdman/compose/hash.go:33`) escapes each half by
doubling only its own dashes, then joins with a single `-`, so project
`"a-"` + command `"b"` collides with project `"a"` + command `"-b"`. The
identical defect in mux op names was fixed by joining with the unambiguous
`-_` separator (`cmdman/cli/mux_op.go`); `GenerateProjectIdentity` is safe
because its hex workdir hash anchors the separator. Deferred because
`GenerateName` feeds registered command names across the whole compose
layer: changing the encoding renames existing commands.

Fix direction: apply the same unambiguous-join treatment, either with a
migration story for existing registered names or accepting the rename
outright (the app has never been deployed).

## compose-attach man page omits --scale (2026-08-28)

`doc/man/cmdman-compose-attach.1.md` lists the command's options but not
the existing `--scale` flag (`cmd/cmdman/commands/compose_attach.go:41`),
which picks the 1-based replica of a scaled service and is required when a
service has more than one replica. Noticed while documenting
`compose capture-screen`, whose page does document its identical flag.

Fix direction: add the flag to the compose-attach page's options list,
matching the wording used by `cmdman-compose-capture-screen.1.md`.

## e2e TestStatus_WithoutRunningMonitor is not hermetic (2026-08-28)

`e2e/cmdman/status_test.go` (missing-identity case, around line 141)
expects `cmdman status get` with no argument to fail with a
missing-identity error, but `testEnv.execFull`
(`e2e/cmdman/main_test.go:129`) passes `os.Environ()` through to the
binary, so a `CMDMAN_CMD_ID` inherited from a cmdman-supervised shell is
resolved instead and the test fails with a different error. Reproduces
deterministically when the suite itself runs under cmdman.

Fix direction: strip `CMDMAN_CMD_ID` (and any other identity-carrying
variables) from the child environment in `execFull`, or explicitly in that
test.
