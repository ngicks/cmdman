# Handoff — deferred items from the implementation run (2026-08-21)

Out-of-scope discoveries awaiting triage; fold into
`doc/plan/issue/issue.md` or drop.

- **`ScaleCell` widens past 99 replicas.** `cmdman/tui/internal/core`'s
  `ScaleCell` renders the replica index with `fmt.Sprintf("%2d", …)` — a
  minimum width — so an index >= 100 takes three cells and breaks the
  fixed two-cell column both row renderers align on. Unreachable at
  realistic replica counts; if three-digit scales ever become real, the
  column width needs a decision (widen, or cap the display).
- **Commands tab dead-row names stay plain.** The switcher renders a dead
  command's name in the subdued weak shade; the Commands tab keeps the
  plain terminal foreground (its historical look — it has no weak color
  plumbed in). Cosmetic asymmetry between the two surfaces; plumbing a
  weak shade into the tab would close it.
