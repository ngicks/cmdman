# Decision log

One entry per material decision: the choice, the rationale, the rejected
alternatives. Entries are numbered **F1, F2, …** (frame-driver) so they
never collide with the parent plan's D-numbers, which this file cites.

All ten local questions were resolved with the user on 2026-08-12 —
see **Decided** below. The inherited section records decisions this plan
is **downstream of**; they are context, not this plan's to revisit.

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

All resolved 2026-08-12; each maps to its F-entry below.

- [x] Q1 API shape → **F1**
- [x] Q2 second identity's home + enumeration → **F2**
- [x] Q3 which side owns `@cmdman_window` → **F3**
- [x] Q4 marker semantics on a framed window → **F4**
- [x] Q5 focus policy owner → **F5**
- [x] Q6 main region before any project exists → **F6**
- [x] Q7 frame pane lifecycle on hide/cycle → **F7**
- [x] Q8 teardown when neither side is left → **F8**
- [x] Q9 driver-neutral contract vs tmux-scoped → **F9**
- [x] Q10 pane-name namespace → **F10**

## Decided

All entries resolved with the user on 2026-08-12.

### F1 — `ApplyLayout` is revised in place; no additive sibling

**Choice.** `ApplyLayout` itself becomes frame-aware: it rebuilds only the
project region, sparing frame-stamped panes in `resetWindow`
(`pkg/muxctl/tmux/apply.go:158-176`), the quiesce sweep, and focus
selection. On a window with no frame panes the revised semantics
degenerate to today's documented whole-window reset, so existing callers
observe no change. The frame side gets its own new operations
(`ShowFrame` / `HideFrame`, pinned in PLAN.md "Pinned contracts").

**Rationale (user's call — the one answer that departed from the drafted
tentative default).** One apply entry point: no risk of two parallel apply
paths drifting, and no consumer ever has to choose the right variant. The
documented reset contract (`session.go:21`) is rewritten rather than
preserved-by-sibling.

**Rejected.** An additive `ApplyLayoutAt(ctx, anchorPaneID, root, marker)`
sibling in the style of `RespawnLeaf` (`session.go:67-73`) — kept the old
docs intact but doubled the apply surface.

*Noted routine call (mine, not user-asked):* `ShowFrame` designates the
main region by leaf name — the consumer passes a placeholder leaf to
`frame.Spec.Carve` and hands its name to `ShowFrame` — reusing the
existing leaf-name vocabulary instead of inventing a sentinel type.

*Amendment (implementation, 2026-08-12).* The realized signature is
`ShowFrame(ctx, root PaneSpec, mainName, defName string) error` — one
parameter more than pinned. F2 requires `ShowFrame` itself to write the
shown def's name into `@cmdman_frame_def`, and the three-arg form had
nowhere to carry it; a side-channel write by the consumer would have
split the atomic "show = panes + record" contract. Empty `mainName` or
`defName` is an error (an empty def name would unset the state and leave
a framed window claiming to be unframed).

### F2 — Frame identity lives in a `StateKey`-backed window option

**Choice.** The shown frame def's name is stored in a window option
through the existing `StateKey` mechanism (`pkg/muxctl/driver.go:38-59`,
tmux mapping `scale_state.go:19-21`; pinned spelling `@cmdman_frame_def`),
fetched inline by `ListWindows` (`list.go:40-43`) and surfaced as an
explicit second field on `muxctl.Window` (`driver.go:67-91`); identity
filtering can match either slot. Pane membership is a separate per-pane
`@cmdman_frame` stamp (R3).

**Rationale.** Extends vocabulary that already exists at exactly this
layer; a window-level record survives states where pane scans cannot
answer, and costs no extra round-trips in enumeration.

**Rejected.** Pane-level stamps only (per-window scans; a frame with no
panes has no record); a dedicated new mechanism (duplicates `StateKey`).

*Amendment (implementation, 2026-08-12).* "Identity filtering can match
either slot" is realized as the enumeration *gate* accepting either slot
— a window carrying only a frame def still enumerates — while
`ListOptions.Identity` keeps matching the ownership slot exactly. Both
pinned queries stay answerable ("windows of project P" = the filter;
"framed windows" = unfiltered scan + `Window.Frame`), and a destructive
`Down --identity dev` can never match a window merely *framed* "dev". A
literal either-slot `Identity` match was rejected in implementation: it
would aim teardown at frame-named windows and still could not answer
"framed windows" without knowing a def name up front.

### F3 — The project keeps `@cmdman_window`; the frame takes the second home

**Choice.** On a framed window the owner slot (`pkg/muxctl/tmux/tmux.go:19`)
holds the **project** identity, unchanged; the frame's record moves to
F2's window option.

