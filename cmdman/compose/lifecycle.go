package compose

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// LifecycleEvent names a point in a compose command's lifecycle at which a hook
// runs. The Lifecycle prefix keeps these apart from model.HookEvent, which names
// the terminal sequences a monitor captures from a command's output.
type LifecycleEvent string

const (
	LifecycleCreatePre  LifecycleEvent = "create_pre"
	LifecycleCreatePost LifecycleEvent = "create_post"
	LifecycleStartPre   LifecycleEvent = "start_pre"
	LifecycleStartPost  LifecycleEvent = "start_post"
	LifecycleStopPre    LifecycleEvent = "stop_pre"
	LifecycleStopPost   LifecycleEvent = "stop_post"
	LifecycleRemovePre  LifecycleEvent = "remove_pre"
	LifecycleRemovePost LifecycleEvent = "remove_post"
)

// lifecycleEvents lists every LifecycleEvent in declaration order.
var lifecycleEvents = [...]LifecycleEvent{
	LifecycleCreatePre,
	LifecycleCreatePost,
	LifecycleStartPre,
	LifecycleStartPost,
	LifecycleStopPre,
	LifecycleStopPost,
	LifecycleRemovePre,
	LifecycleRemovePost,
}

func (e LifecycleEvent) Validate() error {
	if slices.Contains(lifecycleEvents[:], e) {
		return nil
	}
	return fmt.Errorf("unknown lifecycle event %q (allowed: %s)", e, joinLifecycleEvents(
		lifecycleEvents[:]...))
}

// acquires reports whether e can acquire a resource.
func (e LifecycleEvent) acquires() bool {
	switch e {
	case LifecycleCreatePre, LifecycleCreatePost, LifecycleStartPre, LifecycleStartPost:
		return true
	default:
		return false
	}
}

// releases returns the events that may release a resource acquired at e: the
// remove pair undoes a create, the stop pair undoes a start. It returns nil for
// an event that does not acquire.
func (e LifecycleEvent) releases() []LifecycleEvent {
	switch e {
	case LifecycleCreatePre, LifecycleCreatePost:
		return []LifecycleEvent{LifecycleRemovePre, LifecycleRemovePost}
	case LifecycleStartPre, LifecycleStartPost:
		return []LifecycleEvent{LifecycleStopPre, LifecycleStopPost}
	default:
		return nil
	}
}

func joinLifecycleEvents(events ...LifecycleEvent) string {
	s := make([]string, len(events))
	for i, e := range events {
		s[i] = string(e)
	}
	return strings.Join(s, ", ")
}

// OnError is what a failing hook does to the lifecycle operation it runs in.
// The zero value means OnErrorFail.
type OnError string

const (
	OnErrorFail     OnError = "fail"
	OnErrorContinue OnError = "continue"
	OnErrorIgnore   OnError = "ignore"
)

func (o OnError) Validate() error {
	switch o {
	case "", OnErrorFail, OnErrorContinue, OnErrorIgnore:
		return nil
	default:
		return fmt.Errorf("unknown on_error %q (allowed: fail, continue, ignore)", o)
	}
}

// resolved returns o with the zero value replaced by OnErrorFail.
func (o OnError) resolved() OnError {
	if o == "" {
		return OnErrorFail
	}
	return o
}

// LifecycleExec is the command one hook runs for one event.
type LifecycleExec struct {
	Args    []string `json:"args"`
	OnError OnError  `json:"on_error,omitzero"`
}

// LifecycleHook is one item of a compose command's hooks: list. Each event it
// sets runs as a cmdman command of its own, named by [ExecCommandName].
//
// A hook with Resource set declares a resource. It sets exactly one acquire
// event (create_pre, create_post, start_pre or start_post) and at most one
// release event from the matching pair: remove_pre/remove_post for a create_*
// acquire, stop_pre/stop_post for a start_* acquire. The resource holder is
// named by [HolderName].
type LifecycleHook struct {
	Name     string                           `json:"name"`
	Resource string                           `json:"resource,omitzero"`
	Events   map[LifecycleEvent]LifecycleExec `json:"events"`
}

// Validate checks one hook on its own. Name and resource key uniqueness across
// the hooks of a command is checked by validateLifecycleHooks, which sees all
// of them.
func (h LifecycleHook) Validate() error {
	// Name and Resource end up inside generated cmdman command names, so they
	// follow the same rule as compose command names.
	if err := validateName("hook", h.Name); err != nil {
		return err
	}
	if len(h.Events) == 0 {
		return fmt.Errorf("hook %q: no lifecycle event is set", h.Name)
	}
	for _, ev := range slices.Sorted(maps.Keys(h.Events)) {
		if err := ev.Validate(); err != nil {
			return fmt.Errorf("hook %q: %w", h.Name, err)
		}
		exec := h.Events[ev]
		if len(exec.Args) == 0 {
			return fmt.Errorf("hook %q: %s: args is empty", h.Name, ev)
		}
		if err := exec.OnError.Validate(); err != nil {
			return fmt.Errorf("hook %q: %s: %w", h.Name, ev, err)
		}
	}
	if h.Resource == "" {
		return nil
	}
	if err := validateName("resource", h.Resource); err != nil {
		return fmt.Errorf("hook %q: %w", h.Name, err)
	}
	return h.validateResourceEvents()
}

