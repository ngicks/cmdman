# DECISION — project-manager widget

One entry per material decision: choice, rationale, rejected alternatives.
Stubs below mirror PLAN.md Open questions; each fills in as it resolves.

## D1 — floating-pane summon reuses the `cmdman tui --popup` path (2026-08-15)

**Choice** (user, Q1): "Use similar path that `cmdman tui` uses. Auto-detect
+ flags / inheritance so if we later add other drivers, the component
transparently would work too." The switcher summon goes through the same mux
auto-detect + `--popup`-style flag seam as `cmdman tui --popup`
(`cmdman/cli/tui.go`, `resolvePopupDriver`), generalized to launch
`tui widget <name>`. tmux `display-popup` works now; zellij (no driver
exists) keeps erroring at that single seam until a driver is added, at which
point the switcher summon works transparently with no widget/switcher change.

**Rationale**: matches the only popup precedent in the codebase; keeps
driver knowledge in one place; avoids blocking the widget on a zellij driver.

**Rejected**: (a) implementing a full zellij driver in this plan — dwarfs the
widget; (b) a first-class floating-pane primitive on the muxctl `Driver`
contract — bigger contract change than the feature needs now (may still be
where the seam migrates when a second driver lands).

## D2 — vocabulary mapping (2026-08-15)

**Choice** (user, Q2): confirmed. scale = replica **count** of a service
(`cmdman compose scale SERVICE=NUM`); cycle of command = cycling **which
replica** a scaled command's dashboard pane shows (`cmdman compose mux
cycle-scale`); cycle of layout = cycling/applying named `mux: layouts:`
entries (Backend `CycleMux`/`ListLayouts`/`ApplyLayout`).

## D3 — window-first detection applies TUI-wide (2026-08-15)

**Choice** (user, Q3): TUI-wide. One detection function at the Backend seam;
the switcher's `Active` mark
(`cmdman/tui/widget/internal/panel/switcher.go:62-63`), the Layout tab's
`resolveLayoutSelection` (`cmdman/cli/tui_backend_mux.go:68-78`), and
project-manager all inherit window-first, cwd-fallback behavior. Additive:
cwd matching is unchanged when no owning window applies.

**Rejected**: widget-only detection — would leave the switcher blind to the
project whose window the user is sitting in.

## D4 — fallback UX (2026-08-15)

**Choice** (user, Q4): (a) run directly outside any mux window → run normally
with cwd-based detection; no detectable project at all → clear message naming
both failed probes (window, cwd). (b) switcher summon where a floating pane
is unavailable (plain terminal, or a mux the popup seam does not support) →
one-line inline message in the switcher explaining popup is not available.

**Rejected**: in-place run replacing the switcher view; strict error-out.

**Amended by D10**: the no-project message names each probe that failed —
mux token (when given), window, cwd — not just the original two.

## D5 — popup machinery placement: settled by D1 (2026-08-15)

Subsumed by D1: generalize the existing `cmdman/cli/tui.go` popup path
(currently hardwired to `tui __child`) to also launch `tui widget <name>`;
no new muxctl driver primitive in this plan.

## D6 — popup-only, excluded from frame components (2026-08-16)

**Choice** (user, Q6): project-manager is popup-summoned only, like launcher:
it does **not** join `builtinComponents` (`cmdman/frame/spec.go:38-42`), and
`frame` keeps rejecting it as a `component:` name.

**Rationale**: it is a shortcut summoned on demand; docking it would spend a
permanent pane on an occasional control panel.

**Rejected**: adding a `ComponentProjectManager` dockable const.

## D7 — switcher summon key is `m` (2026-08-16)

**Choice** (user, Q7): `m` (manage/mux) in the switcher summons
project-manager for a project row. Free today (taken: `j/k/down/up`, `enter`,
`z`, `q/ctrl+c/ctrl+d` — `panel.go:194-216`).

**Rejected**: `p`, `space`.

## D8 — `--mux-token` flag name; contract shapes (2026-08-16)

**Choice** (user for the flag name, Q8): the D10 token flag is
`--mux-token`. Remaining shapes decided as routine calls (repo preference:
decide and note), recorded in PLAN.md's Public surface delta: Backend gains
`ActiveIdentity`, `ProjectManager`, `SetScale`, `CycleScale`,
`SummonProjectManager` (project identified by the existing
`projectName, composeFile` pair convention); `cli.RunTUIWidget` takes a
`TUIWidgetOptions` struct.

**Amended 2026-08-16** (user): explicit targeting uses `--workdir` — not a
`--project` flag — following the compose command convention
(`cmdman compose -w/--workdir`, `cmd/cmdman/commands/compose.go:33-48`);
`--file`/`--project-name` mirror compose's `-f`/`-p` for disambiguation.
Rejected: a project-manager-specific `--project` flag.

**Rejected**: `--mux-window` (names the referent, but the token is meant to
stay opaque — pane-form tokens would make the name wrong).

## D9 — switcher summon targets the row under the cursor (2026-08-16)

**Choice** (user, Q9): `m` targets the project row the cursor is on —
consistent with `enter`'s switch-to-selected semantics. The summon passes
the project explicitly (`--workdir`/`--file`/`--project-name`, per the D8
amendment), not a token.

**Rejected**: always targeting the detected active project.

## D10 — explicit driver-agnostic mux token argument (2026-08-15)

**Choice** (user): the widget takes the source mux window as an explicit CLI
argument — an **opaque, intentionally driver-agnostic token** (tentative flag
name `--mux-token`, e.g. `--mux-token @5` from tmux's `#{window_id}`
expansion in a bind-key). It is the **highest-priority** detection probe,
ahead of enclosing-window and cwd probes. cmdman never parses the token; the
active muxctl driver interprets it.

**Rationale**: a popup/floating pane runs detached from the window the user
summoned it from, so process-context probing (`CurrentWindowID`) cannot see
the source window; bind-time format expansion is the reliable carrier. An
opaque token keeps the flag meaningful for future drivers (zellij) without
committing to tmux's id syntax.

**Open detail** (folds into Q8): exact flag name and the driver contract for
resolving a token to a window (accepted forms: window id, maybe pane id).

## D11 — `Replicas` is the live instance count, never `LabelScale` (2026-08-16)

**Choice** (routine call, forced by upstream D44): `ServiceScaleInfo.Replicas`
is the per-service **instance count** in the store — the number of the
project's commands carrying that service's labels, compose-ps style, exited
replicas included — not `LabelScale` / `CommandInfo.ScaleCount`. The widget's
`+`/`-` keys use it as the base for `SetScale`.

**Rationale**: the quicklaunch plan's D44
(`doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md`)
established the label goes stale, verbatim: "a `compose scale` leaves the
instances it keeps `ActionUnchanged` and their label at the count that was
desired when they started." Instance count tracks *desired* — reconcile adds
and removes rows on scale, and a crashed replica keeps its row — so it stays
correct where both the label and a running-only count would drift.

**Rejected**: `LabelScale`/`ScaleCount` (stale per D44); counting only
running replicas (drifts low when a replica exits).
