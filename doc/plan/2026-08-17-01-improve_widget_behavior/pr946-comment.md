# Draft comment for https://github.com/charmbracelet/x/pull/946

Confirmation from another real-world hit of #848: [cmdman](https://github.com/ngicks/cmdman) (a shell-command supervisor) mirrors supervised TTY output into an `x/vt` emulator and latches OSC 0/2 titles from its callbacks. Claude Code emits window titles prefixed with U+2733 (`✳ …`); the parser cut them at the `0x9C` continuation byte, the callback received the lone invalid byte `"\xe2"`, and that invalid string then failed protobuf marshaling of our status RPC — blanking the whole status UI while tmux (parsing the same bytes) displayed the title fine. The leftover bytes after the cut also rang a spurious BEL through the emulator's bell callback.

Results with this PR:

- The diff applies cleanly onto the released `ansi v0.11.7`; `go test ./...` passes in the module.
- Through `x/vt`, the Title callback now receives the whole `✳ …` title (verified for U+2733/U+273B/U+2726 leading and mid-string), and the phantom BEL is gone.
- Our end-to-end test that previously reproduced the truncation through the full stack (PTY → emulator → gRPC → CLI listing) passes with this patch vendored.

Would be great to see this merged — the U+2700 block is common in real title streams.
