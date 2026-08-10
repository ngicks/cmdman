# Idea: quick-launch, frame, monitor terminal-state

Status: idea phase (2026-08-05). Not yet a plan — no PLAN.md/STATUS.md/DECISION.md.

This file states **how the features should be**: use cases and usability
requirements. It is deliberately blind to implementation cost — current code
structure, effort, and technical constraints get no vote here. The codebase
grounding (what exists, corrections, sketches, cost notes) and the
implementation-side open questions (11–20) live in [NOTES.md](./NOTES.md) and
feed PLAN.md's context when planning starts. PLAN.md may compromise against
this file only through a DECISION.md entry — never by quietly editing this
file down to what was convenient to build.

## Problem — pain points

Three distinct pains, one theme: too much manual work between "I want to work
on X" and "I am looking at X, knowing its state".

1. **Bring-up is tedious.** The usual workflow to bring up a dev environment:
   1. create a new pane
   2. `cd` into the project dir
   3. `cmdman compose -f devenv up`
   4. `cmdman compose -f devenv mux up`

   Four manual steps, every time, per project.

2. **No at-a-glance command status.** Concretely: LLM CLI agents (claude
   code, codex, …) running in panes — there is no way to see which one is
   waiting for input, still working, or done without visiting each pane.
   Generalized: **a command should be able to report its own state through
   the cmdman CLI**, and that state should be visible everywhere commands
   are listed (`ls`, TUI rows, switcher badges). This is the _active_ twin
   of Track C's passive BEL/title trapping — the same kind of runtime state,
   a different producer.

3. **Switching between windows is hard.** Getting from one running project
   to another means remembering which tmux window holds what. Wanted: list
   all muxctl-ed windows, fuzzy-find the tied project, jump. This folds into
   Track A's launcher (same fuzzy-find surface — running windows are just
   another entry kind whose selection is pure focus-switch) and is also what
   Track B's switcher strip solves ambiently.

The ideas below split into three feature tracks. A and C stand alone; B is
the integrator that consumes both.

---

## Track A — Quick-launch: compose history + launcher

The target: from anywhere in the terminal, **one gesture → a few letters →
Enter → the environment is up and you are looking at it**. The launch is not
done until focus lands inside the project; anything short of that leaves
manual steps alive.

### Use case: launch a project from anywhere

- **Actor / situation**: the user is anywhere in the terminal — mid-task in
  another project, or on a fresh shell — and decides to work on project X.
- **Intent**: get from "I want X" to "I am looking at X, running" without
  manual steps.
- **Walkthrough**: one gesture summons the launcher → a few typed letters
  narrow a merged list (recent projects, discovered projects, running
  windows) → Enter on the top hit → the environment comes up, its mux layout
  is shown, and **focus lands inside the project's window**. Done means
  looking at X.
- **Variant — background launch** (decided, Q2): a distinct select key —
  tentatively `s`, "start" — brings the environment up and creates its mux
  window in the currently attached tmux session **without switching
  focus** — "get X warming up while I keep working on Y". The window is
  there to switch to later; the launcher dismisses and the user is back
  where they were. Enter is the full launch + focus.

### Use case: jump to an already-running project

- **Actor / situation**: several projects are running across multiplexer
  windows; the user no longer remembers which window holds which.
- **Intent**: "go to X" — without first answering "is X running?".
- **Walkthrough**: the same gesture, the same list. Running entries read
  differently from cold ones (state at a glance) but live in the same list —
  the user thinks "go to X", not "is X running". Selecting a running entry
  is a pure focus switch. Re-launching a running project must never error or
  duplicate: it just takes you there (idempotent — reconcile and attach).
- **Ambient path**: when the Track B frame is up, the same jump is available
  without summoning anything — the always-displayed switcher (side column or
  bottom row) shows the running projects with their attention/title state;
  selecting one switches. The summoned full-sized launcher and the docked
  switcher are two form factors of one selector, not two features (see
  "One selector, three form factors" below).

### Use case: one-shot bring-up from the shell

- **Actor / situation**: the user is already in the project directory, shell
  at hand; summoning a launcher would be a detour.
- **Intent**: bring the environment up _and see it_, in one command.
- **Walkthrough**: today `compose -f devenv up` followed by
  `compose -f devenv mux up` is two spellings of one intent — "bring it up
  and show it". There should be a single spelling — decided (Q3):
  `compose up --mux`. CLI spelling is UX too.

### Usability requirements

