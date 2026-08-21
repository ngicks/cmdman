package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ngicks/cmdman/cmdman"
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

// ExitCodeError carries a worker's own exit status out to this process's exit
// status. Whatever the worker printed on its way out has already been streamed
// to the follower's stderr, so the error is not meant to be printed again — the
// only thing left to do with it is exit by it.
type ExitCodeError struct {
	Code int
}

func (e *ExitCodeError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// ExitStatus returns Code clamped into the range a process exit status can
// carry. The monitor records -1 for a run that was cut short rather than
// finishing on its own, which is not a status a process can exit with; it
// becomes a plain failure.
func (e *ExitCodeError) ExitStatus() int {
	if e.Code < 0 || e.Code > 255 {
		return 1
	}
	return e.Code
}

// MuxOpOptions tells [RunMuxOp] how to arrange the detached worker and where to
// put what it prints.
//
// The zero value asks for no worker at all: the operation runs in the invoking
// process, which is what the mux verbs did before any worker existed. Verbs
// that have not been given their identity yet pass it deliberately, so adding
// the identity is what switches a verb over rather than something that can be
// forgotten silently. Asking for a worker with a piece missing is an error, not
// a quiet fall back to running here.
type MuxOpOptions struct {
	// Svc is the cmdman service the worker is registered with, started
	// through, and followed through. Required for a worker.
	Svc *cmdman.Service
	// LogName identifies what the operation acts on. It is the identity
	// component of both the worker's command name and its log file, so it must
	// be derived from the window the operation rebuilds and nothing else: two
	// invocations aimed at the same window must produce the same value.
	// Build it with [MuxOpLogName] or [ComposeMuxOpLogName]. Required for a
	// worker.
	LogName string
	// Argv is the mux verb argv as the user typed it, without the executable.
	// The worker is this binary re-run with exactly these arguments, so it
	// re-reads the spec or compose file and rebuilds its options the way a
	// direct run would. Required for a worker.
	Argv []string
	// Env is the invoker's environment. The worker drives the multiplexer the
	// invoker was looking at, which it can only find through the invoker's
	// $TMUX and the socket it names, so the environment is handed over whole.
	// Empty means this process's own environment.
	Env []string
	// Dir is the directory the worker runs in. Spec paths, compose files and
	// working directories in them are relative to where the user typed the
	// command, so it defaults to this process's working directory.
	Dir string
	// Stdout and Stderr receive the worker's output as it arrives. Empty
	// defaults to this process's own.
	Stdout io.Writer
	Stderr io.Writer
	// InProcess forces the operation to run in the invoking process even when
	// everything a worker needs is present.
	//
	// `mux up` and `mux down` read their spec from stdin when the path is "-"
	// or absent. A detached worker is handed /dev/null for stdin, so it has no
	// way to re-read a spec that only ever existed on the invoking process's
	// stdin; those runs stay here, exactly as they were before, and give up
	// surviving their own pane in exchange.
	InProcess bool
}

// supervised reports whether opts asks for a detached worker. Anything naming
// what the operation acts on does; the untouched zero value does not.
func (o MuxOpOptions) supervised() bool {
	return !o.InProcess && (o.Svc != nil || o.LogName != "" || len(o.Argv) > 0)
}

// resolve fills in the parts that have a sensible local default and reports
// what is missing.
func (o MuxOpOptions) resolve() (MuxOpOptions, error) {
	if o.Svc == nil {
		return o, errors.New("mux op: no cmdman service")
	}
	if o.LogName == "" {
		return o, errors.New("mux op: no log name")
	}
	if len(o.Argv) == 0 {
		return o, errors.New("mux op: no argv")
	}
	if len(o.Env) == 0 {
		o.Env = os.Environ()
	}
	if o.Dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return o, fmt.Errorf("mux op: get working directory: %w", err)
		}
		o.Dir = wd
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return o, nil
}

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
// this process is the worker, and when opts asks for no worker at all.
//
// A worker whose operation failed reports its own exit status as an
// [ExitCodeError].
func RunMuxOp(ctx context.Context, opts MuxOpOptions, op func(context.Context) error) error {
	if os.Getenv(envMuxOp) == "1" {
		// The operation this worker performs registers commands of its own, and
		// a created command records the environment it was born into. The
		// marker belongs to this process's role, not to those commands, so it
		// is dropped before the operation snapshots anything.
		_ = os.Unsetenv(envMuxOp)
		return op(ctx)
	}
	return superviseMuxOp(ctx, opts, op)
}