func (h LifecycleHook) validateResourceEvents() error {
	var acquires, releases []LifecycleEvent
	for _, ev := range lifecycleEvents {
		if _, ok := h.Events[ev]; !ok {
			continue
		}
		if ev.acquires() {
			acquires = append(acquires, ev)
		} else {
			releases = append(releases, ev)
		}
	}
	switch len(acquires) {
	case 0:
		return fmt.Errorf(
			"hook %q: resource %q: no acquire event; set exactly one of %s",
			h.Name, h.Resource, joinLifecycleEvents(
				LifecycleCreatePre, LifecycleCreatePost, LifecycleStartPre, LifecycleStartPost),
		)
	case 1:
	default:
		return fmt.Errorf(
			"hook %q: resource %q: more than one acquire event (%s); set exactly one",
			h.Name, h.Resource, joinLifecycleEvents(acquires...),
		)
	}
	acquire := acquires[0]
	allowed := acquire.releases()
	for _, ev := range releases {
		if !slices.Contains(allowed, ev) {
			return fmt.Errorf(
				"hook %q: resource %q: %s cannot release a resource acquired at %s (allowed: %s)",
				h.Name, h.Resource, ev, acquire, joinLifecycleEvents(allowed...),
			)
		}
	}
	if len(releases) > 1 {
		return fmt.Errorf(
			"hook %q: resource %q: more than one release event (%s); set at most one",
			h.Name, h.Resource, joinLifecycleEvents(releases...),
		)
	}
	return nil
}

// validateLifecycleHooks validates every hook of one command and rejects
// duplicate hook names and duplicate resource keys, which would map two hooks
// onto the same generated cmdman command name.
func validateLifecycleHooks(hooks []LifecycleHook) error {
	names := make(map[string]struct{}, len(hooks))
	resources := make(map[string]string, len(hooks))
	for i, h := range hooks {
		if h.Name == "" {
			return fmt.Errorf("hooks[%d]: name is required", i)
		}
		if err := h.Validate(); err != nil {
			return err
		}
		if _, dup := names[h.Name]; dup {
			return fmt.Errorf("hook %q: name is used by more than one hook", h.Name)
		}
		names[h.Name] = struct{}{}
		if h.Resource == "" {
			continue
		}
		if other, dup := resources[h.Resource]; dup {
			return fmt.Errorf(
				"hook %q: resource %q is already declared by hook %q",
				h.Name, h.Resource, other,
			)
		}
		resources[h.Resource] = h.Name
	}
	return nil
}

// ExecCommandName returns the cmdman command name under which hook runs ev for
// the replica named replica: "<replica>.hook.<hook>.<event>".
func ExecCommandName(replica, hook string, ev LifecycleEvent) string {
	return replica + ".hook." + hook + "." + string(ev)
}

// HolderName returns the cmdman command name that holds the resource key for
// the replica named replica: "<replica>.res.<key>".
func HolderName(replica, key string) string {
	return replica + ".res." + key
}

// ENV_CMDMAN_COMPOSE_HOOK_NAME is set in a hook command's environment to the
// name of the hook item it runs for.
const ENV_CMDMAN_COMPOSE_HOOK_NAME = "CMDMAN_COMPOSE_HOOK_NAME"

// ENV_CMDMAN_COMPOSE_HOOK_EVENT is set in a hook command's environment to the
// [LifecycleEvent] it runs for.
const ENV_CMDMAN_COMPOSE_HOOK_EVENT = "CMDMAN_COMPOSE_HOOK_EVENT"

// ENV_CMDMAN_COMPOSE_RESOURCE_KEY is set in a resource hook command's
// environment to the resource key of its hook item.
const ENV_CMDMAN_COMPOSE_RESOURCE_KEY = "CMDMAN_COMPOSE_RESOURCE_KEY"

// ENV_CMDMAN_COMPOSE_RESOURCE_VALUE is set in a resource hook command's
// environment to the value recorded for the resource when it was acquired, so
// the release event can dispose of it.
const ENV_CMDMAN_COMPOSE_RESOURCE_VALUE = "CMDMAN_COMPOSE_RESOURCE_VALUE"
