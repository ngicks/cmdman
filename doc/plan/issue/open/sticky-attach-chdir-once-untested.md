---
tags: cli attach test
---

# Sticky attach's chdir-once guarantee has no behavioral test

Recorded 2026-08-30 in the legacy backlog `issue.md`; migrated 2026-09-03.

`cli.AttachSticky` chdirs into `AttachOptions.WorkDir` once, then clears
the field on its value copy so the re-attach loop never repeats it
(`cmdman/cli/sticky.go:79-85`). The guarantee is structural only — a
refactor that stops copying the options by value, or reorders the
clearing, would regress silently into a chdir (and a failure log for a
deleted dir) on every restart cycle.

Fix direction: a test driving two loop iterations that asserts a single
chdir, e.g. via a counting seam like the switcher widget's `chdir` field.
