# HANDOFF — improve TUI widget behavior

All open items were folded into `doc/plan/issue.md` at the user's
direction (2026-08-19); this plan is concluded. Entry 4 keeps its
resolution record below for history.

## 1. Broader muxctl cleanup — user-approved deferral (D7) — moved (2026-08-19)

Moved to `doc/plan/issue.md` at the user's direction.

## 2. `frame show` self-heal on desynced windows — deferred (D8, automatic) — moved (2026-08-19)

Moved to `doc/plan/issue.md` at the user's direction.

## 3. Show-before-launch under a standing frame for outside-tmux callers — deferred (D9, automatic) — moved (2026-08-19)

Moved to `doc/plan/issue.md` at the user's direction.

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
`internal/third_party/charmbracelet-x-ansi` behind a `replace` directive, its
module tests and cmdman's full suite pass, and glyph titles now latch
whole (the phantom bell is gone too). A confirmation comment for
PR #946 is drafted at `pr946-comment.md` next to this file — posting it
from this environment failed (the gh token cannot comment on foreign
repos), so it awaits a manual post.

**Follow-up**: moved to `doc/plan/issue.md` (2026-08-19) — track
charmbracelet/x#946, unvendor on the fix release, and the pending manual
post of `pr946-comment.md`.
