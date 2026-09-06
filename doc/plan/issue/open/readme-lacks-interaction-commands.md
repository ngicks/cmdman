---
tags: docs readme cli
---

# README does not cover the interaction commands

Recorded 2026-08-29 in the legacy backlog `issue.md`; migrated 2026-09-03.

`README.md` never mentions `capture-screen` — nor `send-keys`, `attach`, or
the other interaction verbs; it currently only covers the TUI. The
capture-screen plan's step 9 ("man page under `doc/man`, README mention
beside send-keys", `doc/plan/2026-08-27-capture_screen/PLAN.md:215`) was
checked off in STATUS.md, but the README half never landed because there is
no send-keys mention to sit beside. Noticed during capture-screen QA.

Fix direction: give the README a short CLI-surface overview (or at least an
interaction-commands paragraph linking to `doc/man/`) so future "README
mention" plan steps have a place to land; add `capture-screen` beside
`send-keys` there.
