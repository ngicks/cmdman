# Status — tui_live_runtime_state

State: **implementing — step 1 done, step 2 in progress.**

## Gate

- [x] L1 (launcher out of scope — supersedes same-day "markers only"),
      L2 (layer on eventlog) resolved with the user; L3 (drop TUI
      one-shot fan-out), L4 (stamp on push) decided as routine agent
      calls — all in DECISION.md.
- [x] Idea gate: IDEA.md confirmed by the user 2026-08-16 (launcher
      non-case folded in after their L1 revision).
- [x] Contract round: Public surface delta pinned; implementation
      steps written.
- [x] Traceability gate: L1 → Non-goals + criterion 6; L2 → steps 3–5;
      L3 → step 4; L4 → step 5; inherited D32 consumer clause → steps
      1–5 with the launcher deviation recorded beside the quote;
      D20/D22 → step 5 tests; D35 → constraint only. IDEA use cases
      1–3 → steps 1–5 + e2e step 7; use case 4 → L2/steps 3–4;
      launcher non-case → Non-goals. No HANDOFF.md — nothing left
      behind.

## Checklist (mirrors PLAN.md steps)

- [x] 1. `Service.WatchRuntimeState` client + monitor integration test
      (D32: "subscribe → initial snapshot + push on change") —
      `cmdman/cmdman_runtime_state_watch.go` + `_test.go`; L6
      [automatic] records the cancellation-is-clean call; HANDOFF.md
      records a pre-existing monitor race found en route
- [ ] 2. `Backend.WatchRuntimeState` contract + cli impl + coretest
      fake (D32: "Consumers: the TUI/switcher ... subscribe to
      streams")
- [ ] 3. `core.RuntimeWatcher` reconcile/fan-in + unit tests (L2:
      "streams only carry ... for already-known commands, reconciled
      against each list reload")
- [ ] 4. Root TUI wiring + cache; drop TUI one-shot fan-out (L3: "drop
      the one-shot `RuntimeStates` fan-out from the TUI's list path")
- [ ] 5. Switcher wiring; restamp on push (L4: "stamps title-change
      time when the pushed update arrives"); D22 bell suppression holds
- [ ] 6. Docs + stale-comment cleanup (tui man page; the
      `tui_backend_commands.go:26` "later phase" note)
- [ ] 7. e2e: title change with no lifecycle event reaches the TUI
      (criterion 1)

## Next action

Start step 1 (`cmdman/cmdman_runtime_state_watch.go`).
