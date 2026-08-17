# HANDOFF — improve TUI widget behavior

## 1. Broader muxctl cleanup — user-approved deferral (D7)

**What**: A dedicated cleanup pass over `pkg/muxctl`'s interface contract
and layering, beyond the minimal fix this plan ships. Step 7 stays in
scope here (it fixes the reported bug): driver `New` without `WindowID`
always creates, the by-name adoption in
`pkg/muxctl/tmux/tmux.go:290-313` is deleted, `WindowName` is documented
display-only, and `mux.Run` orchestrates find-or-create by identity via
`Server.ListWindows`. Everything wider is deferred:

- Whether `Server.New`'s remaining mode switches (`WindowID` targeting,
  `ReuseCurrentWindow` takeover, create) should be split into separate,
  single-purpose interface methods (find / open / create) instead of one
  config-driven entry point.
- `currentWindowToReuse` / `ReuseCurrentWindow` semantics
  (`pkg/muxctl/reuse.go`, `pkg/muxctl/tmux/tmux.go:113-119`) — audit
  against the same "names and current-window are not identity" principle.
- `deriveIdentity`'s fallback for standalone (non-compose) specs
  (`cmdman/mux/run.go:134-139`), where the identity defaults to the
  window/session *name* — the name-as-identity assumption survives there
  in stamped form.
- A contract-documentation sweep of `pkg/muxctl` (`Config`, `Window`,
  `doc.go`) so every field states whether it is a key or display-only.
- Inherited, still-open item from
  `doc/plan/2026-08-15-01-switcher_creates_window/HANDOFF.md`: windows on
  a dedicated mux socket are invisible to autodetect-only `Land`.

**Why not done here**: user decision 2026-08-18 — "We'll later revisit to
muxctl clean up" (DECISION.md D7). This plan's scope stays the five widget
behaviors; a muxctl redesign is its own plan.

**Follow-up**: a future plan dedicated to muxctl (suggested name:
`muxctl-NN-interface-cleanup`, joining the existing `muxctl-00`/`muxctl-01`
series), taking this list plus whatever step 7's implementation uncovers.

## 2. `frame show` self-heal on desynced windows — deferred (D8, automatic)

**What**: `frameTarget.show` skips its teardown when no def is recorded, so
a window carrying leftover frame-stamped panes with an empty
`frameDefOption` gets new docks stacked on the leftovers. Whether show's
pre-hide should become unconditional (making show itself self-healing) is
undecided.

**Why not done here**: surfaced during the hide-hardening step of the
2026-08-17 widget-behavior plan; hide is the documented, now
state-independent recovery path, and changing show semantics was out of the
approved scope. Recorded by the autonomous orchestrator (DECISION.md D8).

**Follow-up**: decide with the user whether show should hide
unconditionally; if yes, a small change in `cmdman/mux/frame.go` plus a
test mirroring the hide desync tests.
