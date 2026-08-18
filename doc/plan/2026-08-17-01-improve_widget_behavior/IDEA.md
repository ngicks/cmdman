# IDEA — improve TUI widget behavior

> The idea gate was skipped at the user's explicit direction (see DECISION.md
> D1). This file is a brief statement of "how it should be" derived from the
> user's four directives, kept so PLAN.md's goal and scope have something to
> derive from — it is not a full idea-phase exploration.

## The four behaviors, as they should be

### 1. The launcher shows what config knows, not only what history remembers

A user who has dropped a compose project file under the cmdman config
directory (`<config dir>/cmdman/compose/`) opens the launcher and sees that
project listed — even though they have never brought it up on this machine.
Registering a project in config is the act of telling cmdman "this exists";
the launcher honoring only history makes a fresh machine (or a fresh
`data_dir`) show an empty launcher despite a fully configured setup.

Walkthrough: the user syncs their dotfiles to a new machine, which places
named compose projects under the cmdman config dir. They summon the launcher.
The left pane lists the locations of those config-known projects alongside
(and after) any history rows. They cursor to one, enable it, press `S`, and
land in its freshly created window. No typing-to-search required for
something cmdman was already told about.

### 2. Tearing down belongs in the widgets, not only the CLI

Every widget that can bring a project up or manage it can also take it down —
both ways down exists:

- **mux down** — the dashboard windows go away; the supervised commands keep
  running. Non-destructive; the multiplexer is a disposable viewer.
- **compose down** — the project's supervised commands are stopped and
  removed. Destructive; the user must not trigger it by a slipped keypress.

The launcher, the switcher, and the project manager each offer both gestures,
with the same keys meaning the same thing in all three, and the destructive
one guarded by an explicit confirmation.

### 3. The launcher never shares a window between projects

Launching from the launcher always gives each project its own multiplexer
window. Two projects — even two that happen to share a compose project name
in different directories, or two projects with no name at all — never land in
one window. A window belongs to exactly one project identity.

### 4. `z` (hide frame) leaves the switcher

The docked switcher's `z` collapse gesture is removed. Hiding the frame from
inside a pane that the hide itself kills leaves the multiplexer's persisted
frame state stale (`cmdman mux frame show` then no-ops until a CLI
`hide`/`show` round-trip). Frame show/hide remains a CLI gesture
(`cmdman mux frame show|hide`), operated from outside the frame, where its
state handling is reliable.

### 5. Bringing a project up never cycles the layout

Starting or launching a project from a widget applies a predictable layout:
a fresh dashboard window opens on the first layout; an existing dashboard
keeps the layout it is already showing. Layout cycling is its own explicit
gesture (repeated `cmdman mux up` / `compose mux up` on the CLI, `c` in the
project manager) — it never happens as a side effect of bringing something
up.

Observed bug (2026-08-17): launch project A (dashboard up), then start
project B — B takes over A's window (the item-3 sharing bug) *and* the
window flips to the second layout, because a bring-up with no explicit
layout means "cycle to the next one".

## Usability requirements

- Config-known projects are visible by default but must not silently join
  bulk actions the user aimed at their actively-used projects.
- Down gestures give feedback in the widget (status line: what was torn
  down / stopped / removed, or why it failed), and the destructive one
  reads back what it is about to do before doing it.
- Key meanings stay consistent across the three widgets.
- All key-surface documentation (cobra `Long` help, man pages) tells the
  truth after the change: no mention of `z`, full mention of the down keys.
