package cmdman_test

import (
	"strings"
	"testing"
	"time"
)

func TestSendKeys_RunningCommandReceivesInput(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "send-keys-read", "--", "/bin/sh", "-c",
		`read line; printf "<%s>\n" "$line"; sleep 300`)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "send-keys-read", "started", defaultTimeout)

	env.run(ctx, "send-keys", "send-keys-read", "hello world", "Enter")

	waitUntil(t, defaultTimeout, func() bool {
		stdout, _, err := env.exec(ctx, "logs", "send-keys-read")
		return err == nil && strings.Contains(stdout, "<hello world>")
	}, "send-keys output did not appear in logs")
}

func TestSendKeys_LiteralModeSendsRawText(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "send-keys-literal", "--", "/bin/sh", "-c",
		`read line; printf "<%s>\n" "$line"; sleep 300`)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "send-keys-literal", "started", defaultTimeout)

	env.run(ctx, "send-keys", "-l", "send-keys-literal", "Enter")
	time.Sleep(200 * time.Millisecond)

	stdout := env.run(ctx, "logs", "send-keys-literal")
	if strings.Contains(stdout, "<>") {
		t.Fatalf("literal send unexpectedly triggered Enter semantics:\n%s", stdout)
	}

	env.run(ctx, "send-keys", "send-keys-literal", "Enter")
	waitUntil(t, defaultTimeout, func() bool {
		stdout, _, err := env.exec(ctx, "logs", "send-keys-literal")
		return err == nil && strings.Contains(stdout, "<Enter>")
	}, "literal send-keys text did not appear in logs")
}
