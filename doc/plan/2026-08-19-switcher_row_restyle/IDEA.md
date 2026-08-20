# Switcher command row restyle — how it should be

Gate: confirmed by user, 2026-08-20 (reconfirmed after the `M`-binding
addition; first confirmed 2026-08-19)

## One-line statement

The switcher widget's command rows exist to show the title an agent keeps
updating (its live progress report); everything else on the row should spend as
few cells as possible and never wobble the alignment, so the eye can scan
titles down a stable column.

## Actors and situation

- **Actor**: the user running several agent-driven commands (e.g. coding
  agents) under compose projects, with the switcher docked as a narrow column
  beside their work.
- **Situation**: each command continually rewrites its title to say what it is
  doing ("editing foo.go", "running tests…"). The user glances at the column to
  see who is doing what and who needs them.

## Use case walkthrough — glancing at progress

1. The user glances at the docked switcher column.
2. Every command row shows, in one line: the command name, which replica it is,
   a bell if it rang, and the title it last set.
3. The title is clamped to the column width — a long title is cut with an
   ellipsis, never wrapped, never pushed out by decoration.
4. State is read from the command name's color, not from a glyph:
   - a command that needs nothing reads calm (flat/weak color);
   - a command that needs attention reads loud (strong color) — plus its bell.
5. All rows align: name column, index column, and title column start at the
   same x for every row, whether or not the command is scaled.

```
Before (current)                          After (proposed)
 ● ~/work/proj (alpha)                     ● ~/work/proj (alpha)
    web          working [2] · build…         web           2    · building assets…   (flat yellow name)
    web          working [10] · lint          web          10    · linting            (flat yellow name)
    db           ○                            db                                      (dim green name)
    agent        waiting 🔔 · need review     agent            🔔 · need review, plea… (strong red name)
```

(the picture shows the columns: name(12) · index(2, unbracketed) · bell(2) ·
title(clamped); removed: the state word / circle and the `[ ]` brackets.)

## Use case walkthrough — scrolling with the mouse

1. The user hovers the docked switcher column, which lists more projects than
   the pane has rows.
2. They roll the mouse wheel; the list scrolls under the pointer, a few lines
   per notch, without moving the selection — the same contract the launcher's
   panes already honor (wheel scrolls, cursor stays; D31).
3. A click still lands on the row that is visually under the pointer after the
   scroll.
4. Moving the selection with the keyboard afterwards brings the selected group
   back into view, as it does today.

## Use case walkthrough — managing the project on screen

1. The user is sitting in (or looking at) a project's tmux dashboard window
   and has the switcher open; the list cursor may be anywhere.
2. They press `M`; the project manager opens for the project that owns the
   currently displayed tmux window — the one the switcher marks `active` —
   without them having to move the cursor to it first.
3. Lowercase `m` keeps its meaning: manage the *selected* row.
4. When no window answers (no active project), the hint line says so instead
   of guessing.

## Usability requirements

- **Title is the payload.** The title is what the row exists to show. After
  the fixed columns (name, index, bell), the whole remaining width belongs to
  the title. Clamp it to the widget width with a right-side ellipsis; it must
  never cause the line to exceed the pane.
- **The name is clamped too.** A command name longer than its column is cut
  with a right-side ellipsis ("…"), never hard-cut and never allowed to push
  the columns after it. The name column is at least 6 cells: when the pane is
  too narrow for the default column plus a useful title, the name column
  shrinks before the title does, but never below 6 (R8).
- **State by color, not by glyph.** The per-row state word / hollow circle is
  removed; the command name's color carries the state (R1/R3): waiting on the
  user → strong red; working → flat yellow; reported idle/done → flat green;
  running but nothing reported yet → dim green. A dead row keeps its
  lifecycle word (`exited(0)`, `failed`, …) where a title would sit (R2). The
  project head keeps its one marker slot — this change is about command rows.
- **Both surfaces speak it.** The switcher widget and the main TUI's Commands
  tab render command rows in the same language (R4); the wheel-scroll use
  case is the switcher's own.
- **Index is a fixed column.** The scale index drops its square brackets.
  Replicas are assumed to stay under 100, so the index is right-aligned in a
  fixed 2-cell column; an unscaled command (and index-less rows in general)
  renders 2 spaces there so the rest of the row never shifts.
- **Alignment is unconditional.** No optional element may move the columns to
  its right: the scale index and the bell each occupy a fixed slot on every
  row (spaces when absent — R5), so name, index, bell, and title columns
  align across all rows.
- **Failure experience.** A dead command must not look alive: whatever a
  finished run shows, it must stay distinguishable from a live one and must
  not show the stale title (D13 today).
- **`M` manages what you're looking at.** `M` opens the project manager for
  the currently displayed tmux window's project (the `active` one), cursor
  position irrelevant; `m` stays "manage the selected row". No active
  project → a hint-line message, not a no-op.
- **The wheel scrolls the pane.** Mouse wheel over the switcher scrolls the
  list a few lines per notch without moving the selection; clicks resolve
  against the scrolled view. Keyboard movement snaps the selection back into
  view as today.
