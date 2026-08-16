# Status — tui_live_runtime_state

State: **done 2026-08-16 — steps 1-7 implemented, review gate passed.**

Review gate: full-suite ng-test-runner green (build, all tests incl.
e2e, vet, lint). ng-reviewer found one blocking defect — a self-ended
pump escaping Close()'s cancellation could wedge the TUI on quit —
fixed with two new watcher tests (Close-during-self-ended-parked-push,
reload-churn-holds-one-stream), both mutation-verified. Accepted nits
recorded in L9; reviewer confirmed D20/D22 semantics, L3's TUI-only
scope, and the e2e's criterion-1 proof.

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
      launcher non-case → Non-goals. (HANDOFF.md was created later,
      during step 1, for an out-of-scope discovery — see below.)

## Checklist (mirrors PLAN.md steps)

- [x] 1. `Service.WatchRuntimeState` client + monitor integration test
      (D32: "subscribe → initial snapshot + push on change") —
      `cmdman/cmdman_runtime_state_watch.go` + `_test.go`; L6
      [automatic] records the cancellation-is-clean call; HANDOFF.md
      records a pre-existing monitor race found en route
- [x] 2. `Backend.WatchRuntimeState` contract + cli impl + coretest
      fake (D32: "Consumers: the TUI/switcher ... subscribe to
      streams") — core/backend.go + alias.go + cli adapter
      (park-not-drop pump, deviating from eventStream's drop since
      updates carry rendered state) + coretest FakeRuntimeStateStream
- [x] 3. `core.RuntimeWatcher` reconcile/fan-in + unit tests (L2:
      "streams only carry ... for already-known commands, reconciled
      against each list reload") — core/runtime_watch.go + _test.go;
      full verify matrix incl. failed-subscribe-silent (criterion 5);
      Reconcile returns dropped ids for step 4's cache eviction
- [x] 4. Root TUI wiring + cache; drop TUI one-shot fan-out (L3: "drop
      the one-shot `RuntimeStates` fan-out from the TUI's list path")
      — watcher + runtime cache in Model, RuntimeUpdateMsg arm, quit
      closes watcher, ListCommands passes nil runtime; eviction via
      merge-time sweep (L7 [automatic]); no-flash + ignore-unknown-id
      tests in runtime_test.go
- [x] 5. Switcher wiring; restamp on push (L4: "stamps title-change
      time when the pushed update arrives"); D22 bell suppression holds
      — watcher + cache in switcher Model, stampTitle on push arrival
      replaces load-time stampTitles; bucket-move / bell-read /
      no-flash / unknown-id tests, mutation-checked
- [x] 6. Docs + stale-comment cleanup (tui man page; the
      `tui_backend_commands.go:26` "later phase" note) — man sweep
      found no falsified claims; one liveness sentence added to
      cmdman-tui.1.md (L8 [automatic]); the stale comment fell in
      step 4
- [x] 7. e2e: title change with no lifecycle event reaches the TUI
      (criterion 1) — e2e/cmdman/tui_switcher_live_title_test.go:
      PTY-driven switcher shows the retitle while the eventlog stays
      byte-for-byte unchanged; mutation-checked both halves

## Next action

None — the plan is delivered. HANDOFF.md carries the one item leaving
the plan (pre-existing monitor data race).
