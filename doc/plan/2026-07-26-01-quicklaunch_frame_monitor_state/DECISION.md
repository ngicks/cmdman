# Decision log

One entry per material decision: the choice, the rationale, the rejected
alternatives. Stubs below are seeded from the open questions (usability
1–10, 21–33 in IDEA.md; implementation 11–20 in NOTES.md) and get filled
as each resolves. Decisions made while grounding (not user-facing
questions) are recorded at the bottom.

## Stubs — usability (IDEA.md)

- [x] Q1 launcher gesture → D3
- [x] Q2 landing & background-launch spelling → D4
- [x] Q3 one-shot CLI spelling → D5
- [x] Q4 switching semantics (in-place vs per-window) → D6
- [x] Q5 frame appearance trigger → D15
- [x] Q6 default frame contents & collapse gesture → D16
- [x] Q7 bell read/clear semantics → D11
- [x] Q8 notification relay scope → D17 (reframed: OSC hook system)
- [x] Q9 jump-list presentation & docked-form contents → D7
- [x] Q10 reported-status vocabulary → D12
- [x] Q21 launch from outside tmux → D8
- [x] Q22 landing under in-place swap → D6 (moot: per-window everywhere)
- [x] Q23 entry display & match string → D18 (git-aware)
- [x] Q24 frame scope (per session / client) → D15 (dissolved)
- [x] Q25 reported-status lifecycle across runs → D13
- [x] Q26 per-project status aggregation → D14
- [x] Q27 percent base in frame spec → D30
- [x] Q28 mux-less projects → D9
- [x] Q29 launch feedback → D10
- [x] Q30 swap-out experience → D6 (moot: nothing is swapped out)
- [x] Q31 docked switcher interaction → D31 (strip-reach deferred)
- [x] Q32 `command:` frame-entry lifecycle → D19 (`managed: true` opt-in)
- [x] Q33 per-project title representation → D20 (grouped, bucket-sorted)
- [x] Q34 chrome placement under per-project windows → D15 (reframed)

## Stubs — implementation (NOTES.md)

- [x] Q11 history writer site → D34 (compose.Service.Create)
- [x] Q12 stale history rows → keep + surface at launch with removal
      offered (D10/D28; validated in the launcher mock's stale-entry
      flow — inline error, ctrl+d removes)
- [x] Q13 frame build shape → D36 (straight to first-class)
- [x] Q14 frame spec location → D15 (`<config-dir>/frame/<name>.yaml`)
- [x] Q15 widget entrypoint spelling → D37 (`cmdman tui widget <name>`)
- [x] Q16 named-command frame entries → D38 (no; argv covers it)
- [x] Q17 runtime-state storage → D32 (monitor-held, server-stream RPC)
- [x] Q18 TTY-only capture → D35
- [x] Q19 extra OSC capture scope → D39 (BEL + title + 9/777 first)
- [x] Q20 report verb spelling & transport → D33 (`status set/get/delete`, socket)
- [x] Q35 OSC hook config schema → D40 (command+global; async exec+env; passthrough/block)
- [x] Q36 git info source for launcher rows → D41 (exec git)

## Decided

### D1 (2026-08-09) — B's muxctl contract revision is a separate plan

**Choice:** PLAN.md phase 3 treats the first-class frame (subtree-scoped
apply, identity coexistence, `resetWindow`/`Detach` sparing frame panes)
as its own plan directory in the muxctl plan series; this plan only
states its requirements and consumes the result.

**Rationale:** it revises muxctl's documented one-window-one-owner
contract (`pkg/muxctl/session.go`) — a change with its own blast radius,
test surface, and design questions, independent of tracks A/C which
carry the daily pain relief.

