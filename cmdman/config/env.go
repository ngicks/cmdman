package config

import (
	"strings"

	"github.com/ngicks/cmdman/cmdman/model"
)

const (
	// ENV_CMDMAN_HOOK_EVENT names the event kind that triggered a hook run
	// (see model.HookEvent).
	ENV_CMDMAN_HOOK_EVENT = "CMDMAN_HOOK_EVENT"
	// ENV_CMDMAN_HOOK_TITLE carries the window title for a title event and the
	// notification title for a notification event. It is empty otherwise.
	ENV_CMDMAN_HOOK_TITLE = "CMDMAN_HOOK_TITLE"
	// ENV_CMDMAN_HOOK_BODY carries a notification's message. It is empty for
	// every other event.
	ENV_CMDMAN_HOOK_BODY = "CMDMAN_HOOK_BODY"
)

// HookEventEnv is the event data a hook process receives. Fields the event does
// not carry are exported as empty strings, so a hook script can read all three
// without checking whether they exist.
type HookEventEnv struct {
	Event model.HookEvent
	Title string
	Body  string
}

// WithHookEventEnv strips any caller-supplied CMDMAN_HOOK_* variables from env
// and appends the ones describing the event that triggered the hook. env is
// expected to be the supervised command's own environment, so a hook already
// carries the ENV_CMDMAN_* command context added by [WithCommandContextEnv].
func WithHookEventEnv(env []string, ev HookEventEnv) []string {
	prefixes := []string{
		ENV_CMDMAN_HOOK_EVENT + "=",
		ENV_CMDMAN_HOOK_TITLE + "=",
		ENV_CMDMAN_HOOK_BODY + "=",
	}

	out := make([]string, 0, len(env)+len(prefixes))
	for _, entry := range env {
		if hasAnyPrefix(entry, prefixes) {
			continue
		}
		out = append(out, entry)
	}
	return append(
		out,
		ENV_CMDMAN_HOOK_EVENT+"="+string(ev.Event),
		ENV_CMDMAN_HOOK_TITLE+"="+ev.Title,
		ENV_CMDMAN_HOOK_BODY+"="+ev.Body,
	)
}

// WithCommandContextEnv strips any caller-supplied ENV_CMDMAN_* variables from
// env and appends the ones describing the command's own context (data/runtime
// dirs, per-command dir, and id).
func WithCommandContextEnv(env []string, cfg CmdmanConfig, id, commandDir string) []string {
	prefixes := []string{
		ENV_CMDMAN_DATA_DIR + "=",
		ENV_CMDMAN_RUNTIME_DIR + "=",
		ENV_CMDMAN_CMD_DATA_DIR + "=",
		ENV_CMDMAN_CMD_ID + "=",
	}

	out := make([]string, 0, len(env)+len(prefixes))
	for _, entry := range env {
		if hasAnyPrefix(entry, prefixes) {
			continue
		}
		out = append(out, entry)
	}
	return append(
		out,
		ENV_CMDMAN_DATA_DIR+"="+cfg.DataDir,
		ENV_CMDMAN_RUNTIME_DIR+"="+cfg.RuntimeDir,
		ENV_CMDMAN_CMD_DATA_DIR+"="+commandDir,
		ENV_CMDMAN_CMD_ID+"="+id,
	)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
