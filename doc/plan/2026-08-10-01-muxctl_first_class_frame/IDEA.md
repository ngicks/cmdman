# Idea: a first-class frame in the muxctl driver

**Provenance.** This is the spun-off plan
[D1](../2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md) mandates
and D36 commits to, scoped by step 14 of the
[parent plan](../2026-07-26-01-quicklaunch_frame_monitor_state/PLAN.md).
The parent blocks on it from its step 15 on.

This file is **derivative, not a second source of truth**. The parent plan
is finalized and binding; D15 (frame is a standalone show / hide / select /
cycle feature with named defs), D6 (per-project windows), D16 (no default
frame; `switcher`/`statusbar` are built-ins) and D19 (`command:` entries
ephemeral, `managed: true` opt-in) already fix the user-facing behavior and
are **not re-litigated here**. What this file states is the behavior the
*driver layer* must deliver so those decisions are physically possible.

## Who the "user" is here

Two, and both matter:

1. **The frame verbs of the parent plan's step 15** (`show` / `hide` /
   `select` / `cycle`) — the programmatic caller. Their ergonomics are this
   plan's API-design constraint.
2. **The end user**, whose experience the verbs deliver. Their requirements
   are inherited from the parent IDEA.md's Track B ("Persistence",
   "Degradation", "ambient attention").

The frame's *content* is already built:
`pkg/cmdman/frame` parses defs, carves them onto a `muxctl.PaneSpec` tree
(`carve.go:41`), and names the entry panes `frame-<i>`
(`carve.go:22-25`). What is missing is a driver that can put that tree
around a project window and leave it there.

## Use cases

### Show a frame around a project that is already running

- **Actor / situation**: the user has a project up in a tmux window, panes
  running attach/log viewers.
- **Intent**: dock the switcher column (and a status bar) around it.
- **Walkthrough**: the frame panes appear at the edges; the project's panes
  shrink to the remaining rectangle and **keep running** — no viewer
  restart, no scrollback loss, no re-attach flicker. Focus stays where the
  user had it, or lands in the project region — never in the switcher.

### Work the project while the frame is shown

- **Actor / situation**: framed window, user runs `cmdman mux up` again,
  cycles layouts, or cycles a command's scale.
- **Intent**: normal project operations.
- **Walkthrough**: every project operation rebuilds only the project
  region. The frame panes are not killed, not respawned, and never receive
  the viewer detach key sequence. Layout cycling still advances (`up` →
  next layout, not "back to layout 0").

### Tear the project down and keep the chrome

- **Actor / situation**: `cmdman mux down` (or `compose down`) on a framed
  project.
- **Intent**: the project's viewers go away; the chrome is a fixture and
  stays.
- **Walkthrough**: the project region collapses to a clean shell pane; the
  frame stays exactly as it was. The window remains recognizable as
  *framed*. This is the case the parent's rejected compile-in shape could
  not deliver.

### Hide, select, cycle the frame

- **Actor / situation**: laptop screen, or a different def wanted.
- **Intent**: reclaim space with one gesture / swap the chrome.
- **Walkthrough**: hide removes the frame panes and the project region
  expands to the whole window with its panes intact. Select/cycle swaps one
  def's panes for another's in one visible step — the project region is
  resized, never rebuilt.

### Find the project while it is framed

- **Actor / situation**: `cmdman mux ls`, `mux down`, and the launcher's
  running-window enumeration (parent phase 1, step 4).
- **Intent**: a framed project is still a project.
- **Walkthrough**: enumeration lists the window and reports which project
  it holds, from any context (no attached client, no `$TMUX`) — the
  property `pkg/muxctl/doc.go:9-12` already demands of ownership stamps.
  Launcher landing focuses the project region.

### Show a frame before any project exists

- **Actor / situation**: the user shows a frame first, then launches into
  it.
- **Intent**: the chrome is the fixture; projects arrive later.
- **Walkthrough**: the frame appears with an empty main region the next
  project fills. *(What legitimately occupies "empty" is open — Q6.)*

## Usability requirements

- **Persistence.** Frame lifetime is independent of project lifetime in
  both directions: project churn never rebuilds the frame, frame churn
  never rebuilds the project.
- **No process casualties.** The muxctl rule (`pkg/muxctl/doc.go:5-7`)
  holds unchanged: neither operation stops an observed process. Frame
  widgets are viewers too — `hide` may kill them (D19's ephemeral default),
  `mux up` may not.
- **Focus never lands in the chrome.** A left- or top-docked entry is the
  first leaf of the carved tree (`carve.go:69-80`), and the driver's focus
  fallback picks the first leaf (`pkg/muxctl/layout.go:104-108`) — so the
  naive composition puts the cursor in the switcher on every apply. The
  cursor belongs in the project.
- **Works without an attached client.** Every frame operation must be
  addressable by window id / identity, like `Down` already is
  (`pkg/cmdman/mux/down.go:54-58`) — the frame verbs may run from a popup,
  a key binding, or a script.
- **Failure experience.** A terminal too small for the def degrades the way
  layouts already do — panes that cannot be realized are skipped and warned
  (`pkg/muxctl/tmux/apply.go:45-51`), never a hard failure that leaves a
  half-built window. Showing a frame on a window that already has one, or
  hiding one that is not shown, is a no-op, not an error.
- **Discoverability of state.** "Is this window framed, with which def, and
  which project is in it" must be answerable from the window itself — the
  same self-describing property `@cmdman_window` gives today
  (`pkg/muxctl/tmux/tmux.go:12-19`).
