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

## README does not cover the interaction commands (2026-08-29)

`README.md` never mentions `capture-screen` — nor `send-keys`, `attach`, or
the other interaction verbs; it currently only covers the TUI. The
capture-screen plan's step 9 ("man page under `doc/man`, README mention
beside send-keys", `doc/plan/2026-08-27-capture_screen/PLAN.md:215`) was
checked off in STATUS.md, but the README half never landed because there is
no send-keys mention to sit beside. Noticed during capture-screen QA.

Fix direction: give the README a short CLI-surface overview (or at least an
interaction-commands paragraph linking to `doc/man/`) so future "README
mention" plan steps have a place to land; add `capture-screen` beside
`send-keys` there.

## TestComposeMuxCycleScale_NoWindowError flakes with empty output (2026-08-30)

`TestComposeMuxCycleScale_NoWindowError`
(`e2e/cmdman/mux_cycle_scale_test.go:222`) failed once during a full-suite
run with a non-nil error but empty stdout *and* stderr, then passed on
retry, on a baseline run of the same HEAD, and under a compose-family
stress run. Observed while sweeping the e2e tests onto the Cmd/Session
harness; the composed child environment, timeout, and WaitDelay are
byte-identical before and after the sweep, so the flake predates it.

The empty-output-with-error signature fits exec.Cmd's 3s WaitDelay
aborting the stdout/stderr copy under load — a bound the harness and the
old hand-rolled wiring share.

Fix direction: reproduce under load (e.g. -count with parallel suite
pressure), then either capture the real failure (surface the WaitDelay
abort distinctly from an empty run) or raise/rethink the WaitDelay bound
for invocations that legitimately run long under contention.

## Relative command Dir fabricates an absolute-looking reported cwd (2026-08-30)

`model.ValidateCreate` checks `CommandConfig.Dir` only for non-emptiness,
never `filepath.IsAbs`, and `-w/--workdir` reaches it verbatim. A relative
dir (`rel/sub`) round-trips through the monitor's cwd seed
(`cmdman/monitor/runtime_state.go`, `cwdURL` → `file://localhost/rel/sub`)
and comes back out of `cwdPath` as `/rel/sub` — a fabricated absolute path
that contradicts the proto `RuntimeState.cwd` field's documented "absolute
path" contract. The same input makes the attach viewer's `chdirWorkDir`
resolve against the viewer's cwd rather than the monitor's.

Fix direction: validate `Dir` absolute at create, or absolutize it at
create/seed time.

## Sticky attach's chdir-once guarantee has no behavioral test (2026-08-30)

`cli.AttachSticky` chdirs into `AttachOptions.WorkDir` once, then clears
the field on its value copy so the re-attach loop never repeats it
(`cmdman/cli/sticky.go:79-85`). The guarantee is structural only — a
refactor that stops copying the options by value, or reorders the
clearing, would regress silently into a chdir (and a failure log for a
deleted dir) on every restart cycle.

Fix direction: a test driving two loop iterations that asserts a single
chdir, e.g. via a counting seam like the switcher widget's `chdir` field.

## frameComponentArgv call site untested with a real window id (2026-08-30)

`cmdman/mux/frame.go:212` passes `t.windowID` into `frameComponentArgv`,
which appends `--mux-token <windowID>` to the switcher frame pane's argv.
The unit test covers the function in isolation with a literal token, and
`frame_managed_test.go` hardcodes empty arguments — so passing the wrong
field at the call site (the whole point of the fix: the pane must get the
window it is docked in, not the client-relative one) would pass every
test.

Fix direction: a test through `openFrameTarget`/the frame build path
asserting the resolved window id lands in the component argv.

## Runtime-stream cwd test does not pin immediate delivery (2026-08-30)

`TestStreamRuntimeState_PushesParsedCwd`
(`cmdman/monitor/runtime_stream_test.go`) comments that a cwd change
reaches the watcher at once, but its multi-second receive window cannot
distinguish immediate delivery from the 150ms title throttle. The behavior
is in fact immediate (`titleOnlyChange` compares the whole `runtimeView`,
so a cwd change takes the unthrottled branch); the test just doesn't prove
it.

Fix direction: mirror `TestStreamRuntimeState_ThrottlesTitleBurst`'s
timing assertions.

## Window-level start dirs via multiplexer -c plumbing (2026-08-30)

Viewer panes now report per-command cwd, but windows/panes created by the
mux layer still start in the invoker's directory. `mux.RunOptions` could
gain a work-dir and the tmux driver pass `split-window -c` / session start
directories, fixing dashboard window start dirs independently of
per-command truth. Deliberately left out of the pane-cwd work as
orthogonal.
