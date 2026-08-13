// Package mux is the cmdman layer above [muxctl] backing the
// `cmdman mux` and `cmdman compose mux` subcommands.
//
// It owns the user-facing YAML format whose leaves identify a cmdman command
// (or compose service) by name/ID, resolves those names into argv via a
// caller-supplied [Resolver] (typically `cmdman attach <id>` or
// `cmdman logs --sticky <id>`), and applies the resulting [muxctl.MuxSpec] to
// the chosen multiplexer driver — selecting the next layout from
// [muxctl.MuxSpec.Layouts] by reading back the previous marker via
// [muxctl.Session.StatWindow].
//
// The entry points, by file:
//
//   - spec.go   — cmdman-facing YAML types ([Spec], [Layout], [PaneSpec])
//     plus the bare-string leaf shorthand.
//   - build.go  — [Build] / [PaneArgvOpts] / [Resolver]: turns a [Spec] into a
//     [muxctl.MuxSpec] with resolved argv.
//   - run.go    — [Run] / [RunOptions]: driver autodetect (`$TMUX` / `$ZELLIJ`,
//     fallback `tmux`), tmux session+window construction, ownership identity
//     stamping, marker-based layout cycling, the `default_frame` auto-show on
//     the window it builds (V9; warn-never-fail), and the attach-hint print
//     when invoked outside a multiplexer.
//   - down.go   — [Down] / [DownOptions]: identity-based teardown via
//     [muxctl.Server.ListWindows] — server-wide, no $TMUX dependence,
//     works from any pane or outside tmux entirely.
//   - list.go   — [List] / [ListOptions] / [OwnedWindow]: enumerate stamped
//     dashboard windows; presentation is cmdman/cli's job.
//   - frame.go  — [FrameShow] / [FrameHide] / [FrameCycle] / [FrameList]: the
//     frame verbs, composing cmdman/frame's def pipeline onto
//     [muxctl.Session.ShowFrame] / [muxctl.Session.HideFrame] around the
//     current window.
package mux
