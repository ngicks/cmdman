---
tags: docs man compose attach
---

# compose-attach man page omits --scale

Recorded 2026-08-28 in the legacy backlog `issue.md`; migrated 2026-09-03.

`doc/man/cmdman-compose-attach.1.md` lists the command's options but not
the existing `--scale` flag (`cmd/cmdman/commands/compose_attach.go:41`),
which picks the 1-based replica of a scaled service and is required when a
service has more than one replica. Noticed while documenting
`compose capture-screen`, whose page does document its identical flag.

Fix direction: add the flag to the compose-attach page's options list,
matching the wording used by `cmdman-compose-capture-screen.1.md`.
