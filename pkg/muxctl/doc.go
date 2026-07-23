// Package muxctl controls one multiplexer window from non-interactive callers.
// Drivers implement [Driver] and [Session]; [MuxSpec] describes resolved,
// driver-agnostic layouts.
//
// The multiplexer is a disposable viewer: rebuilding, detaching, or closing it
// must not stop observed processes. [Session.ApplyLayout] resets only the
// viewer panes.
//
// Drivers stamp [Config.OwnedIdentity] in native per-window storage and expose
// stamped windows through [Driver.ListWindows]. Enumeration must work without
// an attached client or current window. Titles are presentation, so they must
// not be used as durable identity storage.
package muxctl