// MuxOpLogName returns the identity component of a standalone mux operation's
// command name and log file.
//
// identity is the ownership identity the window carries, which defaults to the
// window's name — an arbitrary string the user chose. server is the multiplexer
// server that window lives on: the socket the spec's driver section names, empty
// for the default server. Both are needed to say which window, because an
// identity only means anything within one server: two tmux servers can each hold
// a session called "cmdman", and an operation on one must not be refused because
// an operation is under way on the other.
//
// The two are joined so that no pair of them can be read as another pair, which
// is what keeps two windows off one lock and one log file. Every '-' in either
// half is doubled, so a run of dashes inside a half is always of even length,
// and the halves are then joined by [muxOpNameSeparator] rather than by a bare
// '-': doubling alone leaves the join ambiguous, since a server "work-" beside
// an identity "dev" and a server "work" beside an identity "-dev" would both
// come out as "work---dev".
func MuxOpLogName(server, identity string) string {
	name := escapeMuxOpNamePart(identity)
	if server != "" {
		name = escapeMuxOpNamePart(server) + muxOpNameSeparator + name
	}
	return sanitizeMuxOpName(name)
}

// muxOpNameSeparator joins the server half of a name to the identity half.
//
// The '_' is what makes the join readable back: it ends the dash run the
// separator falls in, and an escaped half's own dash runs are all of even
// length, so the separator is the one dash in the whole name that closes a run
// of odd length. No pair of halves can produce that anywhere else.
const muxOpNameSeparator = "-_"

// escapeMuxOpNamePart doubles every '-' in one component of a name, which is
// what leaves the component's dash runs even and so tells them apart from the
// separator between components.
func escapeMuxOpNamePart(s string) string {
	return strings.ReplaceAll(s, "-", "--")
}

// ComposeMuxOpLogName returns the identity component of a compose project
// dashboard operation's command name and log file. projectIdentity is the
// project's ownership identity (compose.GenerateProjectIdentity), which is
// already a workdir hash and an escaped project name; the "compose-" prefix
// keeps it from colliding with a standalone window that happens to be named the
// same.
func ComposeMuxOpLogName(projectIdentity string) string {
	return sanitizeMuxOpName("compose-" + projectIdentity)
}

// muxOpNameMaxLen caps the identity component. It ends up as a file name
// alongside a prefix and a suffix, and the usual per-component limit is 255
// bytes.
const muxOpNameMaxLen = 120

// muxOpNameDigestLen is how much of the digest is kept when one is needed. It
// is the same width compose uses for its workdir hash.
const muxOpNameDigestLen = 12

// sanitizeMuxOpName makes name usable as a file name. Window and project names
// are whatever the user typed, so anything outside the portable set is replaced
// and an over-long name is cut short.
//
// Both of those map many names onto one, which would hand two different windows
// the same lock and the same log file, so whenever either applies a digest of
// the original is appended: the result stays deterministic per identity and
// stays distinct between identities.
func sanitizeMuxOpName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	replaced := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
			replaced = true
		}
	}
	out := b.String()
	if out == "" {
		// Nothing identifying survived (or there was nothing to begin with);
		// the digest below is then the whole name.
		replaced = true
	}
	if !replaced && len(out) <= muxOpNameMaxLen {
		return out
	}

	sum := sha256.Sum256([]byte(name))
	digest := hex.EncodeToString(sum[:])[:muxOpNameDigestLen]
	if keep := muxOpNameMaxLen - muxOpNameDigestLen - 1; len(out) > keep {
		// Every byte left in out is ASCII by now, so cutting is safe.
		out = out[:keep]
	}
	return out + "-" + digest
}

// muxOpCommandName returns the store command name a mux operation is registered
// under. The name is what keeps two operations off the same window: the store
// refuses a second command by the same name, so the conflict is decided there
// rather than raced between two workers rebuilding the same layout.
func muxOpCommandName(logName string) string {
	return "muxop-" + logName
}