- **Gesture** (decided, Q1) — a multiplexer key binding opening the
  launcher as a popup (the rofi/fzf/telescope feel: ambient, instant,
  dismissable) is primary and gets the polish; a shell verb
  (`cmdman launch [query]`-ish) and full-TUI navigation fall out of the
  same launcher view as secondary entry points.
- **Selection** (decided, Q9) — search-as-you-type over one merged,
  recency-sorted list: history, discovered projects, **and currently
  muxctl-ed windows** (pain point 3 — the launcher doubles as the window
  switcher). Running entries are marked with state/attention badges, not
  segregated. The top hit selectable with Enter alone; few-letter matching
  matters more than exhaustive listing.
- **Landing** (decided, Q2) — Enter: up + show + **switch focus to the
  project's window**. For an already-running entry the bring-up steps are
  no-ops and selection is a pure focus switch. A distinct select key
  (tentatively `s`) performs the same bring-up into the currently attached
  session without the focus switch (the background-launch variant above) —
  no-focus is a deliberate gesture, never a surprise.
- **Failure experience** — a history entry whose compose file has moved or
  been deleted should stay listed, surface the resolution error at launch,
  and offer removal — not fail cryptically, and not silently vanish from
  history.

### One selector, three form factors

The picker / selector / searcher is **one surface in three styles**, sharing
the merged list, the matching, and the selection semantics:

- **Side bar** — a docked thin column (a Track B frame component).
- **Bottom launcher** — a docked shallow, wide row (also a frame component).
- **Full-sized** — the full-fledged selector, summoned on demand in a
  floating window / popup (the gesture in the use cases above).

The docked forms are the frame's reason to exist: **always displayed**, so
projects needing attention (stopped for input) or progressing (title
updates) are visible while working elsewhere, with no gesture at all.
Switching projects works from either a docked form or the summoned
full-sized one — the docked forms trade search depth for permanence, the
full-sized form is where few-letter search shines. Cold launch (history +
discovered projects) belongs to the full-sized form; the docked forms are
running-projects-only (decided, Q9).

---

## Track B — Frame: docked screen-edge components around the project

### Concept

A **frame** reserves surrounding screen space (side menu, bottom bar, …) for
cmdman-provided display components. The project's mux layout renders in the
space the frame leaves over. It should feel like a window manager's panel
layer for the terminal: ambient chrome that is _just there_, while projects
come and go inside it.

Naming: "frame", deliberately not "layout" — "layout" already names the
marker-cycled pane layouts inside a project's mux spec (and a TUI tab).
Frame is a sibling concept above mux layouts, not a field inside them.

A frame is **not** declared in a project's compose/mux YAML. It is
user-level, and deliberately simple (decided, Q34/Q5/Q24 — DECISION.md
D15): **the frame is just a frame feature**. Frame definitions are saved
as standalone files under `<config-dir>/frame/`, exactly like compose defs
under `<config-dir>/compose/`; which def applies is passed via config or a
command flag. The frame is explicitly controlled — **shown / hidden /
selected / cycled** — by the user; a project's `mux up`/`down` never owns
it. There is no default frame; `switcher` and `statusbar` ship as built-in
components a def can reference (decided, Q6 — D16). Projects render
_inside_ the shown frame's main region.

### Use case: persistent chrome across projects

- **Actor / situation**: the user works across several projects in one
  sitting, switching, stopping, and relaunching them.
- **Intent**: the chrome (switcher, status bar) is a fixture — projects
  change inside it.
- **Walkthrough**: switching, stopping, or relaunching a project never
  rebuilds the chrome. Flicker or reflow of frame panes on project switch
  breaks the illusion that the frame is a fixture.

### Use case: ambient attention

- **Actor / situation**: the user is focused on one project while others run
  in the background.
- **Intent**: notice when a background project needs attention, without
  polling it.
- **Walkthrough**: the always-displayed switcher shows Track C's
  bell/title/status state — a project that has stopped for input glows for
  attention, and one still working shows progress through its title updates —
  without being focused, and without any gesture.

### Usability requirements

- **Persistence.** The frame outlives projects (the use case above).
- **Switching semantics** (decided, Q4) — selecting a project in the
  switcher **navigates between per-project windows**, keeping tmux-native
  window navigation (window list, `C-b n`/`p`) alongside. The rejected
  alternative was an in-place swap of one dashboard window's main region.
  The follow-up this opens: where the chrome lives — duplicated into
  every project window, or on one "home" window only. (Open question 34.)