**Rejected:** folding the revision into this plan (couples A/C delivery
to the riskiest work); shipping the compile-in shape as the destination
(frame dies with `mux down`, killed on every layout cycle — contradicts
IDEA.md's lifecycle).

### D2 (2026-08-09) — history keys on `(WorkDir, Project)`, canonicalized as `compose/hash.go`

**Choice:** `ComposeHistory` primary key is the `(WorkDir, Project)`
pair with `WorkDir` canonicalized via `filepath.Clean(filepath.Abs(p))`
without symlink resolution — the `compose/hash.go` form.

**Rationale:** that pair is already project identity everywhere
(label filter, `service_list` grouping, mux `GenerateProjectIdentity`);
using the same canonicalization keeps history keys and mux identities in
agreement, which the launcher's running-window mapping relies on.

**Rejected:** compose config hash as key (deliberately excludes file
path and project name); the TUI `normalizePath` symlink-resolving form
(would disagree with mux identity).

### D3 (2026-08-09, Q1) — popup key binding is the primary launcher gesture

**Choice:** a tmux key binding opening the launcher as a popup (via the
TUI's existing `--popup` mode) is primary and gets the polish.

**Rationale:** the rofi/fzf feel — ambient, instant, dismissable from
anywhere — matches the "from anywhere" use case; the delivery vehicle
already exists.

**Rejected as primary** (still available as secondary entry points into
the same view): shell verb; full-TUI navigation.

### D4 (2026-08-09, Q2) — Enter = launch + focus; `s` = start-only

**Choice:** Enter performs up + mux up + focus switch, idempotent for
already-running projects (reconcile and attach). A distinct select key —
tentatively `s`, "start" — performs the same bring-up in the currently
attached session without the focus switch (background launch).

**Rationale:** the user wants both endings as first-class gestures:
"launch and go there" and "start it warming up while I stay here". A
separate key reads better than a modifier on Enter.

**Rejected:** modifier-on-Enter for background launch (less
discoverable than a distinct key); focus-always (loses the background
case); never-focus (breaks the landing promise).

### D5 (2026-08-09, Q3) — one-shot spelling is `compose up --mux`

**Choice:** `cmdman compose up --mux`.

**Rationale:** minimal spelling; `runComposeUp` already holds the parsed
spec, so it is one flag plus a short tail — no re-load, no new verb to
learn.

**Rejected:** `mux up` implying `up` (surprising side effect for an
existing spelling); a dedicated verb (unwarranted while the launcher
covers the "from anywhere" case).

### D6 (2026-08-09, Q4 + Q22 + Q30) — switching navigates per-project windows

**Choice:** selecting a project in the switcher navigates between
per-project tmux windows; there is no single dashboard window whose main
region re-renders.

**Rationale:** keeps tmux-native window navigation (window list,
`C-b n`/`p`) as a parallel path, avoids fighting tmux's model, and keeps
each project's pane state alive in its own window.

**Consequences:** Q22 resolves to per-window semantics everywhere —
Track A's landing ("focus lands in the project's window") holds
unchanged, frame or no frame. Q30 is moot — nothing is swapped out.
New question 34: where the chrome lives (duplicated per window vs one
"home" dashboard window). The muxctl first-class-frame revision (D1) may
shrink substantially if Q34 lands on a home window, since a dedicated
frame window has no identity-coexistence problem.

**Rejected:** in-place swap (single-application feel, but forces the
full muxctl ownership revision with no spike shortcut and hides tmux's
native navigation); "windows now, swap later" (chose to commit rather
than keep both semantics alive).

### D7 (2026-08-09, Q9) — one recency-sorted jump list; docked forms running-only

**Choice:** the launcher shows a single merged list sorted by recency;
running entries carry state/attention badges instead of being grouped.
Docked forms list running projects only; cold launch belongs to the
full-sized launcher.

**Rationale:** one mental model — "type the project, hit Enter" — with
no mode distinction between "jump" and "launch"; badges carry the
running/attention signal without disturbing muscle-memory ordering.

**Rejected:** running-first grouping and attention-first sorting (both
make positions jump around as state changes, hurting few-letter muscle
memory).

### D8 (2026-08-09, Q21) — outside tmux: auto-create-or-attach

**Choice:** with no attached client (or none on the intended session),
cmdman creates or picks the session, creates the window, and
attaches/switches the client.

**Rationale:** the landing promise ("you are looking at X") holds from a
bare shell too; anything less leaves manual steps alive, which is the
pain being solved.

**Rejected:** degrade to plain `up` (silently weaker promise); error out
(adds a manual step back).

### D9 (2026-08-09, Q28) — mux-less projects get a synthesized window + warning

**Choice:** launching a project whose compose file has no `mux` section
does plain `up`, then creates and focuses a one-pane shell window at the
project workdir, emitting a warning that the project is up but no mux
section was found.

**Rationale:** the landing promise holds for every project, and the
warning explains why the window is bare instead of leaving the user
wondering whether something failed.

**Rejected:** up + notice without a window (breaks the landing promise
for these projects); excluding mux-less projects from the launcher
(surprising gaps in the list).

### D10 (2026-08-09, Q29) — land immediately; failures raise attention

**Choice:** the launcher dismisses on Enter; focus switches as soon as
the window exists and bring-up is watched in place. Partial failures
additionally raise the project's attention state (Track C bell/badge) so
they are noticed even after switching away. Errors that prevent landing
at all (e.g. stale-history resolution failure, before any window
exists) surface in the launcher with removal offered.

**Rationale:** landing fast keeps the launcher feeling instant; the
attention system is the natural home for "something needs you here",
and it works uniformly whether or not you stayed to watch.

**Consequence:** launch-failure attention depends on Track C's badge
surface; until phase 2 lands, phase 1 ships with the in-place error
output only.

**Rejected:** progress-in-launcher (blocks the instant feel and holds
the popup hostage to slow bring-ups); land + no notification (failures
missed when multitasking).

### D11 (2026-08-09, Q7) — bells clear on attach/preview; per-project aggregation

**Choice:** attaching or previewing a command clears its bell; the
switcher shows a project as unread while any of its commands is unread.

**Rationale:** "read" should mean "I actually looked at that command",
which attach/preview is; no extra dismiss chore.

**Rejected:** project-focus clears (too coarse — glancing at the window
clears bells for panes never looked at); explicit dismiss (a chore that
trains users to ignore the badge).

### D12 (2026-08-09, Q10) — status is enum + optional detail, shown everywhere

**Choice:** the report vocabulary is a small enum
(`working`/`waiting`/`done`) rendered as consistent colored badges, plus
an optional free-form detail string shown in `ls` and TUI rows. Surfaces:
`ls` column, TUI rows, switcher badges.

**Rationale:** the enum makes badges and aggregation (D14) well-defined;
detail keeps expressiveness without costing consistency.

**Rejected:** enum-only (loses cheap expressiveness); free-form
(un-aggregatable, inconsistent badges).

### D13 (2026-08-09, Q25) — reported status dies with the run

**Choice:** like titles, reported status resets on restart; an exited
command shows its exit state, never its last report.

**Rationale:** a stale `working` on a dead process is worse than no
status; exit state is the truthful post-mortem signal.

**Rejected:** persist-until-re-reported (invites stale lies; anything
worth keeping after exit belongs in the exit state or logs).

### D14 (2026-08-09, Q26) — project badge: attention wins

**Choice:** the per-project badge shows the most attention-worthy signal
among its commands: unread bell outranks all, then
waiting > working > done.

**Rationale:** the badge's one job is answering "which project needs
me"; the neediest command is the project's state.

**Rejected:** counts (busier rows; the full picture lives one level
down anyway); most-recent-wins (a late `done` can mask a `waiting`).

### D15 (2026-08-09, Q34 + Q5 + Q24 + Q14) — the frame is a simple standalone feature

**Choice:** reframe instead of choosing among chrome placements: on the
cmdman side the frame is *just a frame feature*. Frame definitions are
named standalone files under `<config-dir>/frame/<name>.yaml`, exactly
like compose defs; which def applies is passed via config or a command
flag. The frame is switched **shown / hidden / selected / cycled** by
explicit user control. Built-in components exist for defs to reference.

**Rationale:** the home-window-vs-per-window framing was over-complicated
for the value it adds; a user-controlled show/hide/cycle fixture with
file-based defs matches the compose-def mental model already in the
product.

**Consequences:** Q5 (appearance) → explicit control, no lazy
appearance. Q24 (scope) → dissolved; a frame is per explicit invocation,
switchers list projects globally (confirm on mock). Q14 (spec location)
→ `<config-dir>/frame/<name>.yaml`. The physical pane-ownership question
(what the muxctl driver must support to show/hide a frame around a
project) moves entirely to implementation question 13, and is to be
validated after reviewing a mock TUI.

**Rejected:** one "home" dashboard window as *the* model; chrome
duplicated into every project window; tmux status-line integration as a
required piece (all still possible later as ways to *use* the feature,
not as its definition).

### D16 (2026-08-09, Q6) — no default frame; switcher/statusbar are built-ins

**Choice:** cmdman ships no default frame content. `switcher` and
`statusbar` ship as built-in components that user defs reference. The
shown/hidden switch doubles as the small-terminal collapse gesture. A
mock TUI is built for the user to review frame usability (sizes, badges,
cycling, interaction) before the spec freezes.

**Rationale:** with defs this cheap to write, a default is presumption;
built-ins keep few-letter ergonomics without dictating layout.

**Rejected:** switcher-only default frame; switcher+statusbar default
(both pre-empt a decision the def file states in three lines).

### D17 (2026-08-09, Q8) — per-command OSC hook system in the base config

**Choice:** instead of picking badges-vs-relay, cmdman grows an OSC hook
system: trapped OSC sequences dispatch a configured command per hook.
Built-in default is passthrough. Built-in hooks ship so common behaviors
(badge, desktop notify) need no shelling-out or user definition. Hook
configuration is **per command** and lives in persistent base-system
configuration — an addition to the base system, not a compose-style
extension. A frame definition can override the default hooks.

**Rationale:** bells, titles, and OSC 9/777 are all the same shape —
"sequence arrives, something should happen" — and users differ on what
that something is; a hook point with good built-ins covers badge-only,
relay, and custom dispatch without three features.

**Consequences:** new persistent config surface (implementation
question 35: schema, built-in vocabulary, dispatch execution model,
frame-override composition). Track C's capture (vt callbacks) becomes
the producer feeding hook dispatch.

**Rejected:** badges only (leaves relay users stuck); hardwired relay
(opt-in or default — bakes one policy in where a hook generalizes).

### D18 (2026-08-09, Q23) — launcher rows are git-aware `path (project)`

**Choice:** a launcher row reads `path (project)` — path first, because
multiple projects can run on one path. For a git workdir the path
renders as **repository name + branch name**; otherwise the dir
basename. The fuzzy match runs over full path, git repository uri,
branch name, and project name.

**Rationale:** the user's mental key for "which one" is repo + branch,
not directory basenames; matching on the uri/branch lets few letters of
either narrow the list.

**Consequences:** the launcher needs git info per entry (implementation
question 36: read `.git` directly vs exec git; caching).

**Rejected:** name-first display (collides across worktrees/branches);
name+full-path (noisy rows, not branch-aware).

### D19 (2026-08-09, Q32) — frame `command:` entries: ephemeral, `managed: true` opt-in

**Choice:** a raw `command:` frame entry is an ephemeral pane by
default — dies on hide/close, restarts on show. Setting `managed: true`
on the entry makes it a cmdman-managed (supervised) command that
survives the frame.

**Rationale:** matches tmux intuition for throwaway widgets (btop, a
clock) while making persistence a one-line, per-entry choice.

**Consequences:** informs implementation question 16 (what remains
there: referencing an *existing* named command).

**Rejected:** always-supervised (monitor overhead and lifecycle
surprise for throwaway widgets).

### D20 (2026-08-09, Q33) — switcher lists titles grouped by project, bucket-sorted

**Choice:** no single representative title per project. The switcher's
column form is a grouped list: groups keyed by project path, each
command's title listed under its group — every pane's state visible
from the side bar, like terminal tab titles but for all panes at once.
Ordering is by title-update time **bucketed into ~5 s chunks** (interval
tunable), then by name-or-id within a bucket, so two agents retitling
every few seconds don't race each other into list churn.

**Rationale:** the driving use case is watching LLM agents' titles;
collapsing to one title per project throws away exactly the signal
wanted. Bucketing keeps "recently active floats up" without constant
reordering.

**Consequences:** the switcher widget is a tree/grouped list, not flat
rows; the frame mock demonstrates the grouped form. The shallow
bottom-row form still shows one aggregated badge per project (D14) —
grouping needs vertical space.

**Rejected:** most-recent title wins (loses per-pane visibility);
attention-command's title (same loss); strict most-recent sorting
without buckets (list churn under racing agents).

### D21 (2026-08-09, mock review round 1) — project dot markers; whole-group selection highlight

**Choice:** from the user's first review of the frame mock:

- The project-level signal is a single **colored dot**: green = idle,
  yellow = working, red = blocked. The separate red unread-bell dot did
  not read ("what's the red dot?") and is gone as a distinct marker —
  an unread bell now renders as **blocked** (red), which matches its
  meaning ("this needs you") and preserves D14's ranking: bell or
  waiting → red, else working → yellow, else idle/done → green.
- The selected group in the switcher is highlighted as a **whole
  background block** (head line + its command rows), not only the head.
- Per-command status words share the same palette (waiting red,
  working yellow, done green), and the switcher carries a dot legend so
  the vocabulary is self-explanatory.

**Rationale:** one dot per project is glanceable and the color
vocabulary (traffic-light) needs no learning; a marker whose meaning
had to be asked about failed its only job.

**Refines:** D14 (rendering only; the aggregation rule stands) and
D11 (bell semantics stand; only the bell's visual changed). The mock is
updated accordingly.

**Rejected:** keeping a separate bell dot next to the status dot
(illegible in review); highlighting only the group head (looked like a
one-line cursor, not a group selection).

### D22 (2026-08-09, mock review round 2) — bell is a distinct 🔔; clears on project selection

**Choice:** the unread bell renders as a **🔔 marker** next to the
project's dot — not folded into the red/blocked dot (partially reverting
D21: the *red dot* as bell marker was illegible; an explicit 🔔 is not).
The dot now reflects reported status only: waiting → red (blocked),
working → yellow, else green (idle). Clear semantics at the selector
level: the bell **resolves when the project is selected through the
selector app**, and **resolves immediately** if the project is already
the selected/focused one when the bell arrives — you were already
looking at it.

**Refines:** D21 (un-folds bell from the dot); D14 (bell no longer
competes in the dot's ranking — it is a parallel marker); D11 (the
switcher-level clear trigger is project selection; immediate resolve
when already focused).

**Rejected:** bell-as-red-dot (D21's folding — ambiguous with
status-blocked); bell persisting on the focused project (a marker for
"go look" is noise when you are already there).

### D23 (2026-08-09, mock review round 3) — 🔔 replaces the dot until checked

**Choice:** the 🔔 is not rendered *beside* the status dot — it
**replaces** the dot while the project has an unread bell. Once checked
(resolved by selecting the project, per D22's rules, including the
immediate-resolve case), the status dot reappears. One marker slot per
project: 🔔 when unread, colored dot otherwise.

**Rationale:** one slot, one signal — the bell is the more urgent of
the two, and the status dot returns the moment the bell is dealt with;
two side-by-side markers doubled the vocabulary for no extra
information at a glance.

**Refines:** D22 (marker placement only; the clear semantics stand
unchanged).

**Rejected:** dot + 🔔 side by side (D22's initial rendering — busier
and wider rows without saying more).

### D24 (2026-08-09, mock review round 4) — marker margin, unknown-green, mouse, focused styling

**Choice:** four switcher refinements from the user's fourth mock
review:

- One space of margin to the right of the marker (dot / 🔔) before the
  name.
- The **green circle also expresses "unknown"** — a command with no
  status update available renders a green ● (replacing the dim `-` in
  command rows); a project whose commands have reported nothing shows
  the green dot. Legend updated accordingly.
- **Mouse support** on switcher entries: clicking a project selects it
  (select + focus + bell resolve, same as Enter). This is the first
  concrete input on open question 31 — mouse click is an expected
  interaction mode for the docked forms.
- The `*` marker on the focused project is **removed** — selection is
  already colored. The **focused-but-not-selected** project instead
  gets a thinner/weaker highlight, clearly subordinate to the
  selection's strong background block.

**Rationale:** margin and the unknown-green keep the marker column
legible and total (every command has a circle-or-word state); mouse
matches how a docked strip will actually be poked at; a text `*` was
redundant next to color and invisible next to it.

**Refines:** D21/D23 (marker rendering); partially informs Q31
(mouse). Mock updated by the opus implementer.

**Amended (2026-08-09, round 5/6):** the margin space belongs on the
**left** of the marker (user's own correction — "right" in round 4 was
a slip), i.e. one leading space before the dot/🔔; the marker-to-name
spacing returns to its pre-round-4 width. And: "unknown" is a green
**hollow circle ○** (empty body), not the filled ● — the first
rendering misread the request. Filled green ● = idle/done; hollow green ○ = unknown (no
status reported). Same color, different glyph: the shape carries the
reported-vs-not distinction the color alone could not.

### D25 (2026-08-09, mock review round 5) — the switcher list is scrollable

**Choice:** the switcher's grouped list scrolls when its content
exceeds the pane height: the viewport follows the cursor (selection
always visible), and the mouse wheel scrolls the list. Truncating at
the pane edge is not acceptable — a docked strip must handle more
projects than fit.

**Rationale:** the docked forms trade size for permanence; without
scrolling, that trade caps how many projects the feature can serve.

**Refines:** D20 (grouped list gains a viewport). Applies to the real
switcher widget as a requirement, demonstrated in the mock.

### D26 (2026-08-09, mock review round 7) — tighter marker spacing; app rows in detected-weak color

**Choice:**

- One less space between the marker (dot / 🔔) and the project name.
- The command ("app") rows under a project name render in a **weaker
  color** than the head, so groups read as head-plus-detail.
- The weak shade is **derived from the terminal's detected letter
  (foreground) color** — queried at startup, blended toward the
  background — not a hardcoded 256-color grey, so it stays visible on
  light and dark terminals alike.

**Rationale:** heads carry the glanceable signal, apps are detail;
hardcoded greys are invisible on the wrong background, and terminals
can report their palette, so use it.

**Refines:** D20/D24 rendering. Applies to the real switcher widget;
demonstrated in the mock.

### D27 (2026-08-09, launcher mock review) — the launcher fills its window

**Choice:** the launcher TUI renders **edge-to-edge in whatever window
it runs in**. The popup framing (small centered window, border) is the
multiplexer's job — `tmux display-popup` creates that window and the
launcher receives its dimensions as the whole terminal. The launcher
never draws its own centered box inside a larger screen.

**Rationale:** that is how a tmux popup actually delivers a program;
a self-drawn inner box would waste popup space and double the borders.

**Refines:** D3 (the popup gesture's delivery model). The launcher
mock is corrected to fill its window; to preview popup proportions,
run it in a small terminal or an actual `tmux display-popup`.

### D28 (2026-08-09, launcher mock review round 2) — two-pane launcher: locations left, projects right

**Choice:** the launcher separates its two selection targets — **target
location** and **compose project** — into two side-by-side sections,
because one location may hold several local compose files and a flat
`path (project)` row conflates the choices:

- **Left**: fuzzy-found location list over an input line with **tab
  auto-completion** of paths. Empty input shows **history**.
- **Right**: the projects (known + local compose files) at the
  location under the left cursor. Projects are **toggled** on/off;
  entries that come from history are **enabled by default**, so the
  empty-input common case is just `s`/`S`.
- **Enter** on the left moves focus into the right section; from the
  right you can leave back left, or act.
- **`s`** starts the enabled project(s) and leaves (background start).
- **`S`** on either section jumps into the selected path and attaches
  to the mux project (launch + focus).

**Key-collision rule** ~~(to validate in the mock): on the left pane,
`s`/`S` act only while the input is empty (history mode) — otherwise
they type into the filter; on the right pane they always act.~~
**Amended (2026-08-10, round 3)** — the empty-input rule failed
validation: history mode is the launcher's opening state, and the
first keystroke of natural queries (`src`, `staging`…) silently
started projects. Replaced by a **three-zone focus model**: the input
text area is its own focus zone — typing only ever edits the filter
there. **Enter** steps input → left list → right list. On a *list*,
bare keys are unambiguous: `s`/`S` act, navigation keys move. Keys
exist to step back toward the input and to erase the input from
anywhere. The lists still filter live while typing in the input.

**Rationale:** location and project are different questions, asked in
that order; history-as-default makes the everyday case two keystrokes.

**Revises:** the launcher-side key spelling of D4 (Enter no longer
launches — `S` is launch+focus, `s` is start-only; the two-ending
semantics of D4 stand) and the flat-list presentation of the first
launcher mock (D7's recency ordering now governs the left pane's
history/location list). D18's git-aware rendering/matching applies to
the left pane rows.

**Rejected:** flat `path (project)` rows (conflates the two choices);
modal two-stage drill-down and grouped single list (offered options —
the user specified the two-pane form instead).

### D29 (2026-08-10, launcher mock review round 4) — no blocking start view; progress circle in the marker slot

**Choice:** starting projects (especially a bulk `s` over several
toggled entries) must not open a blocking progress view — the
alt-screen "started" screen is removed. Instead the affected entries'
**marker slot shows a progress circle icon** while bring-up is in
flight, transitioning to the normal status dot when the project is up.
The launcher itself stays responsive (and in real use dismisses per
D28's "start and leave" — the marker state is what you see if you
summon it again, and what the switcher shows ambiently).

**Rationale:** `s` means "get it warming up while I keep working" —
any modal progress display re-blocks exactly what the gesture exists
to avoid; the marker vocabulary already carries per-project state, so
in-flight is just one more marker.

**Refines:** D28 (`s` feedback), D21/D24 marker vocabulary (adds a
"starting/in-progress" marker). The same marker applies wherever
markers show (switcher, launcher).

### D30 (2026-08-10, Q27) — percent of the remaining rectangle, confirmed

**Choice:** `N%` in a frame entry resolves against the rectangle
remaining at that entry's turn; entry order is the nesting. Confirmed
on the mock by comparing the `side+statusbar` and `statusbar-first`
defs. IDEA.md's spec text reworded; both rules pinned together.

**Rejected:** percent of the whole screen (needs conversion before
carving, can over-allocate); leaving Q27 open.

### D31 (2026-08-10, Q31) — docked-strip interaction settled; strip-reach deferred

**Choice:** the in-strip interaction model is what the mock rounds
converged on: mouse click selects (column) / toggles (where toggles
exist), wheel scrolls without moving the cursor, and the D24/D28/D29
key/zone vocabulary applies inside the strip. The remaining sliver —
how keyboard focus *reaches* a docked strip in real tmux (normal pane
navigation vs a dedicated binding) — is **deferred by the user**: to be
planned and implemented later, with phase 3.

**Confirmed in the same review round** (launcher, no separate
entries): esc in the input zone now clears the query first and quits
on the next press (revising D28's chain end); the dual weak cursor
while the input is focused stays; the `repo(branch) (project)`
double-paren row format stays; the blank marker slot for cold entries
stays; `s` skipping already-running enabled projects stays. Contrast
items (grey-237 focus block, weak-on-grey rows, faint titles,
bottom-row clipping) reviewed and accepted as-is.

### D32 (2026-08-10, Q17) — runtime state lives in the monitor, served as a stream

**Choice:** Track C's runtime state (title, reported status,
bell-unread) is held **in-memory in the monitor** — no store writes —
and served over the monitor's gRPC as a **server-streaming RPC**
(subscribe → initial snapshot + push on change), alongside an extended
one-shot `Status`. Consumers: the TUI/switcher/launcher subscribe to
streams; `ls`/`ps` gain a bounded parallel one-shot dial across
commands with a `SocketPath` (short timeout) to fill their columns.
Title debouncing happens monitor-side before broadcast. Status dying
with the run (D13) falls out for free.

**Rationale (user's call):** the monitor is the single truthful owner
of per-run state; a stream is exactly what live consumers (switcher
badges, title updates) want, with no debounced store churn.

**Consequences:** the eventlog-bell and CommandState-blob options from
NOTES.md are dropped; `ls` accepts the dial loop it didn't have; bell
clear-on-attach (D11) becomes monitor-side state transitions on the
stream.

**Rejected:** CommandState JSON blob composite (store writes per title
change even debounced); eventlog bell events (second write path).

### D33 (2026-08-10, Q20) — `cmdman [compose] status set|get|delete`

**Choice:** the report verb is a **noun family**:
`cmdman status set <status> [--detail …]`, `status get`,
`status delete` (clear) — with a compose-scoped mirror
(`cmdman compose status …`) for project-level queries. Identity from
`CMDMAN_CMD_ID`; transport is the **monitor socket**, consistent with
D32 (the monitor owns the state the verb mutates).

**Rejected:** flat `cmdman report` (no room for get/clear); store
transport (would split state ownership decided in D32).

### D34 (2026-08-10, Q11) — history written by compose.Service at Create

**Choice:** ComposeHistory rows are written service-side (a new
exported method on `*cmdman.Service` plus a `cmdmanSvc` interface
addition) from **`compose.Service.Create`** — the point where project
commands come into existence — so every entry path (CLI, TUI,
launcher, create-without-start) records automatically.

**Nuance flagged:** an `up` on an unchanged project performs no
Create, so `LastUsed` recency would not bump on re-up unless the
upsert is *also* touched from the up/start path. Recorded intent:
row existence from Create; `LastUsed` bump on every up. To confirm
during implementation.

**Rejected:** recording at CLI/TUI call sites (every new caller must
remember).

### D35 (2026-08-10, Q18) — capture stays TTY-only

**Choice:** BEL/title capture applies to `Tty` commands only; piped
commands keep zero parsing overhead. Reported status (D33) works for
everything regardless.

**Rejected:** scanning pipe output for BEL/OSC (cost on every piped
byte for a rare case).

### D36 (2026-08-10, Q13) — straight to the first-class frame

**Choice:** no compile-in spike. The muxctl contract revision —
subtree-scoped apply, `@cmdman_frame` pane stamps, identity
coexistence, `resetWindow`/`Detach` sparing frame panes — is its own
sub-plan (per D1), and the frame feature builds on it. Phase 3 blocks
on that sub-plan from step 15 on.

**Rejected:** compile-in spike (frame panes die on every layout cycle
— cannot demonstrate D15's lifecycle, so it validates nothing the
mocks haven't); frame-as-separate-window (abandons
chrome-around-the-project).

### D37 (2026-08-10, Q15) — widget entrypoint: `cmdman tui widget <name>`

**Choice:** `tui` becomes a command group: bare `cmdman tui` runs the
full TUI; `cmdman tui widget <name>` runs a single widget, with each
widget name a **subcommand** (`cmdman tui widget switcher`, `… widget
statusbar`). `component: <name>` in a frame def resolves to exactly
this invocation.

**Rejected:** `--widget` flag (subcommands give per-widget help/flags);
hidden `__widget` (widgets are debuggable by hand); top-level
`cmdman widget` (crowds the root).

### D38 (2026-08-10, Q16) — no by-name frame entries

**Choice:** the frame entry union stays `component:` | `command:`
(+ `managed:`, D19). Attaching an existing supervised command is
spelled `command: ["cmdman", "attach", "<name>"]`.

**Rejected:** a third `ref:` key (a second name-resolution path to
maintain for something argv already expresses).

### D39 (2026-08-10, Q19) — first-pass OSC capture: BEL, title, 9/777

**Choice:** the monitor's first pass registers BEL, title (OSC 0/2),
and desktop-notification sequences (OSC 9 / OSC 777) — the latter so
D17's hook system has real notification events to dispatch from day
one. OSC 133 / 7 / 9;4 / 99 come later via `RegisterOscHandler` (all
noted in NOTES.md Q19).

**Rejected:** BEL+title only (hook system launches with nothing to
relay); kitchen-sink first pass (most surface to get right at once).

### D40 (2026-08-10, Q35) — OSC hook config: shape, dispatch, and a leaner built-in set

**Choice:**

- **Location**: a `Hooks` field on `model.CommandConfig` (per-command)
  plus a defaults map in the global config. Precedence: frame-def
  override > per-command > global default > built-in default.
- **Dispatch**: the vt callback only latches; a separate goroutine
  execs a configured hook argv fire-and-forget with event data in env
  vars (`CMDMAN_HOOK_EVENT`, `CMDMAN_HOOK_TITLE`, … alongside the
  existing `CMDMAN_CMD_ID` family). Per-command serialization,
  drop-if-busy on floods. Never blocks the emulator-lock path.
- **Built-ins reframed (user's insight)**: only **`passthrough`**
  (default — the sequence flows to the attached viewer) and
  **`block`** (swallow it). Badging is *not* a hook: with D32 the
  monitor always latches bell/title/notification state internally and
  serves it over RPC — no configuration needed for state to exist.
  Desktop notify is a user-defined argv hook, not a built-in.

**Rationale:** the hook layer only decides what happens to the *byte
stream* (pass or block) and what external command to run; state
capture is the monitor's unconditional job.

**Rejected:** `badge`/`notify` as built-in hooks (badge duplicates
D32's always-on latching; notify is one argv line for whoever wants
it); global-only or per-command-only config; stdin-JSON payloads
(env vars suffice for v1).

### D41 (2026-08-10, Q36) — launcher git info via exec git

**Choice:** the launcher shells out to `git` per entry at open
(`git -C <dir> …` for branch, toplevel/repo name, remote uri) —
always correct, including worktrees and exotic setups.

**Rejected:** parsing `.git` directly (fast but re-implements gitdir
indirection and edge cases); go-git (heavy dependency for three
fields).

### D42 (2026-08-14, D28 correction) — tab completion really completes paths

**Choice:** the shipped launcher had narrowed D28's "tab
auto-completion of paths" to the common prefix of the *known
locations* — a mock-stage drift: the launcher mock ran on fixture
locations where the two readings were indistinguishable, and its
narrowed `complete()` was promoted verbatim. Corrected, with
the user picking the full scope over expansion-only:

- The input expands a leading `~` / `$HOME` (path-component boundary,
  the exact inverse of the view's home abbreviation) for matching and
  path operations; the typed spelling is preserved, including in
  completion write-backs.
- For path-shaped input (`/`, `~`, `$HOME`, `./`, `../`), tab
  completes over the union of matched known-location dirs and on-disk
  directories extending the typed path (one ReadDir of the parent;
  dot-dirs only when the typed base starts with `.`; a completion
  ending exactly at a directory gains the trailing separator).
  Non-path input keeps the location-prefix behavior unchanged.
- A typed existing directory that is not a known location resolves
  into a selectable left-pane row via the new
  `Backend.ResolveLaunchDir` (debounced, generation-guarded; the
  listing's richer row wins on dedup; not part of history mode).

**Rationale:** filesystem completion is only useful if the completed
path can be acted on; without the resolved row it dead-ends on an
empty pane. The resolved row reuses the listing's per-directory
build, so the row and the eventual launch agree on WorkDir.

**Refines:** D28 (restores its literal completion wording); D7
(history mode is unaffected — the resolved row never joins it).

**Rejected:** expansion + fs completion without the typed-path row
(offered as the minimal option; dead-ends exactly where D28 matters);
completing against the filesystem for non-path-shaped queries
(fuzzy words are project/repo searches, not paths).

### D43 (2026-08-14, D42 follow-up) — completion suggestion list with menu cycling [automatic]

**Choice:** tab gets zsh's other half — what the completion cannot
decide, it shows and lets you choose. The user confirmed the scope
(menu cycling, and path-shaped input only); the corners below marked
`[automatic]` were settled while implementing.

- Tab extends to the union common prefix as before (D42). Two or
  more candidates surviving that leave a suggestion list open under
  the input line; nothing left to extend puts the first candidate in
  the input, each further tab the next, wrapping, with `shift+tab`
  walking back. An insertion keeps the typed home spelling and takes
  the trailing separator D42 withholds from an *extension* — picking
  a directory off the list is the request to go inside it, so the
  sibling it cuts out of the search is one the user just passed over.
- Non-path input is strictly unchanged: no list, no cycling, and the
  same note when there is no prefix past what is typed.
- The candidate set is D42's union (on-disk dirs extending the typed
  path + matched known-location dirs), fixed when the menu opens: a
  set recomputed from an insertion would be the directories *inside*
  the proposal rather than the ones it is one of. Cycling edits the
  query like any keystroke, so the debounced `ResolveLaunchDir` still
  turns the chosen directory into a left-pane row.
- Dismissal: esc while the list is up drops it and puts back the text
  the cycling started from, spending that esc ahead of D31's chain;
  enter accepts the insertion in place, leaving the zone step to the
  next enter; typing/backspace keep the insertion and drop the list;
  leaving the input zone (enter, a click on a pane) drops it too,
  since tab is an input-zone key. The arrows stay pane navigation —
  cycling is tab/shift+tab alone. [automatic]
- Presentation: each candidate shows as the part past the directory
  they share (the last component, in the usual case) in a row-major
  grid, the inserted one carrying the pane cursor's BgAccent block;
  the list is capped at 6 lines with a `+N more` tail and spends the
  panes' row budget rather than the bottom of the window (D27).
  [automatic]

**Rationale:** D42 made tab walk the filesystem, which is where a
prefix stops being enough: the useful half of a picker is seeing the
handful of directories the prefix left and choosing one, not typing
the letter that distinguishes them. Cycling reuses the input as the
selection — there is nothing new to focus, so the zones (D28) are
untouched.

**Refines:** D42 (the common-prefix rule and `endsAtDir`'s
sibling-preserving separator are unchanged; the menu is what makes
the "left alone, nothing to extend" outcome actionable).

**Rejected:** up/down cycling the list (they are the panes' keys, and
arrowing to a row while typing is D28's fzf reflex — one key cannot
mean both); enter accepting *and* advancing a zone (a mis-chosen
candidate would land the cursor on a row nobody asked for); a list
for fuzzy queries (no tree to walk — D42's rejection stands); a list
that scrolls or overflows the panes (the panes are what the launcher
is for).
