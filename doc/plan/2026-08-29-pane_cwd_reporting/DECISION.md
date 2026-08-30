# Decisions — pane cwd reporting

## D0 — combine os.Chdir and the WorkingDirectory callback (decided)

Chosen by the user, 2026-08-29 (chat): the feature ships both legs — viewer
`os.Chdir` to the configured `Dir` (moves `#{pane_current_path}`) and monitor
OSC 7 latch + attach-replay re-emit via the vendored vt emulator's existing
`Callbacks.WorkingDirectory` (moves `#{pane_path}`).

Rejected alternatives: tmux `-c`/`StartDirectory` plumbing alone (window-level
only, not per-command); OSC 7 re-emit alone (requires binding changes and a
cooperating child); chdir alone (static only, misses `cd` in shells).

## D1 — consume cwd via callback; no upstream issue (decided 2026-08-29)

User chose: no upstream issue — the callback suffices. No vendored-code change
and no upstream dependency: `observe` wires `Callbacks.WorkingDirectory`,
which upstream already fires
(`internal/third_party/charmbracelet-x-vt/osc.go:122-124`), and cmdman's own
latch is the thread-safe read surface. Supporting fact: the emulator's OSC
metadata family (title, icon name, cwd) is uniformly callback-only — all three
fields are unexported with no getters — so cmdman's title latch already
conforms to the same pattern.

Rejected: courtesy upstream issue (nothing here would call the getter);
blocking on upstream (no technical need — the "requires mutex" premise fails
under upstream's external-synchronization contract, cf. `CursorPosition()`).

## D2 — latch the raw Pt payload; bound it exactly like latchTitle (decided 2026-08-29)

User chose: match `latchTitle` exactly. Confirmed in-repo: latchTitle applies
`sanitizeTermString` (UTF-8 repair, `runtime_state.go:169-171`) and
change-detection, with **no length cap** — so latchCwd does the same and adds
none. The latched value is the raw `file://host/path` payload verbatim
(post-sanitize), re-emitted byte-for-byte; preserves the host field and
matches what the callback delivers.

Rejected: explicit cap (would diverge from the title rule for no present
threat model — revisit for both together if ever needed); parse-and-re-encode
(lossy on host, extra failure mode).

## D3 — proto RuntimeState.cwd in scope; no hook event (decided 2026-08-29)

User chose: add `cwd` to proto `RuntimeState` now (additive field 5, buf
regen); do not add a `HookEventCwd` — the hook stays a two-line future
addition at the `runtime_state.go:188` precedent when a consumer exists.

Sub-decision (confirmed by user 2026-08-29): the proto field carries the
**parsed absolute path** (empty when unset), not the raw `file://` URL —
inspect/status readers want a path, while the raw payload stays internal to
the latch for the byte-exact replay re-emit (D2). Parsing happens once at the
snapshot's `view()` boundary; an unparseable payload yields "" in proto while
still replaying verbatim. Rejected: mirroring the raw URL in proto (pushes
parsing to every reader).

## D4 — frame panes chdir too; logs panes stay out (reopened and re-decided 2026-08-29)

Originally: out of scope for all non-attach panes. Reopened the same day on
new information — `M`/`summonActive`'s `activeMark`
(`cmdman/tui/widget/switcher/switcher.go:727-732`) uses the window ownership
identity first and falls back to `os.Getwd()` only when no identity answered,
so `M` works today, BUT with attach-only chdir the switcher frame pane keeps
reporting `$HOME`, breaking cwd-based tmux bindings whenever that pane is
focused, and the `activeMark` fallback stays dark on identity-probe failure.

Re-decided by user: **frame/switcher panes join Leg A** — chdir to the
project workdir. Logs-mode panes remain out of scope (unchanged half of the
original D4). Workdir acquisition resolved by D6.

## D5 — switcher --mux-token fix is in scope (decided 2026-08-30, "fix token here")

The out-of-scope discovery formerly logged as HANDOFF H1 is promoted into
this plan by explicit user decision (HANDOFF.md deleted — nothing is left
behind). The defect: the switcher frame pane is spawned tokenless
(`cmdman/frame/component.go:29` via `frameComponentArgv`,
`cmdman/mux/frame.go:212,524`), so `probeActiveIdentity`
(`cmdman/cli/tui_backend_mux.go:211+`) falls through to the client-relative
`mux.CurrentWindowID` — the "active" project tracks whichever cmdman-owned
window the user's client was viewing at the last reload, which is what made
`M` manage the wrong project on v0.0.23. Fix: the frame builder passes the
enclosing window's id as `--mux-token <windowID>` (project-manager's
existing flag pattern, `tui_widget_projectmanager.go:76`); the cli plumbing
(`TUIWidgetOptions.MuxToken` → backend) already exists.

## D7 — cwd latch cleared on run reset [automatic]

Decided during implementation (2026-08-30, user away): `reset()` clears
`cwd`/`cwdSet` alongside title/bell, and the no-change guard accounts for a
cwd-only latch. Not listed in the plan step, but required by the struct's
documented "only for the current run" invariant — without it a restarted
command would keep the previous run's cwd. Package-private; no surface delta.

## D8 — service-side RuntimeState carries Cwd too [automatic]

Decided during implementation (2026-08-30, user away): the proto field alone
never reaches inspect/status readers — `cmdman.RuntimeState` (the exported
CLI-output struct in `cmdman/cmdman_runtime_state.go`) and its
`runtimeStateFromProto` mapping drop unknown fields. Added `Cwd string
`json:",omitzero"`` there so `svc.RuntimeStates` / WatchRuntimeState /
--format templates actually see it, matching the docs step's promise of an
inspect/status cwd field. Watch tests updated: the opening snapshot now
carries the seeded configured dir instead of being empty.

## D9 — replay re-emit terminates with ST, not BEL [automatic]

Decided during implementation (2026-08-30, user away): the synthesized OSC 7
in the attach replay ends with `ESC \` instead of `\x07`. The seed puts the
sequence in every replay, and a BEL terminator tripped the bell-block e2e
guarantee (TestHooks_BlockKeepsBellFromViewers scans the viewer stream for
any 0x07): a viewer that blocks bells must not receive a stray BEL from the
monitor itself. The payload still goes out verbatim; only the terminator
differs from the plan's literal, and terminals/tmux accept both.

## D6 — frame workdir via self-resolve, enabled by the token (decided 2026-08-30, "then self-resolve")

Open question 6 resolved: no `--workdir` flag. Once the switcher holds a
reliable token, its identity probe answers from its own window
deterministically; the widget then chdirs when the active group resolves
(identity matched against the project listing's `Workdir`). Rejected: argv
`--workdir` flag — its advantage (probe independence) evaporates with D5,
and it would add CLI surface plus `mux.RunOptions` plumbing for nothing.
Accepted trade-off: cwd is correct only after the first listing+probe load
(milliseconds in practice), and a re-resolve re-chdirs if the active group
changes.
