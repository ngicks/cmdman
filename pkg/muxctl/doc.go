// Package muxctl controls one multiplexer window from non-interactive callers.
// Drivers implement [Driver], [Server], and [Session]; [MuxSpec] describes
// resolved, driver-agnostic layouts.
//
// The multiplexer is a disposable viewer: rebuilding, detaching, or closing it
// must not stop observed processes. [Session.ApplyLayout] resets only the
// viewer panes of the project region it builds.
//
// A window carries two independent identities. Drivers stamp
// [Config.OwnedIdentity] in native per-window storage for the project that owns
// the window, and [Session.ShowFrame] records the shown frame's name under
// [StateKeyFrameDef]. [Server.ListWindows] enumerates every window carrying
// either one and reports both, without an attached client or current window.
//
// The two identities divide the window's panes, so each side is operated and
// torn down without disturbing the other: a project apply rebuilds only the
// project region, [Session.ShowFrame] and [Session.HideFrame] only the frame's
// panes, focus never lands in a frame pane, and each teardown clears only its
// own state — whichever goes last restores the window itself.
//
// Titles are presentation, so they must not be used as durable identity
// storage.
package muxctl
