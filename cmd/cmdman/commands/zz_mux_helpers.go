package commands

import (
	"context"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// muxOpOptions is the half of [cli.MuxOpOptions] every mux verb fills the same
// way. The worker is registered with the service this invocation resolved; it is
// this binary run again with the arguments the user typed, in the environment
// they typed them in; and it prints where this command prints.
//
// logName is the verb's own half — what the operation acts on, which is what
// keeps two operations off one window.
func muxOpOptions(cmd *cobra.Command, svc *cmdman.Service, logName string) cli.MuxOpOptions {
	return cli.MuxOpOptions{
		Svc:     svc,
		LogName: logName,
		Argv:    os.Args[1:],
		Env:     os.Environ(),
		Stdout:  cmd.OutOrStdout(),
		Stderr:  cmd.ErrOrStderr(),
	}
}

// muxSpecLogName names the dashboard the spec at path describes: the window
// `mux up` builds and `mux down` restores.
//
// The spec is read here only for its driver section: which multiplexer server to
// ask where we are, and which server the answer belongs to — a session name says
// which window only within one server. A worker carrying the operation out reads
// the file again for everything else, as freshly as a direct run would.
func muxSpecLogName(ctx context.Context, path, session string) (string, error) {
	driver, err := specDriver(path)
	if err != nil {
		return "", err
	}
	identity, err := mux.ResolveIdentity(ctx, mux.IdentityOptions{
		Driver:      driver,
		SessionName: session,
	})
	if err != nil {
		return "", err
	}
	return cli.MuxOpLogName(driver.Socket, identity), nil
}

// composeMuxLogName names the dashboard window a compose mux verb acts on. A
// compose project's identity is its work directory and its name, so it is the
// same string wherever the command is typed from — nothing has to be asked of
// the multiplexer to know it.
//
// Resolving it reads the compose file, which a worker carrying the operation
// out reads again for itself.
func composeMuxLogName(cf *composeFlags) (string, error) {
	selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
	if err != nil {
		return "", err
	}
	return cli.ComposeMuxOpLogName(selection.ProjectIdentity()), nil
}

// muxFrameOpOptions builds the options a frame verb hands [cli.RunMuxOp].
//
// A frame verb acts on the window the caller is pointing at, so that window's
// driver-native id is what names the worker: two runs aimed at one window name
// one worker, and two aimed at different windows never take each other's place.
// When there is no window to point at — outside the multiplexer with no session
// named — there is nothing to name a worker after either, so the operation runs
// here and reports that in its own words.
//
// The frame verbs take no driver options, so the server is always the one $TMUX
// names and there is nothing to tell the name apart by. Window ids are numbered
// per server, so two multiplexer servers running at once can hand out the same
// id for two different windows.
func muxFrameOpOptions(
	cmd *cobra.Command,
	svc *cmdman.Service,
	session string,
) (cli.MuxOpOptions, error) {
	windowID, ok, err := mux.FrameTargetWindow(
		cmd.Context(), mux.FrameOptions{Session: session},
	)
	if err != nil {
		return cli.MuxOpOptions{}, err
	}
	if !ok {
		return cli.MuxOpOptions{InProcess: true}, nil
	}
	return muxOpOptions(cmd, svc, cli.MuxOpLogName("", windowID)), nil
}

// specPathArg is the spec path a mux verb was given: its one positional
// argument, or the stdin default when it was given none.
func specPathArg(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return "-"
}

// specDriver extracts the driver spec (name, path, socket, opts) from the spec
// at path. It is used by runMuxDown, runMuxLs and muxSpecLogName to honour a
// custom socket when one is declared in the spec without requiring the caller to
// resolve the full layout. The stdin default ("-") is treated as no file, so
// teardown/listing uses the default driver rather than blocking on stdin.
func specDriver(path string) (muxctl.DriverSpec, error) {
	if path == "-" {
		return muxctl.DriverSpec{}, nil
	}
	src, closer, err := openSpecSource(path)
	if err != nil {
		return muxctl.DriverSpec{}, err
	}
	defer closer()
	spec, err := mux.Decode(src)
	if err != nil {
		return muxctl.DriverSpec{}, err
	}
	return spec.Driver, nil
}

// openSpecSource opens the spec source. An empty or "-" path reads from stdin
// (returns a no-op closer); anything else opens the file.
//
// A spec read from stdin is the one case a mux operation cannot be handed to a
// detached worker: the worker is given /dev/null for stdin, so a spec that only
// ever existed on the invoking process's stdin is gone by the time the worker
// looks for it. Those runs stay in the invoking process — which is what every
// mux verb did before any worker existed — and give up surviving their own pane
// in exchange. Naming a file is what buys that back.
func openSpecSource(path string) (io.Reader, func(), error) {
	if path == "" || path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}
