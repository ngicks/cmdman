# Handoff — pane cwd reporting

Deferred tasks and out-of-scope discoveries from the implementation run
(2026-08-30). None started; each entry stands alone.

## H1 — relative configured Dir fabricates an absolute-looking cwd

`model.ValidateCreate` checks `Dir` only for non-emptiness, never
`filepath.IsAbs`, and `-w/--workdir` reaches `CommandConfig.Dir` verbatim.
A relative dir (`rel/sub`) round-trips through the monitor's seed
(`cwdURL` → `file://localhost/rel/sub`) and comes back out of `cwdPath`
as `/rel/sub` — a fabricated absolute path that contradicts the proto
field's "absolute path" contract. The same input makes the attach
viewer's `chdirWorkDir` resolve against the *viewer's* cwd rather than
the monitor's. Options: validate `Dir` absolute at create, or absolutize
at create/seed time.

## H2 — sticky attach's chdir-once has no behavioral test

`cli.AttachSticky` chdirs once then clears `opts.WorkDir` so the
re-attach loop never repeats it (`cmdman/cli/sticky.go:79-85`). The
guarantee is structural only; a refactor that stops copying `opts` by
value or reorders the clearing would regress silently. A test would
drive two loop iterations and assert a single chdir.

## H3 — frameComponentArgv call-site wiring untested with a real window id

`cmdman/mux/frame.go:212` passes `t.windowID` into
`frameComponentArgv`; the unit test covers the function in isolation
with a literal token, and the managed-frame test hardcodes empty
arguments. Passing the wrong field at the call site would pass every
test, and that argument is the entire point of the mux-token fix.

## H4 — runtime-stream cwd test comment overclaims

`TestStreamRuntimeState_PushesParsedCwd`
(`cmdman/monitor/runtime_stream_test.go:110`) says a cwd change reaches
the watcher at once, but its 5s receive window cannot distinguish
immediate delivery from the 150ms title throttle. The behavior is
correct (`titleOnlyChange` compares the whole view, so cwd changes take
the immediate branch); the test just doesn't pin it. Mirror
`TestStreamRuntimeState_ThrottlesTitleBurst`'s timing assertions.

## H5 — compose attach sets no WorkDir

`cmd/cmdman/commands/compose_attach.go:80` builds `AttachOptions`
without `WorkDir`, so compose-attach viewers get the OSC 7 leg but not
the chdir leg. Closing it is the same 5-line Inspect + WorkDir snippet
`attach.go` uses; at that point promoting it to a shared unexported
helper in `zz_helpers.go` is worthwhile. (The TUI's internal attach
also sets no WorkDir, but that one is likely deliberate: chdir is
process-global and would move the whole dashboard.)

## H6 — e2e harness inherits CMDMAN_* env from the invoking shell

`TestStatus_WithoutRunningMonitor` fails whenever the invoking shell
carries `CMDMAN_CMD_ID` (e.g. the test run itself happens inside a
cmdman-supervised session): the spawned binary picks the id up as
identity and the "missing identity" error never fires. The harness
(`e2e/cmdman/main_test.go`) sets its own data/runtime/conf vars but
does not scrub the rest of the `CMDMAN_` namespace. Scrubbing at
`execFull`/env construction would make the suite hermetic.

## H7 — tmux -c / StartDirectory plumbing for window start dirs

Pre-existing candidate from the plan's non-goals: `mux.RunOptions`
gaining a work-dir and the tmux driver passing `split-window -c` /
session start directories would fix window-level start dirs. Orthogonal
to per-command truth; deliberately left out of this plan.