**Rationale.** Every identity-matching consumer keeps working unchanged —
`Down` (`pkg/cmdman/mux/down.go:105-108`), `List`
(`pkg/cmdman/mux/list.go:80-84`), `CycleScale`
(`pkg/cmdman/mux/cycle_scale.go:94-98`) — and the window-takeover hazard
(R6) largely evaporates because a framed window's owner slot still holds
the project's own identity (`reuse.go:52-56`).

**Rejected.** The parent NOTES.md sketch (frame owns the slot, project
moves): would have forced a revision of every consumer above and made the
takeover hazard acute. D1/D36 commit to the feature, not to slot
assignment, so this is not a departure from a parent decision.

### F4 — Frame panes are excluded from marker consistency

**Choice.** `StatWindow`'s consistency scan (`pkg/muxctl/tmux/stat.go:50-57`)
skips frame-stamped panes; only project panes vote on the marker. Frame
panes never carry `@cmdman_marker` or `@cmdman_leaf`.

**Rationale.** Smallest change: the marker stays a pane-level property
exactly as today, just scoped; cycling (`pkg/cmdman/mux/run.go:151-161`)
and cycle-scale's marker gate keep working. Giving frame panes a marker
would re-expose them to the marker-keyed quiesce sweep (R2).

**Rejected.** A window-level marker like `@cmdman_scale` (revises the
whole stamp/read path and voids the existing consistency check); a
separate frame-marker option (the frame has no cycling concept to need
it).

### F5 — The driver owns focus policy

**Choice.** Frame-stamped panes are never focus candidates; the driver
enforces this in the focus-selection path around `PickFocus`
(`pkg/muxctl/tmux/apply.go:62-69`, `pkg/muxctl/layout.go:78-108`).

**Rationale.** The guarantee lives where the stamp lives; no consumer can
get it wrong, today or in a future caller.

**Rejected.** Consumer-set `Leaf.Focus` on the main subtree (every future
caller must remember it); belt-and-braces both (redundant).

### F6 — Empty main region = the driver's default pane

**Choice.** Showing a frame on a window with no project realizes the main
region as the driver's **default pane** — whatever the driver respawns by
default (tmux precedent: `Detach`'s respawn, `detach.go:53-61`) — replaced
by the first project apply. The contract deliberately does **not** say
"shell" (user's phrasing: "default pane but no shell emphasis").

**Rationale.** Show-before-launch ("chrome is the fixture, projects
arrive later", parent IDEA.md) just works, with no new spec surface; what
the default pane runs stays the driver's business.

**Rejected.** "Frames require a project" (kills the use case); a
first-class empty-region spec concept (new surface for one case).

### F7 — The driver treats all frame panes alike on hide/cycle

**Choice.** `HideFrame` kills frame-stamped panes uniformly. Managed-ness
(D19's `managed: true`) is entirely the consumer's concern above the
driver — the frame verbs detach/preserve a managed viewer before asking
the driver to remove its pane.

**Rationale.** Keeps the driver policy-free; D19 is a cmdman-level
decision and stays above the driver seam.

**Rejected.** A per-pane "managed" flag with driver-side
`ViewerDetachKeys` quiesce before kill (pushes cmdman policy below the
seam).

### F8 — Full restore fires only when the last side is removed

**Choice.** Per-side teardown clears only its own side's panes and state;
whichever call removes the **last** cmdman-owned state performs today's
full restore (`pkg/muxctl/tmux/detach.go:12-47`: respawn default pane,
unset `pane-border-status`, clear all `@cmdman_*` options).

**Rationale.** No half-stamped windows that neither side recognizes — the
risk PLAN.md names — and no separate cleanup pass to remember.

**Rejected.** Always-partial teardown with residual state until an
explicit cleanup runs.

### F9 — Driver-neutral contract, tmux-only implementation

**Choice.** The frame contract is stated driver-neutrally in `muxctl`'s
interface docs (`session.go`, `driver.go`, `doc.go`) and implemented only
in the tmux driver — exactly how the ownership stamp is handled today.

**Rationale.** Future zellij/wezterm drivers implement or reject it
explicitly instead of consumers feature-detecting; the tmux-mechanism
caveat stays where it already lives (`scale_state.go:32-34`).

**Rejected.** Documenting frames as a tmux-driver extension consumers must
type-assert for.

### F10 — Separate validation; no reserved pane-name prefix

**Choice.** The frame tree and the project layout are always validated and
applied as separate calls, never merged into one `muxctl.Layout`, so
`Validate`'s duplicate-name rule (`pkg/muxctl/validate.go:23-29`) never
sees both trees. A project leaf literally named `frame-0` remains legal.

**Rationale.** The collision is structural, so remove the structure that
creates it; no constraint lands on user layout names.

**Rejected.** Reserving the `frame-` prefix in project-layout validation.
