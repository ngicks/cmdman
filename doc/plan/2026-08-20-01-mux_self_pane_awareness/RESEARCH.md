# Root-cause research (2026-08-20, verified by isolated repro)

Both reported symptoms — "`compose -f devenv up` then `mux up 1` never starts
`frame show`" and "all panes killed but not restarted" — have one root cause:

**When `mux up` runs from a shell pane inside the window being (re)built, the
tmux driver destroys the caller's own pane mid-apply, killing the cmdman
process that is orchestrating the operation.** The process dies silently
(SIGHUP/kill from its own tmux command) and everything scheduled after that
point never happens.

Two sub-cases:

1. **Caller's pane is the anchor** (single-pane window → current-window
   takeover, `cmdman/mux/run.go:175`). The anchor hosts the DFS-last leaf, so
   cmdman dies at the *final* `respawn-pane -k` of its own pane
   (`pkg/muxctl/tmux/leaf.go:117`). The layout completes, but
   `showDefaultFrame` — which runs *after* `ApplyLayout` in `mux.Run`
   (`cmdman/mux/run.go:247-260`) — is never reached. → no frame, shell
   consumed, no error.
2. **Caller's pane is a non-anchor project pane** (invocation from a pane
   inside the existing dashboard). `resetWindow` kills every project pane but
   the first (`pkg/muxctl/tmux/apply.go:222-226`), including the caller's.
   cmdman dies before any split/respawn; the viewers were already quiesced
   (detach keys, exited under `remain-on-exit on`). → one dead pane left,
   nothing rebuilt. The deferred `remain-on-exit off` restore
   (`pkg/muxctl/tmux/detach.go:181-185`) never runs either.

The hazard is acknowledged in `pkg/muxctl/tmux/leaf.go:63-67` ("when the
process being replaced is itself driving this tmux command, it can die before
any follow-up tmux command lands") but only title/marker writes are ordered
around it.

## Repro matrix (scratch HOME/data/runtime, private tmux socket)

| Invocation context of `compose -f devenv mux up 1` | Result |
|---|---|
| Outside tmux | ✅ dashboard + frame, attach hint |
| Inside tmux, different window | ✅ rebuilt, frame shown, rc=0 |
| Single-pane shell window (takeover) | ⚠️ dashboard built, **no frame**, shell silently consumed |
| Pane inside the dashboard window | 💥 **panes killed, nothing rebuilt**, one dead pane |

## Regression provenance (corrected after user input)

The failing path is byte-identical on v0.0.20 and v0.0.22 (both reproduced),
and `git diff v0.0.18..v0.0.20` over apply.go / reuse.go / leaf.go /
detach.go / cmdman/mux/run.go is empty. The regression is **not** in the
apply code — it is that the frame feature (d52b1ec, v0.0.18) added
`showDefaultFrame` *after* `ApplyLayout` in `mux.Run`, behind the
always-present final self-consuming respawn. Pre-frame, nothing material ran
after the apply, so a takeover consuming the invoking shell as its last act
was harmless: the dashboard was already complete, and the shell becoming a
viewer is the intended takeover outcome. Post-frame, the same self-kill
silently skips the frame show (symptom 1).

Two more facts about the user's workflow explain the history:

- Their habitual invocation point is the **supervised shell displayed in a
  dashboard pane** (the `shell` compose command, viewed through
  `cmdman attach`). That process tree lives under the command's monitor, not
  under the tmux pane, so pane kills/respawns never touch the invoking
  cmdman — the apply always completed from there. The viewer pane detaches
  and respawns mid-apply and reconnects to the same shell.
- The bare-CLI-from-a-regular-pane flow (today's incident) puts the driver
  inside the pane tree; config-dir projects (`-f devenv`, v0.0.21) made that
  flow natural. Symptom 2 (invocation from a regular extra pane inside the
  dashboard: killed mid-`resetWindow`, nothing rebuilt) fails identically on
  every version since v0.0.18 that was tested.

Caveat for the fix: a shell supervised by a monitor inherits `$TMUX_PANE`
from whichever pane ran `compose up`, so the invoking process can carry a
**stale** pane id that may even name a pane of the target window. The
mechanism must be benign under misidentification (deferring a wrongly-"self"
pane's respawn to last is harmless; killing the wrong pane early is not).

## Adjacent hazard (same shape, other verbs)

- `Session.Detach` → `collapseProjectRegion` → `resetWindow` + anchor
  respawn (`pkg/muxctl/tmux/detach.go:23-77`): `mux down` from inside the
  dashboard self-kills the same way.
- `Session.RespawnLeaf` (`pkg/muxctl/tmux/leaf.go:159-169`): cycle-scale
  respawning the pane the caller lives in.
- `Session.HideFrame` kills frame panes; a caller shell inside a frame pane
  is unusual but possible.

## Cwd-scoped identity (clarified: intended behavior, not a hazard)

Work_dir-less config projects hash the invocation cwd into the project
identity (`cmdman/compose/selection.go:40`, `cmdman/compose/hash.go:16-19`):
a dashboard brought up from cwd A is a different instance than `mux up` from
cwd B. Initially recorded as a contributing hazard; the user clarified this
is by design — compose is a template for a file, each cwd instantiates its
own project, and `work_dir` exists precisely to pin a project to one
instance. Cross-cwd "duplicate" windows are separate instances, correctly.

## Adversarial verification (2026-08-21)

The spare-then-settle direction survives the root-cause check, but the first
draft placed the finality boundary too low and overstated one failure guarantee.
The following gaps were verified against the current callers:

1. **Down is aggregate.** `cmdman/mux/down.go:137-171` loops over every matching
   window and prints after each detach. Settling a self pane inside
   `Session.Detach` or at the end of that row can prevent later rows and output
   from running. The settle belongs after the complete loop.
2. **CycleScale mutates durable state after respawn.** In
   `cmdman/mux/cycle_scale.go:228-250`, `RespawnLeaf` precedes
   `writeScalePosition`; the outer function also visits multiple windows and
   builds aggregate results/errors (`:104-126`). A self-consuming respawn must
   be deferred past all of those effects, not merely past the driver method.
3. **HideFrame is sometimes only phase one.** `frameTarget.show` replaces a
   def with `hideFrameOf` followed by `ShowFrame`
   (`cmdman/mux/frame.go:225-247`). A settle produced by hiding the old frame
   must be retained through the show phase. The same applies to `FrameCycle`,
   which uses this replacement path.
4. **The supervised-shell error promise is stronger than self-pane deferral.**
   `quiesceViewers` intentionally detaches marked viewers before mutations. A
   command executing in the monitor-owned shell survives a pane teardown, but
   its inherited `$TMUX_PANE` does not identify the current attach viewer.
   Therefore "$TMUX_PANE-only defer" can preserve the shell process without
   guaranteeing that a failed operation leaves its error immediately visible.
   Q6 asks whether viewer restoration is part of the required failure UX.
5. **D2's quantifier and list disagree.** `muxctl.Session.Close` calls
   `kill-window` and is pane-destroying, but D2's exhaustive-looking list omits
   it. No production call site uses it; Q7 makes the intended boundary explicit.
6. **CycleScale lacks the environment seam used elsewhere.** It calls
   `resolveServer(..., os.Environ())` and `CycleScaleOptions` has no `Env`.
   Deterministic stale/popup pane-id tests and explicit cmdman-layer resolution
   require adding or otherwise accounting for that seam in the post-gate
   public-surface contract.
