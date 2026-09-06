---
tags: compose naming bug
---

# compose.GenerateName joins name halves ambiguously

Recorded 2026-08-22 in the legacy backlog `issue.md`; migrated 2026-09-03.

`compose.GenerateName` (`cmdman/compose/hash.go:33`) escapes each half by
doubling only its own dashes, then joins with a single `-`, so project
`"a-"` + command `"b"` collides with project `"a"` + command `"-b"`. The
identical defect in mux op names was fixed by joining with the unambiguous
`-_` separator (`cmdman/cli/mux_op.go`); `GenerateProjectIdentity` is safe
because its hex workdir hash anchors the separator. Deferred because
`GenerateName` feeds registered command names across the whole compose
layer: changing the encoding renames existing commands.

Fix direction: apply the same unambiguous-join treatment, either with a
migration story for existing registered names or accepting the rename
outright (the app has never been deployed).
