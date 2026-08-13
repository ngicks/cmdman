package model

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// HookEvent names a sequence the monitor captures from a command's output
// (D39). It is the key of a [HookSet].
type HookEvent string

const (
	// HookEventBell is BEL (0x07).
	HookEventBell HookEvent = "bell"
	// HookEventTitle is a window-title sequence (OSC 0 / OSC 2).
	HookEventTitle HookEvent = "title"
	// HookEventNotification is a desktop notification (OSC 9 / OSC 777).
	HookEventNotification HookEvent = "notification"
)

// HookEvents returns every hook event kind in a stable order.
func HookEvents() []HookEvent {
	return []HookEvent{HookEventBell, HookEventTitle, HookEventNotification}
}

// IsHookEvent reports whether s names a hook event.
func IsHookEvent(s string) bool {
	return slices.Contains(HookEvents(), HookEvent(s))
}

// HookAction is what happens to a captured sequence on its way to an attached
// viewer. Capturing the state it carries is not an action: the monitor latches
// title, bell, and notification state unconditionally and serves it over RPC,
// with or without a hook (D40).
type HookAction string

const (
	// HookActionPassthrough leaves the sequence in the byte stream, so an
	// attached viewer sees it exactly as the command wrote it. It is the
	// built-in default.
	HookActionPassthrough HookAction = "passthrough"
	// HookActionBlock swallows the sequence so no viewer ever sees it.
	HookActionBlock HookAction = "block"
)

// HookActions returns every hook action in a stable order.
func HookActions() []HookAction {
	return []HookAction{HookActionPassthrough, HookActionBlock}
}

// Hook is what one event kind does: what happens to the sequence itself
// (Action) and what external command it runs (Exec). The two are independent -
// an event can be blocked and still run a hook, or pass through and run one.
type Hook struct {
	// Action defaults to [HookActionPassthrough] when empty.
	Action HookAction `json:"action,omitzero"`
	// Exec is an argv run fire-and-forget when the event fires, with the event
	// data in CMDMAN_HOOK_* environment variables. An empty Exec runs nothing.
	Exec []string `json:"exec,omitzero"`
}

// Blocks reports whether the sequence must be kept from viewers.
func (h Hook) Blocks() bool {
	return h.Action == HookActionBlock
}

// HookSet configures hook behavior per event kind. A missing key means the
// built-in default (pass the sequence through, run nothing).
type HookSet map[HookEvent]Hook

// Validate rejects unknown event keys and actions so a typo in a config file
// surfaces at load time instead of silently disabling a hook.
func (s HookSet) Validate() error {
	for _, ev := range slices.Sorted(maps.Keys(s)) {
		if !IsHookEvent(string(ev)) {
			return fmt.Errorf(
				"hooks: unknown event %q (known: %s)",
				ev,
				joinHookNames(HookEvents()),
			)
		}
		h := s[ev]
		if h.Action != "" && !slices.Contains(HookActions(), h.Action) {
			return fmt.Errorf(
				"hooks: event %q: unknown action %q (known: %s)",
				ev,
				h.Action,
				joinHookNames(HookActions()),
			)
		}
		if len(h.Exec) > 0 && h.Exec[0] == "" {
			return fmt.Errorf("hooks: event %q: exec program is empty", ev)
		}
	}
	return nil
}

// HookLayers holds the hook sets that contribute to a command's effective hook
// configuration, highest precedence first (D40). Layers do not merge field by
// field: the most specific layer that names an event defines that event
// completely, and fields it leaves unset take the built-in default.
type HookLayers struct {
	// Frame is unused: a frame definition's hook override (D17) does not travel
	// through a layer of its own. A `hooks:` entry in a def belongs to a
	// `managed: true` entry, which is a supervised command like any other, so
	// the override is written into that command's [CommandConfig.Hooks] when the
	// frame verbs create it and arrives here as Command (V5). What is left here
	// is a slot for an override that could not be a per-command one; nothing
	// sets it.
	Frame HookSet
	// Command is the per-command HookSet from [CommandConfig.Hooks].
	Command HookSet
	// Global is the defaults map from the cmdman config file.
	Global HookSet
}

// Resolve returns the effective hook for ev. The zero [Hook] - passthrough,
// no exec - is the built-in default at the bottom of the precedence chain.
func (l HookLayers) Resolve(ev HookEvent) Hook {
	for _, set := range []HookSet{l.Frame, l.Command, l.Global} {
		if h, ok := set[ev]; ok {
			return h
		}
	}
	return Hook{}
}

// joinHookNames renders a vocabulary for an error message.
func joinHookNames[T ~string](values []T) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return strings.Join(out, ", ")
}
