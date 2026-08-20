# Decisions — switcher command row restyle + wheel scroll

Inherited context (quoted, not re-summarized):

- D13 (upstream plans, as `core/render.go:165` states it): "an exited command
  must never show its last report".
- D24 (as `core/render.go:63` states it): "Filled ● is a reported state …
  hollow ○ is 'nothing reported at all' … because color alone cannot carry
  the reported-vs-not distinction." — this restyle deliberately revisits the
  command-row half of that decision; the project-head half stands.
- D31 (as `launcher/view.go:162` states it): "scrolling away from the cursor
  is what a wheel is for" — the switcher wheel adopts the same contract.

## Stubs (from PLAN.md open questions)

(none open)

## Resolved

- **R1 (Q1): name color per state** — user, 2026-08-19: "Strong red, flat
  yellow, idle/done -> flat green. unreported -> weak green." Mapping:
  waiting → strong red (bold, ANSI 1); working → flat yellow (plain ANSI 3);
  idle/done → flat green (plain ANSI 2); running-but-unreported → weak/dim
  green. Routine call on rendering "weak green": faint green
  (`StyleMarkerIdle.Faint(true)`), not a fg→bg blend — it stays a themed
  basic-ANSI color like the rest of the markers. Rejected: strong yellow as
  the attention color; working rendered strong.
- **R2 (Q2): dead rows keep the lifecycle word** — user, 2026-08-19. The word
  (`exited(0)` / `failed` / `pending…`) sits where the title would; a dead
  row has no title (D13) so it costs no title space; the name stays in the
  plain weak shade. Rejected: color-only (loses exit codes); word only on
  failure.
- **R3 (Q3): reported-vs-not survives as dim-vs-normal green** — user,
  2026-08-19 (consistent with R1's "unreported -> weak green"). D24's
  command-row hollow circle is retired; the distinction moves into green
  intensity. The project head's hollow/filled dot is untouched.
- **R4 (Q4): both surfaces** — user, 2026-08-19. The Commands tab
  (`cmdman/tui/view.go:renderCommandList`) adopts the same language as the
  switcher rows: name color carries reported state, fixed unbracketed index
  column, clamped title, no reported-status word. `ScaleBadge` is therefore
  replaced by `ScaleCell` repo-wide and deleted. Routine calls scoped with
  it: the tab's 1-cell lifecycle glyph column stays (spinner/◌/✔/✘ carry
  lifecycle, which name color does not), but the running `●` case renders a
  space — running is now the name's color's job; wheel-scroll remains
  switcher-only (the tab has its own selection/clamping model and was not
  what the request described).
- **R5 (Q5): reserved 2-cell bell column on every row** — user, 2026-08-19.
  Every command row keeps a permanent 2-cell slot (🔔 or two spaces) between
  the index column and the title, so titles align across belled and un-belled
  rows; alignment was chosen over the 2 cells it costs. Rejected: bell
  leading the title (belled row's title shifts); leaving it optional as
  today.
- **R8: the command name is clamped with a right-side ellipsis, name column
  ≥ 6 cells** — user, 2026-08-19: the name "should be clamped … with a right
  side ellipsis, but it should be at least six characters". A name longer
  than its column renders "…" at the cut instead of today's silent hard cut
  (`PadCells(name, 12)`); the column keeps its default width (12) when the
  pane affords it and shrinks before the title does on narrow panes, but
  never below 6 cells. Routine call: shrinking priority (name gives way
  first, floor 6) chosen so a narrow pane still shows some title — the
  payload — rather than a full name and nothing else.
- **R9: `M` summons the manager for the active project** — user, 2026-08-20:
  "Add binding M to open manager for currently displayed tmux window's
  dashboard target." `M` in the switcher opens the project manager for the
  group `activeMark` marks (the project owning the tmux window the caller
  displays, per `activeIdentity`; cwd fallback as today), regardless of the
  list selection; `m` keeps managing the selected row. Routine calls: no
  active group (or an unnamed one) → status-line message, mirroring
  `summonSelected`'s "no project to manage here"; if the cwd fallback marks
  several groups active (shared workdir), the first in list order wins —
  they sort to the top anyway; the footer hint becomes "m/M manage". (An
  earlier "always put dashboard target on top of the switcher list" idea was
  explicitly retracted by the user in the same exchange — not planned.)
- **R6 (Q6): keep the `" · "` separator before the title** — see below.
- **R7 (Q7): wheel scrolls the view; keyboard snaps back** — see below.

- **R6 (Q6): keep the `" · "` separator before the title.** Routine call made
  without the user (per base preference). Rationale: it is what visually
  separates the command's own words from the TUI's columns, and 2 cells is
  cheap next to a clamped title. Rejected: dropping it (marginal gain, blurs
  the boundary between name/index columns and free-text title).
- **R7 (Q7): wheel scrolls the view; the next keyboard move snaps the view
  back to the selection.** Routine call: D31 already fixes this contract and
  the launcher implements it ("scrolling away from the cursor is what a wheel
  is for"); two widgets with two wheel behaviors would be worse than either.
  Rejected: moving the selection into the visible window first.

- **Deletion of the three old badge helpers moves from step 1 to step 5**
  **[automatic]** — orchestrator, 2026-08-21. The run commits after every
  step, so step 1 deleting `RowStateBadge`/`ReportedStatusBadge`/`ScaleBadge`
  while their callers migrate only in steps 2/5 would leave commits 1–4
  unbuildable (and the repo's post-edit lint hook would flood step 1 with
  failures from untouched packages). Instead step 1 only adds the new
  helpers + tests; the deletions land in step 5 together with the last
  caller migration. Same end state, every commit green. Rejected:
  plan-as-written compile-break-guided migration.
