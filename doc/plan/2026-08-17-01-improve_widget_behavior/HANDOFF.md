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

## 3. Show-before-launch under a standing frame for outside-tmux callers — deferred (D9, automatic)

**What**: Before the identity-keyed lookup change, `mux up` from outside
tmux could adopt a pre-existing frame-only window by name and launch the
project "under the chrome". Now the miss branch always creates, so that
path builds a fresh window beside the standing frame. Inside tmux the
current-window takeover still covers it. If the capability should return,
the find must key on the frame stamp (never the name) and needs answers
for: which frame-only window wins when several exist, and whether adoption
wants an opt-in/opt-out guard.

**Why not done here**: surfaced while removing by-name adoption in the
2026-08-17 widget-behavior plan; the approved design was "miss: plain
create", and frame-stamp adoption is new design. Recorded by the
autonomous orchestrator (DECISION.md D9).

**Follow-up**: decide with the user; likely belongs to the deferred muxctl
cleanup plan (entry 1). Related: `Server.Open`'s by-name fallback
(`pkg/muxctl/tmux/tmux.go:211`) is now exercised only by tests — a
removal candidate for the same cleanup.

Also note, same change, accepted consequence (not open): `compose mux
down` then `up` now builds a fresh window — down clears the stamp, so the
next up has nothing to recognise; the restored window stays. The e2e
tests pin the new behavior.

## 4. Upstream vt parser bug — report to charmbracelet/x/vt (D11, automatic)

The emulator's OSC parser honors raw C1 control bytes even when they are
continuation bytes inside a multi-byte UTF-8 rune, so an OSC 0/2 title
like "✳ done" (U+2733 = E2 9C B3) is cut at the 0x9C (read as ST) and
delivered as the invalid fragment `"\xe2"`; the leftover bytes then print
into the screen and the trailing BEL rings a phantom bell. Separately,
its `handleTitle` splits the payload on ';' and silently drops any title
containing one (`len(parts) != 2`). Pinned version:
`github.com/charmbracelet/x/vt v0.0.0-20260622092256-25656177ba8e`.

**Resolution (2026-08-19)**: the bug was already reported upstream as
charmbracelet/x#848 (with two open fix PRs: #946, the UTF-8
continuation-byte counter, and #886, which drops 8-bit ST entirely; the
actual defect lives in `x/ansi`'s parser, which `x/vt` uses). PR #946
applied cleanly to the pinned `x/ansi v0.11.7`; it is vendored at
`third_party/charmbracelet-x-ansi` behind a `replace` directive, its
module tests and cmdman's full suite pass, and glyph titles now latch
whole (the phantom bell is gone too). A confirmation comment for
PR #946 is drafted at `pr946-comment.md` next to this file — posting it
from this environment failed (the gh token cannot comment on foreign
repos), so it awaits a manual post.

**Follow-up**: track charmbracelet/x#946; when a release containing the
fix ships, delete `third_party/charmbracelet-x-ansi` plus the `replace`
and bump the dep (see `third_party/README.md`). The latch-side sanitize
(D11) stays regardless, as the guard against any future parser
misbehavior.
