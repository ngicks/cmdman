# Decision log

## D16: commit the plan directory before implementation [automatic] (2026-08-21)

Decision: commit the untracked plan directory as a docs commit before
starting implementation, matching repo precedent (main's history commits
plan directories alongside their implementation).

## D17: stdin-provided specs run in-process, not supervised [automatic] (2026-08-21)

Decision: when `mux up`/`mux down` receive the spec on stdin (`path == "-"`,
`cmd/cmdman/commands/zz_mux_helpers.go:34`), the operation runs in-process as
today instead of being handed to the detached worker. A detached worker
re-execs the original argv with /dev/null stdin, so a piped spec would be
lost; forwarding stdin to the worker would add machinery for a rare flow.
The pre-existing self-pane hazard remains for that flow only, documented in
code.

Rejected: stdin forwarding (extra plumbing for a rare flow); hard error
(would break a working outside-pane piped invocation).

## D18: follower output bounded to the current run; log file stays append-only [automatic] (2026-08-21)

Decision: fix the reviewer-confirmed defect (every follow replayed ALL
previous runs' output from the shared per-identity log — a failed run's
stale error reprinted verbatim by the next succeeding run) by making the
follower skip content that predates the current run, NOT by truncating the
file at create time. The shared, append-only per-identity file is the
user's explicit design (one window's history in one place); the follower
just must not re-print history as if it were live output. No time-based
filtering (backwards clock steps burned us once already) — bound by
position/offset.

Rejected: truncate-on-create (destroys the cross-run history the shared
file exists to keep); time-based since filtering (clock-skew fragile).

## D8: approach pivot — supervise the operation instead of spare-then-settle (2026-08-21)

Decision: pane-destroying mux verbs run as a cmdman-supervised operation (the
existing monitor infrastructure): the CLI spawns a detached, monitor-owned
worker that performs the whole operation, and follows its output while the
invoking context survives. The worker's process tree lives under the monitor,
not under any tmux pane, so destroying the invoking pane can never abort the
operation — the same reason the supervised-shell flow always worked.

Rationale (user): mux verbs may be wired to tmux keybindings (`run-shell`),
where no visible pane exists — spare-then-settle's "error prints in the
invoking pane" advantage only covers visible-shell invocations. Failures are
rare and this is a developer tool; restart is acceptable; durable failure
records via the existing log pipeline (`cmdman logs`) suffice. The driver
stays completely untouched — no settle contract, no self-pane identification,
no multi-window/frame-replacement ordering work.

Supersedes: the spare-then-settle direction (PLAN.md approach v1). Q4
(settle API), Q5 (self-pane identification), and Q7 (`Session.Close` scope)
are moot: supervision covers every pane-destroying verb uniformly, including
`Session.Close`, with no per-verb mechanism.

Rejected: spare-then-settle (driver-side complexity; blind spot for paneless
invocation contexts); hybrid supervise+late-kill (most machinery of all).

## D9: failure records are ephemeral — runtime dir preferred (2026-08-21)

Decision: durable-enough failure diagnostics live under the runtime dir
(cleared on reboot), like the event log (`config.EventLogPath`). User choice.
Open tension recorded as open question 9 (D11 stub): reusing `cmdman logs`
verbatim implies the per-command data-dir convention (`config.CommandDir`);
honoring runtime-dir ephemerality may need a log-path override or an
op-scoped record.

## D10: op worker is a registered `--rm` command with a runtime-dir log (2026-08-21)

Decision (user): "registered with --rm command. Specify logging file to run
dir so it won't be deleted on command removal." The op runs as a normal
store-registered command created with `AutoRemove` (existing `--rm`
machinery, `cmd/cmdman/commands/create.go:49,155`); its log driver is pointed
at a runtime-dir file via the existing `LogOptPath` ("path") log-opt, so the
log survives the auto-removal.

Rationale: full reuse — monitor spawn, state store, logs plumbing — with no
lasting `cmdman ls` noise, and the diagnostic record outlives the record of
the command itself.

Rejected: stable per-project registered id (lingers in `ls`); non-registered
bespoke worker (reimplements the plumbing).

## D11: op log file named by the compose identity schema, in the runtime dir (2026-08-21)

Decision (user): the log file lives under the runtime dir and is named with
compose's workdir-hash + project-name schema (`workdirHash` +
`escapeName` / `GenerateName` shape, `cmdman/compose/hash.go`), so the same
project/window always appends to the same file. Interleaving from concurrent
ops is acceptable as long as the logger writes one message atomically (the
k8s-file driver writes line-framed records).

Rejected: data-dir per-command convention (dies with `--rm`); per-invocation
file names (scatters the history of one window).

