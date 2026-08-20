package mux

import (
	"context"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// IdentityOptions configures [ResolveIdentity]. Every field means what the
// field of the same name means to [Run] and [Down], so a caller asking what a
// verb will act on passes the options it is about to hand that verb.
type IdentityOptions struct {
	// Driver selects and configures the multiplexer server. An empty
	// Driver.Name autodetects from Env, exactly as it does for [Run].
	Driver muxctl.DriverSpec
	// SessionName is the session the caller targets. Empty resolves the current
	// session the way [Run] resolves it.
	SessionName string
	// WindowName is the label the window wears. Empty defaults the identity to
	// the resolved session name.
	WindowName string
	// Env is the process env consulted for driver autodetection and for the
	// current-session lookup. Empty defaults to os.Environ().
	Env []string
}

// ResolveIdentity returns the ownership identity a standalone [Run] or [Down]
// with these options would stamp on, or search for. It touches no window: it
// only asks the multiplexer where the caller is.
//
// Standalone mux has nothing context-independent to derive an identity from — a
// spec is not tied to a directory the way a compose project is — so the answer
// is where the caller stands: the session name it resolves to, unless a window
// name says otherwise. The verbs derive it in passing, on their way to the
// window; this is for callers that must name what a verb will act on before
// running it. A compose caller needs none of this — its identity is the project
// identity compose hashes from the work directory, which asks the multiplexer
// nothing.
func ResolveIdentity(ctx context.Context, opts IdentityOptions) (string, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	server, err := resolveServer(ctx, opts.Driver, env)
	if err != nil {
		return "", err
	}
	return currentIdentity(ctx, server, opts.SessionName, opts.WindowName, env), nil
}

// currentIdentity derives the standalone identity from an already-connected
// server. [Down] defaults its own identity through it, so a caller naming ahead
// of time what a teardown will look for and the teardown itself cannot end up
// disagreeing.
func currentIdentity(
	ctx context.Context,
	server muxctl.Server,
	sessionName, windowName string,
	env []string,
) string {
	resolved := resolveSessionName(
		sessionName,
		env,
		func() (string, bool, error) { return server.CurrentSessionName(ctx) },
	)
	return deriveIdentity("", windowName, resolved)
}