- **Appearance trigger** (decided, Q5 via Q34) — explicit control only:
  the frame is shown / hidden / selected / cycled by the user; the def
  comes from config or a command flag. No lazy auto-appearance.
- **Degradation.** On small terminals the chrome must be collapsible with
  one gesture (like hiding an editor sidebar) — a frame that always eats 20%
  of a laptop screen would train the user to avoid it. The shown/hidden
  switching above is that gesture.

### Frame spec — the user-facing format

Frame defs live as named standalone files under `<config-dir>/frame/`
(like compose defs; decided, Q34 — this also settles implementation
question 14). A frame is a flat array of display components. Each entry
docks to one of the four screen edges and takes N cells or N% **of the
rectangle remaining at that entry's turn** (decided, Q27 — not percent
of the whole screen). What runs in the entry is a union of two mutually
exclusive keys:

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
_remaining_ rectangle, and whatever is left at the end is the main region
for the project. That is why the flat array expresses nesting without nested
structure — **order is the nesting**:

- `[left 20%, bottom 2]` → full-height side column; bottom bar spans only the
  remainder.
- `[bottom 2, left 20%]` → full-width bottom bar; shorter side column.

This order-dependence is the one non-obvious rule and must be pinned
explicitly in the spec doc. Two entries docking to the same edge (stacked
bottom bars) fall out of sequential carving for free.

### First consumer

The project-switcher strip — the docked form of Track A's selector ("One
selector, three form factors"): a side-bar column or bottom-launcher row
listing active projects (data source shared with Track A),
switching/attaching on select, showing bell/title/status badges from
Track C. The side-bar column is a **grouped list** — command titles under
project-path groups, bucket-sorted (decided, Q33); the shallow row form
shows one aggregated badge per project (Q26). Which docked style (side bar vs bottom row) is the user's choice
via the frame spec's `edge`/`size`, not a separate component per style.

---

## Track C — Runtime state: trapped (BELL / title) and reported (CLI)

Two producers of the same kind of per-command runtime state:

- **Passive trapping** — the monitor captures what the command already
  emits: BEL, terminal-title changes (OSC 0/2), maybe desktop-notification
  sequences (OSC 9/777).
- **Active reporting** — the command (or a hook around it) tells cmdman its
  state explicitly. Pain point 2's driving case: LLM CLI agents whose hooks
  (e.g. Claude Code hooks) call something like
  `cmdman report --status waiting` so "which agent needs me" is answerable
  at a glance.

Both land in the same place — per-command runtime state — and surface
through the same consumers.

### Use case: which agent needs me

- **Actor / situation**: several LLM CLI agents run in panes across
  projects; each is somewhere in a work/wait/done cycle.
- **Intent**: answer "which one needs me" at a glance, from anywhere.
- **Walkthrough**: each agent's hook reports its status through the cmdman
  CLI → one colored state word per command appears in `ls`, TUI rows, and
  the switcher → the user looks at the list, not at each pane.

### Signals and their lifecycles

Three distinct signals, with different lifecycles:

- **Title = live context.** What the command is _doing right now_ — the
  editor's open file, the current directory, a task phase. It should appear
  wherever the command is listed: `ls` rows, TUI rows, the switcher. Stale
  titles are worse than none; a title should die with the run.
- **Bell = attention.** An unread marker that persists until the user
  _looks_ — attaches or previews the command, or focuses the project — then
  clears. The unread lifecycle (what counts as "read"; per-command vs
  aggregated per-project in the switcher) matters more to the experience
  than the capture mechanics.
- **Reported status = intent.** A command saying `working` / `waiting` /
  `done` in its own words. Unlike a bell it is a _level_, not an edge: it
  stays whatever it is until re-reported, needs no read/unread lifecycle,
  and is exactly what "see my agents' status at a glance" wants — one
  colored word per command in `ls` and the switcher. Vocabulary (decided,
  Q10): a small enum for the badge + optional free-form detail shown in
  rows. Like titles, it dies with the run (decided, Q25).
- **Desktop notifications / OSC dispatch** (decided, Q8 — DECISION.md
  D17): cmdman grows a general **OSC hook system**. Trapped OSC
  sequences dispatch a configured command per hook; the built-in default
  behavior is passthrough. Built-in hooks ship so the common behaviors
  (badge, notify) need no shelling-out or user definition. Hooks are
  configured **per command** as persistent base-system configuration (not
  a compose-level extension), and a frame definition can override the
  default hooks while that frame is in play.

