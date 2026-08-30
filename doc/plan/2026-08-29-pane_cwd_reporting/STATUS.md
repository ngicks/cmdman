# Status — pane cwd reporting

Current state: **implementation in progress** (autonomous run, 2026-08-30) —
step 1 landed and committed (967b834). Includes an unplanned but required
reset() clear of the latch (D7 [automatic]).

Worktree: `feat-pane-cwd` (branch `feat-pane-cwd`, based on `main` @ c9d6ff4).

## Checklist (mirrors PLAN.md steps)

- [x] 1. Latch: `latchCwd` + `WorkingDirectory` callback wiring — delivers
      IDEA U2 "the monitor's VT emulator latches it" (967b834)
- [ ] 2. Seed from config `Dir` — delivers IDEA "a freshly started silent
      command still reports its configured Dir"
- [ ] 3. Re-emit in `subscribeOutput` — delivers IDEA U2 "receives a
      synthesized OSC 7 at replay start"
- [ ] 4. hook_filter accounting — delivers PLAN scope "hook-filter
      accounting for the newly captured kind"
- [ ] 5. Proto `RuntimeState.cwd` + buf regen — delivers D3 "add cwd to
      proto RuntimeState now"
- [ ] 6. Viewer chdir (`AttachOptions.WorkDir`) — delivers IDEA U1 "chdir'd
      to the command's Dir at attach start" and U4 best-effort failure
- [ ] 7. Switcher `--mux-token` via frame argv — delivers D5 "the frame
      builder passes the enclosing window's id as --mux-token"
- [ ] 8. Frame chdir on active-group resolve — delivers D4 "frame/switcher
      panes join Leg A" per D6 "chdirs when the active group resolves"
- [ ] 9. E2E coverage — repo rule "implement e2e tests if any existing test
      is not covering the case"
- [ ] 10. Man page + inspect/status + widget-flag docs — checklist docs
      coverage

## Next action

Start step 1 (latch) on the user's go-ahead.
