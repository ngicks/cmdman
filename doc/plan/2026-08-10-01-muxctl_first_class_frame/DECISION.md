# Decision log

One entry per material decision: the choice, the rationale, the rejected
alternatives. Entries are numbered **F1, F2, …** (frame-driver) so they
never collide with the parent plan's D-numbers, which this file cites.

Nothing local is decided yet — every stub below is open. The inherited
section records decisions this plan is **downstream of**; they are context,
not this plan's to revisit.

## Inherited from the parent plan (context, not re-decided)

See
[`../2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md`](../2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md).

- **D1** — B's muxctl contract revision is a separate plan: subtree-scoped
  apply, identity coexistence, and `resetWindow`/`Detach` sparing frame
  panes get their own plan directory in the muxctl plan series, because
  they revise muxctl's documented one-window-one-owner contract
  (`pkg/muxctl/session.go:7`). **This directory is that plan.**
- **D36** — straight to the first-class frame: no compile-in spike (frame
  panes would die on every layout cycle, so it could not demonstrate D15's
  lifecycle). The parent's phase 3 blocks on this plan from its step 15 on.
- **D15** — the frame is a standalone feature: named defs under
  `<config-dir>/frame/<name>.yaml`, explicitly shown / hidden / selected /
  cycled. That lifecycle is what this plan's goal criteria must make
  physically possible.
- **D6** — switching navigates per-project windows; there is no single
  dashboard window whose main region re-renders. So a frame surrounds a
  *project window*, and both identities live on that one window.
- **D16** — no default frame; `switcher` and `statusbar` are built-in
  components. Below the driver they are argv like any other pane
  (`pkg/cmdman/frame/carve.go:15`, `:87-105`).
- **D19** — frame `command:` entries are ephemeral by default,
  `managed: true` opts into cmdman supervision. Whether the driver must
  treat the two differently is this plan's Q7.
- **D30** — `N%` resolves against the rectangle remaining at the entry's
  turn; carving already delivers this through `muxctl.ComputeChildCells`
  (`pkg/muxctl/layout.go:8-60`). No driver work.
- **D37** — `component: <name>` resolves to `cmdman tui widget <name>`,
  supplied through `frame.ComponentArgv` (`pkg/cmdman/frame/carve.go:15`).
  The driver never sees component names.

## Stubs — open questions (PLAN.md "Open questions")

Each resolves into an F-entry below; tentative defaults live in PLAN.md and
are explicitly not decisions.

- [ ] Q1 API shape: additive anchored-apply sibling vs revised
      `ApplyLayout` semantics (`pkg/muxctl/session.go:21`) → F?
- [ ] Q2 second identity's home and how `ListWindows` exposes/filters it
      (`pkg/muxctl/tmux/list.go:97-100`, `pkg/muxctl/driver.go:38-59`) → F?
- [ ] Q3 which side owns `@cmdman_window` (`pkg/muxctl/tmux/tmux.go:19`) → F?
- [ ] Q4 marker semantics on a framed window
      (`pkg/muxctl/tmux/stat.go:50-57`, `pkg/cmdman/mux/run.go:151-161`) → F?
- [ ] Q5 focus policy owner (`pkg/muxctl/layout.go:104-108`,
      `pkg/muxctl/tmux/apply.go:62-69`) → F?
- [ ] Q6 main region before any project exists
      (`pkg/cmdman/frame/carve.go:41`, `pkg/muxctl/spec.go:140-142`) → F?
- [ ] Q7 frame pane lifecycle on hide/cycle, managed vs ephemeral (D19) → F?
- [ ] Q8 teardown when neither side is left
      (`pkg/muxctl/tmux/detach.go:12-47`) → F?
- [ ] Q9 driver-neutral contract vs tmux-scoped
      (`pkg/muxctl/tmux/scale_state.go:32-34`) → F?
- [ ] Q10 pane-name namespace (`pkg/muxctl/validate.go:23-29`,
      `pkg/cmdman/frame/carve.go:22-25`) → F?

## Decided

*(empty — this plan is a draft; no local decision has been made.)*
