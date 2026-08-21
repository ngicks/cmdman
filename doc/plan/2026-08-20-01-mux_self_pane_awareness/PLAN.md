# Mux self-pane awareness

Make every pane-destroying mux operation safe to invoke from any context —
inside its own target window, a popup, or a paneless tmux keybinding — by
running the operation as a cmdman-supervised worker whose process tree no
pane kill can reach (D8).

> Skeleton until the IDEA.md gate passes (reset by the D8 pivot). Goal/scope
> derive from IDEA.md; contracts and step detail are filled in after the gate.

## Goal / success criteria

- All IDEA.md inside-invocation cases (UC1–UC6) complete their full effect
  when invoked from inside the target window or from a paneless context; UC7
  flows keep synchronous output and a real exit code. Per D1, no invocation
  context is itself a hard error.
- Concretely, the repro matrix in RESEARCH.md becomes all-✅: takeover run
  ends with `framedef=default` stamped and viewers running; inside-dashboard
  run ends with the layout rebuilt, not a dead-pane graveyard.
- A failed operation is recorded durably (D9) and never leaves
  `remain-on-exit on` behind; the CLI prints the failure when its context
  survives.

## Scope

- Every pane-destroying mux verb: `mux up` / `compose mux up` / widget
  CycleMux, `mux down`, `cycle-scale`, `mux frame hide|show|cycle`. One
  mechanism — supervised execution — covers them uniformly (D8 makes the D2
  four-verb enumeration and the `Session.Close` boundary moot).
- The cmdman layer only: spawning the op worker via the existing monitor
  infrastructure, following its output/exit, durable failure records (D9),
  and env propagation ($TMUX etc.) into the worker.
- `pkg/muxctl` is out of scope by design: no driver changes at all.

## Non-goals

- Cwd-scoped identity for work_dir-less projects is intended behavior
  (compose is a template; each cwd is its own instance — user clarified,
  RESEARCH.md last section). Nothing to fix, here or elsewhere.
- Any change to the takeover/reuse *decision* rules
  (`pkg/muxctl/tmux/reuse.go`); which window is targeted stays as is.
- New CLI surface. No new flags or verbs (a hidden internal subcommand for
  the worker, like `__monitor`, is expected and is not user surface).
- tmux 3.7 floating-pane awareness in the driver's pane classification
  (bystander floating panes killed by reset / adoptable as anchor) — recorded
  as HANDOFF.md H1. Note D8 already makes any *invoking* context safe,
  floating panes included.

## Context (current behavior)

See RESEARCH.md for the verified failure mechanism. Key locations:

- `cmdman/mux/run.go:134-266` — `Run` orchestrates pick-window → `ApplyLayout`
  → `showDefaultFrame` → attach hint; dies mid-way today when its pane is
  consumed. Same shape: `down.go:137-171`, `cycle_scale.go:104-126`,
  `frame.go:225-247`.
- `cmdman/monitor/mon_spawn.go`, `mon_spawn_posix.go` — the existing
  double-fork spawn: re-exec `os.Executable()` with a hidden subcommand,
  forwarding `--data-dir`/`--runtime-dir`/`--config`. The template for
  spawning the op worker.
- `cmdman/config/config.go:147-155` — `EventLogPath`: precedent for
  ephemeral runtime-dir records (D9). `CommandDir` (:166-174) is the
  data-dir per-command convention `cmdman logs` reads from.
- `cmdman/mux/run.go:89-91` — `RunOptions.Env`: the invoker's environment
  ($TMUX, socket path) that must reach the worker for the driver to connect
  to the right server. `CycleScaleOptions` lacks this seam
  (`cycle_scale.go:14-31, 89`).

## Approach (sketch — detail after the gate)

Chosen direction (D8): **supervised operation + follower CLI**.

1. A mux verb's CLI invocation spawns a detached, monitor-owned worker
   (reusing the `SpawnMonitor` double-fork shape) that executes the entire
   operation — all windows, frame phases, durable state, restores.