Amendment (2026-08-21, user): keep the current naming schema, but prefix
`compose-` to compose mux log names to separate the namespaces:
compose → `compose-<wdhash>-<escaped-project>.log`, standalone →
`<escaped-identity>.log` (bare identity; cross-session sharing of the same
identity's file is accepted). Identity components sanitized for filenames.
Rejected: session-qualifying standalone names (the earlier routine call —
more moving parts than the accepted sharing warrants).

## D13: deterministic op command name as the concurrency lock (2026-08-21)

Decision (user): the op command's store name is deterministic per identity —
`muxop-<op-log-name>` (same schema as the D11 log name). A second invocation
against the same identity fails on the name conflict: concurrent ops on one
window are prevented by construction, not raced. The wrapper clears a
dead/failed leftover record (hard-crashed worker) before reporting
"already running".

Rejected: random-suffixed names (allows the concurrent-op race D13 exists to
prevent).

## D14: the launcher widget also goes through the supervised wrapper (2026-08-21)

Decision (user): widget CycleMux uses the same wrapper as the CLI verbs —
one code path is simpler. Explicitly revocable: "invalidate this decision if
it's not simpler" in practice.

## D15: op log capped at 1MiB, 1 file (2026-08-21)

Decision (user): set `max-size=1MiB`, `max-file=1` log-opts on the op
command. Op logs are small; a cap keeps the runtime-dir file bounded either
way.

## D12: the CLI follows via the `cmdman logs -f` equivalent (2026-08-21)

Decision (user): the invoking CLI follows the op with the existing
follow-logs plumbing (monitor `Subscribe` stream), printing lines live, then
exits with the op's exit code. Not a bespoke pipe; not wait-then-dump.

## D1: "wrong pane" invocations are absorbed, never rejected (2026-08-20)

Decision: running a mux verb from a pane inside the target window is always
allowed. A non-managed extra pane is closed as part of layout cycling — "Also
allow non-managed pane -- just close it on layout cycling instead of a hard
error" (user). The consumption happens as the operation's final act, after
every other effect has committed. Invocations from popups / floating panes
(panes not in the target window) proceed untouched, like outside-window runs.

Rationale: the user's historical flow runs `mux up` from inside the dashboard
(the supervised shell pane) and from ad-hoc panes; a hard error would break
the primary gesture. Late consumption preserves the operation's integrity.

Rejected: refuse-with-error (breaks the primary flow); spare-the-pane (leaves
a pane the layout does not describe and skews geometry).

## D2: self-pane awareness covers all pane-destroying verbs (2026-08-20)

Decision: `ApplyLayout` (mux up), `Detach` (mux down), `RespawnLeaf`
(cycle-scale), and `HideFrame` all get the mechanism.

Rationale: same hazard shape in all four; the mechanism is shared, so the
marginal cost is small. Chosen by user over the ApplyLayout-only minimal fix.

## D3: no extra success feedback on a consumed pane (2026-08-20)

Decision: when the final act consumes the invoking pane, the dashboard
appearing (or the window collapsing on `mux down`) is the feedback; no
`display-message` flash. Failures still print normally, since a failed run
never reaches the pane-consuming act.

Rationale: chosen by user; the visual result is unambiguous.

## D4 (moot, superseded by D8): pending-settle contract on muxctl.Session

Was: changed return values on `ApplyLayout`/`Detach` vs. a separate armed
`SettleSelfPane(ctx)` method. No settle mechanism exists under D8.

## D5 (moot, superseded by D8): how the self pane is identified robustly

Was: `$TMUX_PANE` trust vs. PID-ancestry verification. Under D8 the worker
never lives in a pane, so no self-pane identification is needed.

## D6: no automatic viewer restore on supervised-shell failure (2026-08-21)

Decision: when a mux verb entered through the supervised shell fails after
viewers were quiesced, the operation does not restore/reconnect a viewer.
"Leave it as is" (user): the monitored shell survives with the error printed
to it, and the user reads it on manual reattach (`cmdman attach`) or when the
dashboard is rebuilt.

Rationale: the shell and its error always survive (monitor-owned process
tree); the failure path stays simple. IDEA.md's "no silent half-states" is
scoped accordingly: immediately-visible errors are guaranteed for pane-owned
invocations, recoverable errors for supervised-shell invocations.

Rejected: automatic viewer restoration on the failure path (extra recovery
mechanism for a rare path; the tentative default, not taken).

## D7 (moot, superseded by D8): whether Session.Close is in scope

Was: does the settle mechanism cover exported `Session.Close`. Under D8 there
is no per-verb mechanism — supervision covers any driver call the worker
makes, `Session.Close` included, with zero driver changes.
