# STATUS — TUI big improvements

**State:** implementation complete — all five feature workstreams (A–E) have
landed and the final Docs/tests step is done. Two live-environment correctness
bugs in the workstream-C vt preview were then found via reproduction and fixed:
**D12** (drain the emulator's unbuffered response pipe — fixes the popup *hang*)
and **D13** (recover emulator panics + fall back to the log view — fixes the
*crash* when previewing codex). DECISION.md holds 13 entries: **D0** (workstream
split), **D1–D8** (the OQ resolutions), **D9** (vt no-remote-resize), **D10**
(tabs single source of truth), **D11** (where the `--tab` helpers live), **D12**
(vt response-pipe drain), **D13** (vt crash-proofing). No open questions remain.

## Live-bug fixes (post-implementation, workstream C)
- **Hang (D12):** the vt emulator answers terminal queries into an unbuffered
  `io.Pipe`; the read-only preview never drained it, so the first query reply
  blocked `term.Write` under the write lock and deadlocked `Render()`. Fixed by a
  discard-drain goroutine; verified `-race`-clean. Regression test
  `TestDrainRawDoesNotDeadlockOnQueryResponses`.
- **Panic (D13):** `vt`/`ultraviolet` panics on some real control sequences
  (scroll region + XTWINOPS + line-insert) a full-screen TUI emits, killing the
  popup. Fixed by `recover()` around all emulator calls + per-session fallback to
  the sanitized log preview. Regression tests `TestDrainRawRecoversEmulatorPanic`,
  `TestRawClosedPanicDisablesTerminalPreviewAndFallsBack`.
- **Clutter (D14):** the PTY was created at 0×0, so the emulator (sized to the
  narrow pane) never matched the command's render width → reflow garble. Fixed by
  the monitor setting a default 80×24 PTY and reporting the live PTY size over a
  new `AttachResponse.resize` field; the TUI sizes the emulator to the reported
  size and crops to the pane (no remote resize, D9). New proto field + monitor
  `PtySize` + `Session.RecvMessage`. Regression tests
  `TestDrainRawResizeSizesEmulatorToPTY`,
  `TestPreviewTerminalEmulatorSizedToPTYNotPane`. **Applies to commands whose
  monitor is the new binary — restart/re-run existing commands to pick it up.**
- **Still-boggy on transition (D15):** the client rebuilds a fresh emulator on
  every attach and reconstructed it from the raw 1 MiB ring, whose oldest bytes
  (alt-screen enter, initial paint, one-time chrome) rotate out for a busy
  full-screen program — so the preview looked right on first view and then broke
  each time the selection moved to another command and re-attached. Fixed by a
  persistent server-side emulator in the monitor (`screenTracker`) that never
  loses screen state to ring rotation and hands attach a coherent current-screen
  **snapshot** in place of the raw scrollback (D9 still holds; no proto/TUI
  change). The tracker contains the D12 pipe-drain and D13 panic-recover so vt can
  never reach the supervisor's critical output path. Regression tests
  `TestScreenTrackerSnapshotReconstructsScrolledOutChrome`,
  `TestScreenTrackerSnapshotMatchesFullReplay`,
  `TestScreenTrackerFeedDoesNotDeadlockOnQuery`. **Monitor-side fix — restart/
  re-run existing commands to pick it up.**

## Checklist (mirrors PLAN.md steps)

- [x] A1 — canonical `tabs.go` table (`Tab`/`tabDefs`/`TabNames`/`TabKeys`/`ParseTab`/`NumTabs`) + `TabLayout` + `Options.InitialTab`
- [x] A2 — `--tab` cobra flag (inline usage from `TabKeys`, `ParseTab`, completion) + forward to popup child
- [x] E1 — popup geometry flags + `PopupConfig` fields + `display-popup` argv
- [x] B1 — `popupComposeUp` + `Backend.ComposeUp` + confirm flow
- [x] B2 — `e` editor handoff (`$VISUAL`/`$EDITOR`/vim) + path resolution
- [x] B3 — `enter` definition viewer overlay + `Backend.ProjectDefinition`
- [x] C0 — thread `Tty` through projection (`CommandInfo.Tty`/`commandRow.tty`) + tests
- [x] C1 — add `charmbracelet/x/vt`; raw read-only preview stream contract
- [x] C2 — vt emulator preview renderer + local resize; predicate + sanitize fallback
- [x] D1 — `Backend.ListLayouts` + `layoutTab` state/render + default-to-marker
- [x] D2 — `enter` applies layout (`Backend.ApplyLayout`) + direct-mode warning
- [x] Docs/tests — help text, e2e, README/architecture note

## Done
- Repo grounding: read the whole TUI package, the popup launcher, the mux
  list/spec/cycle code, compose up, and confirmed `charmbracelet/x/vt` is not yet
  a dependency (sibling of the already-vendored `charmbracelet/x/*`).

## In progress
- None — all steps (A1–D2 + Docs/tests) are implemented.

## Blocked / waiting
- Nothing blocking. (R1 risk for vt is retired by the attach-source decision D7.)

## Next action
- None — implementation complete. The TUI ships three tabs (Commands/Compose/
  Layout), the `--tab` flag, and the `--popup-width/-height/-x/-y` geometry flags;
  README/architecture note and e2e coverage are refreshed.
