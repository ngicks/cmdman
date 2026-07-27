# Thoughts: quick-launch, frame, monitor terminal-state

Status: brainstorm / consolidation. **Not yet a plan** — no PLAN.md/STATUS.md/DECISION.md;
this file collects and sorts feature ideas before planning starts.

## Problem

The usual workflow to bring up a dev environment is tedious and slow:

1. create a new pane
2. `cd` into the project dir
3. `cmdman compose -f devenv up`
4. `cmdman compose -f devenv mux up`

Four manual steps, every time, per project.

The ideas below split into three feature tracks plus cross-cutting notes.
The tracks are buildable in any order: A and C are standalone, B is the
integrator that consumes both.

---

## Track A — Quick-launch: compose history + launcher

### What exists already

- The TUI already has a Compose tab that can run `compose up`
  (`pkg/cmdman/tui/composeup.go`) and a mux integration (`pkg/cmdman/tui/mux.go`).
- Search-as-you-type filtering exists (`pkg/cmdman/tui/filter.go`).
- `workDir` override is plumbed through `RunTUI` (`pkg/cmdman/cli/tui.go`).
- `compose ls` / `mux list` already enumerate active projects.
- The store already does schema migrations (`cmdman migrate`; `exit_history`
  table exists), so adding a table is cheap.

So this is less "new tab" and more "new data source + launcher flow" on top of
existing parts.

### What's new

- Persist a history of compose invocations: project dir + `-f` files, plus
  last-used timestamp. Likely a new store table.
- A launcher input: search-as-you-type over history with tab-completion;
  selecting an entry runs `up` + `mux up` in one action.
- List active projects and attach from the TUI — presentation over the existing
  `compose ls` / `mux list` plumbing, not new machinery.

### Worth considering alongside

A pure-CLI shortcut may kill 80% of the tedium without the TUI:
e.g. `cmdman compose -f devenv up --mux` — one flag that chains `mux up` after
`up`. The TUI launcher then becomes convenience on top rather than the only fix.

---

## Track B — Frame: docked screen-edge components around the project

### Concept

A **frame** reserves surrounding screen space (side menu, bottom bar, …) for
cmdman-provided display components. The project's mux layout renders in the
space the frame leaves over.

Naming: "frame", deliberately not "layout" — `mux.Spec` already has
`Layouts []Layout` (marker-cycled pane layouts) and the TUI already has a tab
named "Layout". Frame is a sibling concept above mux layouts, not a field
inside them.

### Not part of a compose project

A frame is **not** declared in a project's compose/mux YAML. It is user-level
(own YAML or config), which matches its job: it wraps and outlives any single
project, since its whole point is hosting cross-project elements like the
switcher strip.

Lifecycle follows from that: a project's `mux up`/`down` does not own the
frame. The frame is either brought up separately (`cmdman frame up`-ish) or
lazily created when the first project is displayed into it; projects render
*inside* its main region.

### Data model: flat array, edge docking

A frame is a flat array of display components. Each entry docks to one of the
four screen edges and takes N cells or N% of the screen. What runs in the
entry is a union of two mutually exclusive keys:

- `component:` — a built-in cmdman widget, callable in few letters
  (e.g. `switcher`, `statusbar`).
- `command:` — arbitrary argv, so anything can be a frame entry
  (a clock, `btop`, a log tail, …).

```
frame:
  - edge: left      # top | bottom | left | right
    size: 20%       # N cells or N%
    component: switcher
  - edge: bottom
    size: 2
    component: statusbar
  - edge: right
    size: 30%
    command: ["btop"]
```

Space is carved **sequentially**: each entry takes its slice from the
*remaining* rectangle, and whatever is left at the end is the main region for
the project. That is why the flat array expresses nesting without nested
structure — **order is the nesting**:

- `[left 20%, bottom 2]` → full-height side column; bottom bar spans only the
  remainder.
- `[bottom 2, left 20%]` → full-width bottom bar; shorter side column.

This order-dependence is the one non-obvious rule and must be pinned explicitly
in the spec doc.

Two entries docking to the same edge (e.g. two stacked bottom bars) falls out
of sequential carving for free — allowed unless a reason to forbid appears.

### Implementation notes

- **Units:** tmux sizes in cells and percent, not pixels — "N pixels" becomes
  N cells (`-l N`) / N% in practice. Percent should resolve against the current
  remaining rectangle, which is what tmux `split-window -l N%` does naturally
  and conveniently matches the carving semantics.
- **Mapping is pleasant:** applying a frame is a sequence of splits from the
  window edges *before* the project layout is applied to the leftover pane.
  The flat array translates 1:1 into split order — the muxctl layer needs no
  new tree machinery.
- **Entries as leaves:** the mux model already resolves leaves into argv via
  `Resolver`; frame entries are just panes with argv. `component:` resolves to
  a cmdman-provided widget invocation (e.g. `cmdman tui --widget switcher`)
  and `command:` passes argv through verbatim — both collapse to the same
  pane-with-argv shape below the spec layer. Keeps muxctl/tmux almost
  untouched; mostly a `pkg/cmdman/mux`-level (or new sibling package)
  spec/build change.
- **Ownership:** `mux down`'s identity-based teardown currently assumes windows
  belong to a project. Frame panes need their own identity stamp so a project
  teardown never reaps the frame, and vice versa.

### First consumer

The project-switcher strip: a thin column or short row listing active projects
(data source shared with Track A), switching/attaching on select, showing
bell/title badges from Track C.

---

## Track C — Monitor: trap BELL / OSC titles / notifications

### What exists

`pkg/cmdman/monitor/terminal_screen.go` already parses output with
`charmbracelet/x/ansi`, but nothing currently captures BELL or OSC 0/2 — this
is an extension of existing parsing, not new infrastructure.

### Storage — do not reuse labels

`Labels` lives on `CommandConfig` (`pkg/cmdman/model/command_config.go`) and is
user-supplied *configuration*; titles/bells are *runtime state* that changes
constantly. Mixing them means config writes on every title change and namespace
collisions with user labels.

Options, in rough preference order:

1. **In-memory in the monitor**, exposed over the monitor's existing server API
   (`monitor/mon_server.go`). `ls`/TUI/switcher query it live; nothing
   persists, which matches the ephemerality of a title.
2. **Emit into the existing `eventlog`** if history/notifications are wanted.
3. **Store schema change** only if the state must survive being queried from
   processes that cannot reach the monitor socket.

### Consumers

- Titles shown in `ls` / `ps` / TUI rows.
- Bell → unread badge in the Track B switcher.

---

## Dependencies between tracks

- B's switcher widget consumes A's "active projects" enumeration and C's
  bell/title state — but both already have (or can have) APIs independent of
  B, so build order is free.
- A is standalone and hits the daily pain directly; C is standalone; B is the
  integrator.

## Open questions

1. History keying and dedupe for Track A: project dir + file set? What happens
   when files move?
2. Which sequences Track C traps: BELL and OSC 0/2 for sure; OSC 9 / OSC 777
   desktop-notification variants are a possible scope-creep line.
3. Is the one-shot CLI chaining (`up --mux` or similar) in scope as Track A's
   step zero?
4. Where does the frame spec live: standalone YAML, user config, or both?
5. Frame lifecycle trigger: explicit `frame up` vs lazy creation on first
   project display — or both?
6. Should frame entries also support referencing a cmdman-managed command by
   name/ID (a third union variant), the way mux leaves resolve names into
   `cmdman attach <id>` — or is `command: ["cmdman", "attach", ...]` spelled
   out explicitly good enough?
