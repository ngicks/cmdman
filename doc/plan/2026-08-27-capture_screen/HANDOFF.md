# capture-screen — handoff ledger

## Out-of-scope discovery: compose-attach man page omits `--scale`

`doc/man/cmdman-compose-attach.1.md` (options list around lines 29–35) does
not document the existing `--scale` flag of `cmdman compose attach`
(`cmd/cmdman/commands/compose_attach.go:41`). Pre-existing gap noticed while
documenting `compose capture-screen`, which has the same flag. Follow-up: add
the flag to that page (one-line doc fix, any future docs pass).

## Out-of-scope discovery: TestStatus_WithoutRunningMonitor is not hermetic

`e2e/cmdman/status_test.go:141-145` fails when the test suite itself runs
inside a cmdman-supervised shell: `testEnv.execFull` inherits `os.Environ()`
(`e2e/cmdman/main_test.go:129`), so an inherited `CMDMAN_CMD_ID` makes
`cmdman status get` resolve that identity instead of erroring. Pre-existing;
unrelated to capture-screen. Follow-up: clear `CMDMAN_CMD_ID` in `execFull`
(or in that test).
