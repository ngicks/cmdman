# Status — switcher creates the project window

Current state: implemented; unit + e2e tests and lint green. A final review
pass is in flight; findings (if any) land as follow-up commits.

## Checklist

- [x] Step 1 — `SwitchTarget` contract ("the backend receives the group's
  `Workdir`/`Name` … not just the opaque identity hash", DECISION.md
  "Identity key")
- [x] Step 2 — panel keeps identity-less dead end ("still gets the hint-line
  message and no command, as today", DECISION.md "no derivable identity")
- [x] Step 3 — `mux.Land` find-or-create+focus, pre-stamped identity passed
  through (DECISION.md "Identity key" amendment); no compose up
  (DECISION.md "does not bring the project up")
- [x] Step 4 — fake backend + panel tests assert the dispatched target
- [x] Step 5 — e2e: create-then-jump and find-not-create on re-select
- [x] Step 6 — man page + subcommand help describe find-or-create-then-jump

## Verification

- [x] `go build ./...`, `go vet ./...`
- [x] `go test ./...` including e2e (~169s)
- [x] `golangci-lint run`
- [ ] Reviewer pass (in flight)

Next action: fold in reviewer findings, then done.