### Consumers

- Titles and reported status shown in `ls` / `ps` / TUI rows.
- Bell → unread badge, reported status → per-command state word, both in
  the Track B switcher.
- The launcher (Track A): running entries can show aggregated
  status/attention so "which project needs me" is answerable from the
  jump list itself.

---

## How the tracks relate

- B's switcher widget consumes A's "active projects" enumeration and C's
  bell/title/reported-status state; build order stays free.
- A's launcher can likewise show C's per-project attention/status in its
  jump list, but degrades gracefully without it.
- A is standalone and hits the daily pain directly; C is standalone; B is
  the integrator. (Cost and sequencing notes: NOTES.md.)

## Open questions — usability

These decide how the features should feel, and get resolved before planning
starts. Implementation-side questions (11–20) are recorded in NOTES.md and
get decided during planning, downstream of these answers.

1. **(A) Launcher gesture** — ✅ **Resolved (2026-08-09)**: popup key
   binding primary; shell verb and full-TUI navigation are secondary entry
   points into the same launcher view. (DECISION.md D3.)
2. **(A) Landing** — ✅ **Resolved (2026-08-09)**: two endings — launch
   with focus switch (idempotent for running projects), and a bring-up
   without the focus switch. (DECISION.md D4.) **Key spelling revised
   by D28**: in the two-pane launcher, `S` = jump into the path +
   attach (launch + focus), `s` = start and leave (background); Enter
   moves focus from the location pane into the project pane.
3. **(A) One-shot CLI spelling** — ✅ **Resolved (2026-08-09)**:
   `compose up --mux`. (DECISION.md D5.)
4. **(B) Switching semantics** — ✅ **Resolved (2026-08-09)**: (b)
   per-project windows; tmux-native navigation stays. Opens question 34
   (chrome placement). (DECISION.md D6.)
5. **(B) Frame appearance trigger** — ✅ **Resolved (2026-08-09)** by the
   Q34 reframe: explicit shown / hidden / selected / cycled control; the
   def is passed via config or command flag; no lazy appearance.
   (DECISION.md D15.)
6. **(B) Default frame contents & collapse** — ✅ **Resolved
   (2026-08-09)**: no default frame; `switcher` and `statusbar` ship as
   built-in components referenced from user defs. Shown/hidden switching
   is the collapse gesture. Usability to be reviewed on a mock TUI
   before the spec freezes. (DECISION.md D16.)
7. **(C) Bell read/clear semantics** — ✅ **Resolved (2026-08-09)**:
   cleared by attach/preview of that command; the switcher aggregates
   unread per project. (DECISION.md D11.) **Amended (D22/D23)**: in the
   switcher/selector, unread renders as a 🔔 marker **in place of** the
   project's status dot until checked; it resolves when the project is
   selected through the selector (the dot then reappears), and
   immediately if the project is already the selected one when the bell
   arrives.
8. **(C) Notification relay** — ✅ **Resolved (2026-08-09)**, reframed:
   neither badges-only nor a hardwired relay — a per-command **OSC hook
   system** in the base configuration. Hooked OSC dispatches a configured
   command; default behavior is passthrough; built-in hooks cover common
   behaviors without user definitions; frame defs can override defaults.
   Config schema is implementation question 35. (DECISION.md D17.)
9. **(A) Jump-list presentation** — ✅ **Resolved (2026-08-09)**: one
   recency-sorted list; running entries marked with state/attention
   badges, not segregated; docked forms running-only, cold launch via the
   full-sized form. (DECISION.md D7.)
10. **(C) Reported-status vocabulary** — ✅ **Resolved (2026-08-09)**:
    small enum (`working`/`waiting`/`done`) rendered as consistent
    badges + optional free-form detail in rows; shown in `ls`, TUI rows,
    and the switcher. (DECISION.md D12.)

Questions 21–26 below were found reviewing the use cases end to end
(2026-08-08). They are usability questions like 1–10; numbering continues
after NOTES.md's implementation questions 11–20 so cross-references stay
stable.

21. **(A) Launch from outside tmux** — ✅ **Resolved (2026-08-09)**:
    auto-create-or-attach — cmdman creates or picks the session, creates
    the window, and attaches/switches the client, so the landing promise
    holds everywhere, even from a bare shell. (DECISION.md D8.)
22. **(A×B) Landing under in-place swap** — ✅ **Resolved (2026-08-09)**
    by question 4's answer: per-project windows everywhere, so Track A's
    landing ("focus lands in the project's window") holds unchanged, with
    or without a frame. (DECISION.md D6.)
