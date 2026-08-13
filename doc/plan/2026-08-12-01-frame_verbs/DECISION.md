# Decision log

One entry per material decision: the choice, the rationale, the
rejected alternatives. Entries are numbered **V1, V2, …** (frame
verbs), never colliding with the parent's D-numbers or the muxctl
plan's F-numbers, both of which this file cites.

## Inherited (context, not re-decided here)

Operative sentences quoted verbatim — see
[../2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md](../2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md)
and
[../2026-08-10-01-muxctl_first_class_frame/DECISION.md](../2026-08-10-01-muxctl_first_class_frame/DECISION.md).

- **D15**: "Frame definitions are named standalone files under
  `<config-dir>/frame/<name>.yaml`, exactly like compose defs; which
  def applies is passed via config or a command flag. The frame is
  switched **shown / hidden / selected / cycled** by explicit user
  control."
- **D16**: "cmdman ships no default frame content. `switcher` and
  `statusbar` ship as built-in components that user defs reference.
  The shown/hidden switch doubles as the small-terminal collapse
  gesture."
- **D17**: "A frame definition can override the default hooks."
  (Owned here per **V5**, plan step 5.)
- **D19**: frame `command:` entries "are ephemeral by default,
  `managed: true` opts into cmdman supervision".
- **D6**: "switching navigates per-project windows".
- **D37**: "`component: <name>` resolves to `cmdman tui widget
  <name>`".
- **F7** (muxctl): "managed-ness (D19) is entirely the consumer's
  concern above the driver — the frame verbs detach/preserve a managed
  viewer before asking the driver to remove its pane." Select/cycle
  "are consumer compositions of hide + show."
- **F6** (muxctl): a frame shown with no project realizes the main
  region as "the driver's default pane", so show-before-launch needs
  no verb-side work.

## Decided

**Idea gate (2026-08-13): IDEA.md confirmed as written by the user.**

### V9 (2026-08-13, Q9) — `mux up` auto-shows `default_frame`

**Choice.** When `default_frame` is set, every window `mux up` creates
gets that frame shown automatically. The key therefore both names the
default def (D15's "via config") and auto-applies it. Unset key = no
auto-show. A window that is already framed is left untouched — auto-
show never replaces a def the user selected deliberately.

**Rationale (user's call).** Under D6, switching is a jump between
per-project windows; per-window deliberate placement (option a) would
make the frame vanish on every jump to a fresh window, defeating the
fixture. Auto-show makes the frame effectively everywhere without a
new tracking mechanism.

**Rejected.** (a) stays bare — frames placed deliberately per window
(the strict D15 reading; loses the fixture on switch). (c) frame
follows the client — switcher shows on arrive / hides on leave (one
frame tracking the user; makes the switcher a frame-verb caller and
adds hide/show churn on every jump).

*Noted routine calls (mine, not user-asked):* (1) a broken
`default_frame` (unresolvable or invalid def) must not fail `mux up` —
the dashboard is up's primary job; it warns and continues unframed.
(2) No `--no-frame` opt-out flag initially: unsetting the key, or
`frame hide` after up, covers it; a flag can be added on demand.

### V1 (2026-08-13, Q1) — verbs mount at `cmdman mux frame`

**Choice.** `cmdman mux frame show|hide|cycle|ls`, composed in
`cmd/cmdman/commands` beside `mux up/down/ls`, inheriting
`-s/--session`.

**Rationale.** Frames are per-window fixtures; mux already owns window
control and has the session plumbing; muscle memory from
`mux up/down/ls` carries over.

**Rejected.** Top-level `cmdman frame` (D15's "exactly like compose
defs" reads on where def *files* live, not where verbs mount); both
with an alias (two names for one thing, double the docs).

### V2 (2026-08-13, Q2) — no `select` verb; `show DEF` covers it

**Choice.** `select` is folded into `show`: showing a def while a
different one is up replaces it in place. D15's "selected" behavior is
delivered; the verb is not.

**Rejected.** The four-verb family verbatim from D15 — `select` would
be an exact synonym of show-with-argument.

### V3 (2026-08-13, Q3) — cycle order: sorted `ListNamedDefs`

*Routine call (mine, per base preference — noted, not user-asked).*

**Choice.** `cycle` walks the sorted `frame.ListNamedDefs()` order,
starting after the currently shown def (read from `Window.Frame`),
wrapping; from no-frame it shows the first def.

**Rejected.** Def-file declared order (no such ordering exists across
standalone files); MRU (persistent state to maintain for marginal
gain).

### V4 (2026-08-13, Q4) — `frame ls` ships now

**Choice.** `cmdman mux frame ls` lists the discoverable defs and
which def is shown on which window (`ListNamedDefs` +
`ListWindows`/`Window.Frame`), satisfying IDEA §7 discoverability.

**Rejected.** Defer — show's error already lists candidates, but both
halves exist today, so the verb is cheap composition.

### V5 (2026-08-13, Q5) — D17 hook override owned by this plan

**Choice.** This plan adds a `hooks:` key (`model.HookSet`, same shape
as config `default_hooks`) to the frame def entry grammar
(`cmdman/frame/spec.go`); a managed entry's hooks land in its
supervised command's `model.CommandConfig.Hooks`, overriding config
`DefaultHooks` through the existing per-command precedence. Plan
step 5.

**Rationale.** D17 is decided behavior owned by no plan and no code —
exactly the unowned-clause gap this plan family was burned by before
(`default_frame` was caught the same way).

**Rejected.** A separate plan (small enough to not warrant one, and
another handoff is another chance to drop it); re-deciding/dropping
D17.

*Noted routine call:* hooks are monitor behavior, so `hooks:` on an
ephemeral (unmanaged) entry has nothing to attach to — it warns and is
ignored, per the project's warn-never-silently-drop YAML convention.

### V6 (2026-08-13, Q6) — switcher navigate-only; `--no-quit`; no `q` when docked

**Choice (user's refinement beyond the offered option).** The docked
switcher is navigate-only: move (arrows / j k), enter, mouse click
switch windows — no start/stop/kill from the docked form. In
addition, the widget entrypoint gains a `--no-quit` flag that unbinds
`q`/quit, and a widget invoked **as a frame component** always gets it
(`frame.WidgetArgv` appends `--no-quit`), so a docked widget never
exits from a keypress.

**Rationale.** A frame pane exiting leaves a dead hole in the fixture;
quitting is a standalone-run affordance. The navigate-only boundary is
recorded here so parent Q31's "full command manager" creep is
explicitly out.

**Rejected.** Navigate + manage (start/stop in the docked form) —
nothing upstream demands it.

### V7 (2026-08-13, Q7) — managed identity `frame-<def>-<i>` + adopt

**Choice.** Each `managed: true` entry maps to a supervised command
named `frame-<def>-<i>` (def name + entry index). `show` adopts it if
already running, else creates it; `hide`/`cycle` detach the viewer and
leave the command running (F7).

**Rejected.** Content-hash identity (compose `LabelConfigHash`
precedent — more machinery; revisit if stale-adopt bites); always
create (contradicts D19's survive-hide intent).

*Noted trade-off:* a changed entry command under the same name adopts
the old process until it is stopped by hand. Documented behavior;
content-hash recreation is the upgrade path if it hurts in practice.

### V8 (2026-08-13, Q8) — collapse key `z` in the docked switcher

**Choice.** `z` in the docked switcher runs frame hide — collapse
without leaving the keyboard, honoring D16's "shown/hidden switch
doubles as the small-terminal collapse gesture". Restore is CLI
(`frame show`), since the widget is gone once hidden.

**Rejected.** CLI-only collapse for now.

*Noted routine call:* `z` targets the current window's frame and is a
quiet no-op when none is up (consistent with hide's no-op semantics),
so the binding needs no docked/standalone mode split.

## Stubs — open questions (PLAN.md "Open questions")

All resolved 2026-08-13.

- [x] Q9 frame presence across window switches → **V9**
- [x] Q1 verb mount → **V1**
- [x] Q2 `select` verb vs `show DEF` → **V2**
- [x] Q3 cycle order & start → **V3**
- [x] Q4 `frame ls` verb → **V4**
- [x] Q5 D17 hook-override owner → **V5**
- [x] Q6 switcher interaction set → **V6**
- [x] Q7 managed entry identity → **V7**
- [x] Q8 in-widget collapse gesture → **V8**
