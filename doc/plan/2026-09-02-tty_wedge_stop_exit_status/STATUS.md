# Status — TTY monitor wedge, survivor sweep, exit status

State: **plan finalized 2026-09-03 — not started.** Steps 1–3 can start
now; step 0 (the prerequisite plan) precedes every monitor step (4–7).

## Checklist (mirrors PLAN.md steps)

- [ ] 0. Prerequisite: `2026-08-16-02-monitor_proc_handle_race` steps 1–4
      implemented — D7 "prerequisite plan lands first"

- [ ] 1. `reportTargetErrors` + `--ignore-errors` — D2 "attempt every
      target, print one line per failure, exit 1 … unless the opt-out
      flag", D5 "`--ignore-errors`"
- [ ] 2. Man `## Exit status` + option bullet ×4 — IDEA "the `stop` man
      page states the exit status contract"
- [ ] 3. compose `firstStopErr` at three sites — D3 "surfaces the first
      per-target error as the outcome error"
- [ ] 4. Detach from the pty reader — D6 "never joins the goroutine
      parked in `ptmx.Read`", D9 "`readerDrainWait` (1 s)"; IDEA U1 step 2;
      D10 "`reader_detached`" attr + `Warnings`
- [ ] 5. `Setsid` for the supervised child, hooks keep `Setpgid` — D8
- [ ] 6. Subreaper + sweep — D1 "terminates and reaps every process the
      run left behind", D4 "reaps … `SIGTERM` … `orphanGrace` (2 s) …
      `SIGKILL`s the rest, and reaps until nothing remains"; D10
      "`survivors_unreaped`" attr
- [ ] 7. pgid fallback — D7 "`runPgid` under its `procMu`"; IDEA U3 step
      2 "even in the window after the child has been reaped"
- [ ] 8. e2e U1, U2/U5, U4 (via `rm`), U6
- [ ] 9. Close both issue items in `doc/plan/issue/`

## Next action

Start step 1 (`reportTargetErrors` + `--ignore-errors`) on a feature
branch; run step 0 (the prerequisite plan) before step 7.