2. The CLI follows the worker: streams its output and exits with its exit
   code. If the CLI's pane is consumed mid-operation, only the follower dies;
   the worker is unaffected.
3. The worker's output/failure is recorded durably (D9: runtime-dir
   preference; exact mechanism = open question 9) so a paneless or consumed
   invocation can be diagnosed afterwards.
4. The worker receives the invoker's relevant environment ($TMUX, $TMUX_PANE
   irrelevant now but the server socket matters) via its command config.

Alternatives rejected (see D8): spare-then-settle (driver-side deferral —
complex, and blind to paneless invocation contexts); hybrid supervise +
late-kill (most machinery).

## Public surface delta

No user-facing CLI or config-file surface changes; no `pkg/muxctl` changes.
Everything below is internal or hidden:

```go
// No new subcommand. The op command re-execs the ORIGINAL argv with an
// internal marker env set (same pattern as __CMDMAN_INTERNAL_MONITOR_DAEMON):
//   __CMDMAN_INTERNAL_MUXOP=1 cmdman <original mux verb + args...>
// Marker set → the verb runs in-process (it IS the worker), rebuilding
// spec/options/Svc exactly like a direct run. Marker unset → the verb
// spawns the op command and follows it. RunOptions carries live state
// (Svc FrameSvc, Stdout io.Writer — cmdman/mux/run.go:83-94), so an
// options-serialization request file is not viable.

// cmdman/mux — CycleScaleOptions gains the Env seam the other verbs have:
type CycleScaleOptions struct {
    // ...existing fields...
    Env []string // invoker environment; parity with RunOptions.Env,
                 // resolveServer stops reading os.Environ() directly
}

// The op command registered per invocation (not new surface — existing
// model.CommandConfig fields):
//   Name:          "muxop-<op-log-name>"  // deterministic = concurrency lock (D13)
//   AutoRemove:    true                                  (D10)
//   RestartPolicy: "no"   // a failed apply must never be respawned in a loop
//   LogDriver:     k8s-file
//   LogOpts:       {"path": "<runtime-dir>/mux/<op-log-name>.log",   (D11)
//                   "max-size": "1MiB", "max-file": "1"}             (D15)
//                  // op-log-name: compose-<wdhash>-<escaped-project> | <escaped-identity>
//   Env:           invoker's $TMUX (+ tmux socket context)
```

Durable state vocabulary: the op log file
`<runtime-dir>/mux/<op-log-name>.log`. Compose projects:
`compose-<workdir-hash>-<escaped-project>.log` — the identity schema
(`cmdman/compose/hash.go`) with a `compose-` prefix separating the
namespace. Standalone mux: `<escaped-identity>.log` — the bare identity
(default: window name, `deriveIdentity`, `cmdman/mux/run.go:332-341`);
sharing one file across sessions with the same identity is accepted (same
identity → same file). Identity components are sanitized for filenames
(window names are arbitrary strings).

## Implementation steps

1. **Worker entrypoint.** Marker-env re-exec (see Public surface delta):
   each mux verb's CLI path checks `__CMDMAN_INTERNAL_MUXOP`; set → run the
   operation in-process as today (the worker path), unset → hand off to the
   spawn+follow wrapper (step 3). Options serialization was rejected:
   `RunOptions` carries `Svc`/`Stdout` live state
   (`cmdman/mux/run.go:83-94`). Spec/compose files are re-read by the
   worker — same freshness semantics as a direct run. Verifiable alone:
   invoke a verb with the marker env set.
2. **Env seam.** Add `CycleScaleOptions.Env`
   (`cmdman/mux/cycle_scale.go:14-31`) and route `resolveServer` through it
   (:89), removing the direct `os.Environ()` read. Verifiable with existing
   cycle-scale unit tests plus a stale-env case.