23. **(A) Entry display & match string** — ✅ **Resolved (2026-08-09)**:
    rows read **`path (project)`** — path first, since multiple projects
    can run on one path. The path renders git-aware: for a git workdir,
    **repository name + branch name**; otherwise the dir basename. The
    fuzzy match runs over full path, git repository uri, branch name,
    and project name. How git info is obtained/cached is implementation
    question 36. (DECISION.md D18.)
24. **(B) Frame scope** — ✅ **Resolved (2026-08-09)** by the Q34
    reframe: scope dissolves — a frame is shown/hidden per explicit
    invocation, not a per-session singleton cmdman manages. Switchers
    list all projects globally (to be confirmed on the mock).
    (DECISION.md D15.)
25. **(C) Reported-status lifecycle across runs** — ✅ **Resolved
    (2026-08-09)**: status dies with the run, like titles; an exited
    command shows its exit state, never a stale report from a dead
    process. (DECISION.md D13.)
26. **(C) Per-project status aggregation** — ✅ **Resolved (2026-08-09)**:
    attention wins — waiting > working > done; an unread bell outranks
    all. (DECISION.md D14.)

Questions 27–33 below came out of a second end-to-end review
(2026-08-09) — corners the earlier passes left unstated or, in one case
(27), internally contradictory.

27. **(B) Percent base in the frame spec** — ✅ **Resolved (2026-08-10)**:
    percent of the **remaining rectangle**, confirmed on the mock
    (`side+statusbar` vs `statusbar-first` defs); spec text reworded;
    pinned next to the order-is-nesting rule. (DECISION.md D30.)
28. **(A) Mux-less projects** — ✅ **Resolved (2026-08-09)**: plain `up`
    plus a synthesized one-pane shell window at the workdir, **with a
    warning telling the user the project is attached but no mux section
    was found** — the landing promise holds everywhere, and the warning
    explains why the window looks bare. (DECISION.md D9.)
29. **(A) Launch feedback** — ✅ **Resolved (2026-08-09)**: land
    immediately — the launcher dismisses on Enter and focus switches as
    soon as the window exists; the user watches bring-up in place.
    Failures additionally raise the project's attention state (Track C
    bell/badge), so a partial failure is noticed even from elsewhere.
    Errors that prevent landing entirely — e.g. a stale history entry
    that fails resolution before any window exists — still surface in
    the launcher with removal offered. (DECISION.md D10.)
30. **(B) Swap-out experience** — ✅ **Resolved (2026-08-09)**: moot —
    question 4 chose per-project windows, so nothing is swapped out;
    windows persist with their pane state. (DECISION.md D6.)
31. **(B) Docked switcher interaction** — ✅ **Resolved (2026-08-10)**:
    the in-strip model is settled by the mock rounds — mouse click
    selects / toggles, wheel scrolls, and the zone/key model of
    D24/D28/D29 applies. How the strip is *reached* by keyboard in real
    tmux (pane navigation vs a dedicated binding) is **deferred** — to
    be planned and implemented later, with phase 3. (DECISION.md D31.)
32. **(C, B) `command:` frame-entry lifecycle** — ✅ **Resolved
    (2026-08-09)**: ephemeral pane by default; setting `managed: true`
    on the entry makes it a cmdman-managed (supervised) command that
    survives hide/close. (Informs implementation question 16.)
    (DECISION.md D19.)
33. **(C) Per-project title representation** — ✅ **Resolved
    (2026-08-09)**, reframed: no single representative title. The
    switcher is a **grouped list**: groups keyed by project path, with
    each command's title listed under its group — the wezterm-tab-title
    experience, but for every pane at once from the side bar. Ordering:
    by title-update time, **bucketed** (≈5 s chunks, exact interval
    tunable) so frequently-retitling agents don't race each other in the
    list; within a bucket, sorted by name-or-id. (DECISION.md D20.)
34. **(B) Chrome placement under per-project windows** — ✅ **Resolved
    (2026-08-09)** by reframing: the home-vs-per-window question was
    over-complicated. The frame is a standalone feature the user shows /
    hides / selects / cycles, with defs under `<config-dir>/frame/`
    passed via config or command flag and built-in components to display.
    Where a shown frame's panes physically live is now an implementation
    concern (folds into question 13), validated on a mock TUI first.
    (DECISION.md D15.)
