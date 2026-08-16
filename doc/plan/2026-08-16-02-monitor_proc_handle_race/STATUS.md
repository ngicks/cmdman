# Status — monitor_proc_handle_race

State: **not started — plan finalized 2026-08-16 (idea phase skipped
per R1; no open questions).**

## Checklist (mirrors PLAN.md steps)

- [ ] 1. Guard the handles under `procMu` (HANDOFF: "a
      `stdinMu`-guarded accessor for `m.cmd`/`m.stdin` (or
      equivalent)"; R2 extends to `ptmx`; R3 fixes the shape)
- [ ] 2. Regression test combining `Status`/`WriteStdin`/`Signal` with
      a run ending under `-race` (HANDOFF: "a regression test
      combining an RPC with a run ending"), mutation-checked
- [ ] 3. Lift the workaround comment in
      `cmdman/cmdman_runtime_state_watch_test.go:47-52` (criterion 3)
- [ ] 4. Close out: mark parent HANDOFF entry picked up; full suite +
      lint

## Next action

Start step 1.