3. **Spawn + follow wrapper.** In the service layer: register the op command
   under its deterministic name (D13; on a name conflict, remove a
   dead/failed leftover and retry once, else fail with "mux op already
   running") with `AutoRemove`, k8s-file driver, `LogOptPath` + 1MiB/1-file
   caps (D11/D15), invoker env,
   start it (existing `SpawnMonitor` path), follow via the `Logs`
   follow plumbing (`cmdman/cmdman_logs.go:46` `Service.Logs` sticky
   streaming — the `logs -f` equivalent, D12), then surface the op's exit
   code. Exit-code capture is unconditional (UC7 requires a real rc): read
   the final exit event/state before `AutoRemove` erases the record.
   Create `<runtime-dir>/mux/` on demand. Verify at implementation that the
   k8s-file writer appends (not truncates) when `LogOptPath` names an
   existing file, and that writes are atomic per line (D11's concurrency
   acceptance rests on both).
4. **Wire the verbs.** `mux up` / `compose mux up` / `mux down` /
   `cycle-scale` / `mux frame hide|show|cycle` CLI paths call the wrapper
   instead of running the operation in-process. Widget `CycleMux` goes
   through the wrapper too (D14: one path is simpler; revisit if it proves
   otherwise).
5. **Tests.**
   - e2e inside-a-pane harness (send-keys-driven invocation): RESEARCH.md
     matrix rows 3/4 become all-✅ (`framedef` stamped, viewers alive,
     marker set, `remain-on-exit off`).
   - e2e paneless invocation (`run-shell`-style) completes.
   - e2e multi-window `Down`/`CycleScale` from a pane in the first matching
     window: later windows, per-window results, scale state complete.
   - e2e frame replacement from a frame pane: both phases complete.
   - Failure injection: op log file exists under `<runtime-dir>/mux/` with
     the error; `remain-on-exit` restored; surviving-context CLI prints the
     error and exits non-zero.
   - UC7 regression: outside-tmux / different-window runs keep synchronous
     output and real exit codes.

## Testing and verification

- e2e: reproduce the RESEARCH.md matrix rows 3 and 4 and assert the
  all-effects-committed end state (`framedef` stamped, viewers alive,
  marker set, `remain-on-exit off`).
- e2e: `run-shell`/keybinding-style paneless invocation completes.
- e2e/integration: multi-window Down and CycleScale from a pane in the first
  matching window: later windows, per-window results, and scale state all
  complete.
- Failure injection: worker failure leaves a durable record (D9) and restores
  `remain-on-exit`; surviving-context invocation prints the error and rc.
- UC7 regression: outside-tmux and different-window runs keep synchronous
  output and exit codes.
- Manual: the original two-command repro from a plain tmux shell.

## Risks

- Worker environment fidelity: the driver resolves the tmux server from env;
  the worker must see the invoker's $TMUX/socket or it will drive the wrong
  (or no) server.
- Follower lifetime: the CLI dying mid-stream must be harmless to the worker
  (no pipe-driven SIGPIPE kill, no blocking writes).
- Op lifecycle hygiene: concurrent invocations, leftover op records after
  success, and stale op state on reboot (runtime dir clears — D9 precedent).
- Exit-code loss in consumed contexts is by design (D3/D6) but must not leak
  into UC7 contexts.
- `AutoRemove` (D10) races the follower's exit-code read: the op record may
  be gone by the time the stream ends. Step 3 must close this race (capture
  the rc from the exit event/state before removal); UC7 requires a real
  exit code, so if the race cannot be closed, reopen with the user rather
  than degrade to "rc unknown".
- Concurrent invocations against the same identity are serialized by the
  deterministic op name (D13): the second fails on the name conflict. The
  wrapper must distinguish "running" (report already-running) from a
  dead/failed leftover after a worker hard-crash (remove and retry once) —
  getting that wrong either blocks the identity forever or kills a live op's
  record.

## Open questions

None. Resolved history: Q1 → D1, Q2 → D2 (enumeration made moot by D8),
Q3 → D3, Q6 → D6, approach pivot → D8, ephemerality → D9,
Q8 → D10 (registered `--rm` op, runtime-dir log), Q9 → D11 (compose
identity-schema log name in runtime dir), Q10 → D12 (`logs -f` follow).
Q4/Q5/Q7 are moot under D8.
