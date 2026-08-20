package cli

import (
	"context"
	"os"
)

// envMuxOp marks a re-exec'd cmdman process as the worker carrying out a mux
// operation, the same way the monitor's daemon marker tells the final detached
// monitor apart from the intermediate that spawned it.
//
// The worker is this binary run again with the arguments the user typed, so the
// marker is the only thing separating the two roles: set means "you are the
// worker, do the work"; unset means "you are the invocation the user typed,
// arrange for a worker and follow it".
const envMuxOp = "__CMDMAN_INTERNAL_MUXOP"

// RunMuxOp carries out one mux operation that may destroy the pane it was
// invoked from, and is how every such verb is reached from the command line.
//
// Bringing a dashboard up, tearing one down, cycling a replica and replacing a
// frame all rebuild the window the command was typed in, so the pane running
// the command can be closed while the operation is still going. Everything
// after that point never happens: further windows are left untouched, the tmux
// options cmdman set are left behind, and the user is left with a half-applied
// dashboard and no error to show for it.
//
// The way out is for the operation not to live in that pane at all. A detached
// worker process performs it from start to finish, and the invocation the user
// typed only follows the worker's output and reports what it did. The worker is
// this same binary re-run with the same arguments and envMuxOp set, so it
// re-reads the spec or compose file and rebuilds its options exactly as a
// direct run would — options are never handed over as data, because they carry
// live values (the cmdman service seam, the writer the output goes to) that do
// not survive a process boundary.
//
// op is the worker's half of that split: it is called here when the marker says
// this process is the worker. A following process runs it today only because
// the worker spawn is not in place yet.
func RunMuxOp(ctx context.Context, op func(context.Context) error) error {
	if os.Getenv(envMuxOp) == "1" {
		return op(ctx)
	}
	return superviseMuxOp(ctx, op)
}

// superviseMuxOp is the single place the detached worker plugs into: spawning
// it, streaming its output and surfacing its exit status all belong here.
//
// Until that lands it performs op in this process, which is exactly what the
// mux verbs did before, so routing them through [RunMuxOp] changes no behavior
// on its own.
func superviseMuxOp(ctx context.Context, op func(context.Context) error) error {
	return op(ctx)
}
