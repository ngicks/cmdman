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

## D12 — bind-key snippet wraps display-popup in run-shell [automatic] (2026-08-16)

**Choice**: the documented binding is
`bind-key -n M-p run-shell 'tmux display-popup -E … "cmdman tui widget
project-manager --mux-token #{window_id}"'` — not a bare `display-popup`
binding.

**Rationale**: spike (NOTES.md Q1): tmux 3.7b does not format-expand
`display-popup`'s shell-command (nor `-e VAR=…`); triggered from a real
binding the child received the literal `#{window_id}`. `run-shell` **is**
expanded; the run-shell form was verified end-to-end (token arrived `@1`,
resolved to the project identity).

**Rejected**: bare `display-popup` binding (broken); `-e` env carrier (not
expanded either).

## D13 — token resolves via ListWindows; window probe gated on `$TMUX` [automatic] (2026-08-16)

**Choice**: `ActiveIdentity`'s token probe resolves the token by matching it
against `ListWindows` rows (`Window.WindowID`), yielding identity and
staleness in one call with no new driver surface. The enclosing-window probe
runs `CurrentWindowID` only when the process is actually inside a mux
(`$TMUX`/`$ZELLIJ` present — same guard as `cmdman/mux/frame.go:384-390`).

**Rationale**: spike (NOTES.md Q1/Q2): `CurrentWindowID` is
**client-relative**, not process-relative — it reports the attached client's
displayed window, returns `ok=true` even outside tmux entirely, and with two
clients silently returns the other client's window. Raw `@N`/`%N` tokens
resolve server-globally as-is; on a stale token `ReadWindowState` swallows
the error (`"", nil`) while `ListWindows` matching distinguishes
"window gone" from "no identity".

**Rejected**: `ReadWindowState(token, "window")` (`window` is not a declared
`StateKey`; swallows staleness); a new driver `ResolveWindow` primitive (not
needed); trusting `CurrentWindowID`'s `ok` (it has no honest "don't know").

**Amended 2026-08-16 [automatic]** (step-3 implementation): pane-form tokens
(`%N`) do **not** resolve — `ListWindows` rows carry window ids only. D10's
"maybe pane id" open detail closes as unsupported; the documented binding
uses `#{window_id}`, so nothing documented breaks. Also: token resolution
required a new thin exported wrapper `mux.CurrentWindowID`
(`cmdman/mux/current.go`, mirroring `mux.List`) because `resolveServer` is
unexported — a scoped deviation from PLAN's "`cmdman/mux`: no change" line,
which was written about the scale ops.

## D15 — layout-tab probe precedence kept; statusbar inherits identity [automatic] (2026-08-16)

**Choice**: `resolveLayoutSelection` puts the identity probe ahead of its
existing cwd → by-name chain exactly as PLAN step 3 states, even though that
means an explicit Compose-tab selection is overridden whenever the user sits
in any cmdman-owned window. The pre-existing chain already preferred ambient
context (cwd) over the explicit selection, so this preserves, not changes,
the precedence philosophy. The docked statusbar also inherits identity-first
Active naming (it reads the same `Active` flag) — accepted as D3's
"TUI-wide" working as intended.

**Rejected**: ranking the explicit selection above ambient detection — a
precedence redesign beyond this plan's scope; recorded in HANDOFF.md as a
possible follow-up instead.

## D16 — step-4 contract deviations from the fenced delta [automatic] (2026-08-16)

**Choice**: two signatures diverge from PLAN's fenced Base-layer block, and
the block is updated to match. (a) `compose.Service.Scale` returns
`(*UpResult, error)`, not bare `error` — with `error` only, per-replica
start failures would stop reaching `cli.UpResultErr` and a failed replica
would exit 0. (b) `MuxScaleStateOption` is
`{Selection ProjectSelection; SessionName string}` per the plan's own
"exact shape aligned with MuxLsOption at implementation" clause.
`MuxScaleState` needed no `cmdman/mux` change — it folds `mux.List`'s
per-window `ScalePositions` (`cmdman/mux/list.go:111`).

**Rejected**: bare-`error` `Scale` (silently green failed replicas); the
flat File/ProjectName/WorkDir option (no sibling op is shaped that way).

## D17 — explicit project target outranks ambient identity in `ProjectManager` [automatic] (2026-08-16)

**Choice**: when the widget is invoked with an explicit compose target
(`--file`/`--project-name`, as the switcher summon passes per D9),
`ProjectManager` resolution uses that selection directly and skips the
ambient identity/cwd chain. Only a bare invocation (or `--mux-token`) uses
detection. **Owned by step 6**, which wires the summon.

**Rationale**: step 4's `ProjectManager` reuses `resolveLayoutSelection`,
so ambient identity would override the summon's explicit flags whenever the
popup opens inside any cmdman-owned window (a popup always does — `$TMUX`
is set), resolving the *enclosing* project instead of the row the user
picked. That inverts D9's "targets the project row the cursor is on" and
IDEA's "From the switcher, it targets the project under the cursor."
Explicit intent beats ambient default; D15's precedence call governs only
surfaces with no explicit target.

**Rejected**: leaving ambient-first for the summon path (breaks D9);
re-ranking the whole TUI's precedence (HANDOFF follow-up, unchanged).

## D18 — backward cycle = `set = Shown-1`; unknown/uncyclable rows refuse locally [automatic] (2026-08-16)

**Choice**: `CycleScale` has no "previous" primitive, so the widget's
`h`/`left` sends `set = Shown-1` (1-based select), wrapping to `Replicas`
from replica 1. When `Shown == 0` (D14's unknown) or the row is not
`Cyclable`, the key refuses with a local note and no backend call. Unknown
shown position renders `[?]`; a custom `shownBadge` is used because
`core.ScaleBadge` is `CommandRow`-shaped with no unknown state.

**Rationale**: an unknown position has no predecessor to name — guessing
one moves the pane somewhere unasked; a never-cyclable row would only
exercise D14's awkward error wording for nothing.

**Rejected**: omitting backward cycling (the key table promises it);
forward-only wrap-around emulation of "previous" (N-1 pane flips flash the
dashboard).

## D14 — `Shown` reports agreement only; disagreement renders unknown [automatic] (2026-08-16)

**Choice**: `compose.Service.MuxScaleState` reports a service's shown-replica
position only when every dashboard window of the project agrees; on
disagreement the service is omitted from the map, so
`ServiceScaleInfo.Shown=0` renders as unknown. The widget's error line for a
failed cycle must not imply the cycle didn't happen (a stray identity-stamped
window makes `cycle-scale` exit non-zero even when every real dashboard
succeeded — `cmdman/mux/cycle_scale.go:145-151`).

**Rationale**: spike (NOTES.md Q3): agreement is invocation-dependent, not an
invariant — a session-scoped `cycle-scale -s sessA` left windows at `web=3`
vs `web=2`, and the merged `ReadScaleState` read is last-row-wins
(`cycle_scale.go:317-323`), silently contradicting what one session shows.
Rendering a possibly-wrong number is worse than rendering unknown.

**Rejected**: last-row-wins passthrough (silently wrong for one of the
sessions); erroring on disagreement (blocks the whole view over one stale
window).

**Amended 2026-08-16 [automatic]** (step-4 implementation): a window holding
*no* position **abstains** rather than voting for replica 1 — `MuxUp` seeds a
new window from the merged read without writing state back
(`cmdman/compose/mux.go:70-77`), so an unset window is most likely showing
the agreed position, and the opposite reading would let one stray
identity-stamped window blank the whole view (the outcome this entry already
rejected). Pinned by `TestAgreedScalePositions`.
