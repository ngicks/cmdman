package cmdman_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/ngicks/cmdman/cmdman"
)

// Result is the outcome of one child process. Stdout and Stderr are trimmed;
// for a pty session both streams share the terminal and land in Stdout.
type Result struct {
	Stdout string
	Stderr string
	Err    error
}

// Cmd describes one child-process invocation. It is the only place child
// environments are composed, so no test can drive the binary with an
// environment that differs from every other test's.
type Cmd struct {
	env      *testEnv
	bin      string
	args     []string
	dir      string
	extraEnv []string
	timeout  time.Duration
	stdin    io.Reader
	muxless  bool
}

// execTimeout bounds a single invocation. It is generous because a cold start
// spawns a monitor and opens the store; tests that need a tighter bound say so
// with WithTimeout.
const execTimeout = 90 * time.Second

// Cmd builds an invocation of the cmdman binary under test.
func (e *testEnv) Cmd(args ...string) *Cmd {
	return &Cmd{env: e, bin: cmdmanBin, args: args, timeout: execTimeout}
}

// Tool builds an invocation of some other binary - tmux, /bin/sh - carrying the
// same environment cmdman itself gets, so a cmdman it execs resolves this
// test's store.
func (e *testEnv) Tool(bin string, args ...string) *Cmd {
	return &Cmd{env: e, bin: bin, args: args, timeout: execTimeout}
}

func (c *Cmd) InDir(dir string) *Cmd {
	c.dir = dir
	return c
}

// WithEnv appends variables after the composed base, so they win over it.
func (c *Cmd) WithEnv(kv ...string) *Cmd {
	c.extraEnv = append(c.extraEnv, kv...)
	return c
}

func (c *Cmd) WithTimeout(d time.Duration) *Cmd {
	c.timeout = d
	return c
}

// Muxless additionally strips the variables by which cmdman detects an
// enclosing multiplexer, so the child cannot inherit the developer's own
// tmux/zellij session.
func (c *Cmd) Muxless() *Cmd {
	c.muxless = true
	return c
}

func (c *Cmd) WithStdin(r io.Reader) *Cmd {
	c.stdin = r
	return c
}

func (c *Cmd) environ() []string {
	base := hermeticEnviron()
	if c.muxless {
		base = muxlessEnv()
	}
	return slices.Concat(base, []string{
		cmdman.ENV_CMDMAN_DATA_DIR + "=" + c.env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR + "=" + c.env.runtimeDir,
		cmdman.ENV_CMDMAN_CONF + "=" + c.env.confPath,
	}, c.extraEnv)
}

func (c *Cmd) build(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.bin, c.args...)
	cmd.Dir = c.dir
	cmd.Env = c.environ()
	cmd.Stdin = c.stdin
	// WaitDelay ensures cmd.Wait returns even if spawned child processes hold
	// stdout/stderr pipe FDs open (e.g. the detached monitor).
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

// desc names the invocation for failure messages.
func (c *Cmd) desc() string {
	return strings.TrimSpace(filepath.Base(c.bin) + " " + strings.Join(c.args, " "))
}

// Exec runs the command to completion. It never fails the test; the caller
// decides what the outcome means.
func (c *Cmd) Exec(ctx context.Context) Result {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := c.build(ctx)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
		Err:    err,
	}
}

// Run executes the command and returns its stdout, failing the test if it did
// not succeed.
func (c *Cmd) Run(ctx context.Context, t *testing.T) string {
	t.Helper()
	res := c.Exec(ctx)
	if res.Err != nil {
		t.Fatalf("%s failed: %v\nstderr:\n%s", c.desc(), res.Err, res.Stderr)
	}
	return res.Stdout
}

// ExpectFail executes the command and requires it to fail with every wantStderr
// present in its stderr.
func (c *Cmd) ExpectFail(ctx context.Context, t *testing.T, wantStderr ...string) Result {
	t.Helper()
	res := c.Exec(ctx)
	if res.Err == nil {
		t.Fatalf("%s succeeded unexpectedly; stdout=%q", c.desc(), res.Stdout)
	}
	for _, want := range wantStderr {
		if !strings.Contains(res.Stderr, want) {
			t.Fatalf("%s: expected %q in stderr, got %q", c.desc(), want, res.Stderr)
		}
	}
	return res
}

// Session is a child process running under a pty, with everything it has
// written so far accumulated for inspection.
type Session struct {
	t    *testing.T
	ptmx *os.File

	mu  sync.Mutex
	out bytes.Buffer

	readerDone chan struct{}
	done       chan struct{}
	waitErr    error
}

// StartPTY starts the command on a pty and reads from it until it exits. The
// pty is left at its default size: what attach does with the size it is handed
// is what several tests assert, so the harness must not pick one.
func (c *Cmd) StartPTY(ctx context.Context, t *testing.T) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)

	cmd := c.build(ctx)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		t.Fatalf("start %s under a pty: %v", c.desc(), err)
	}

	s := &Session{
		t:          t,
		ptmx:       ptmx,
		readerDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
	go s.read()
	go func() {
		defer close(s.done)
		s.waitErr = cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = ptmx.Close()
		cancel()
		<-s.done
	})
	return s
}

func (s *Session) read() {
	defer close(s.readerDone)
	b := make([]byte, 8192)
	for {
		n, err := s.ptmx.Read(b)
		if n > 0 {
			chunk := b[:n]
			s.mu.Lock()
			s.out.Write(chunk)
			s.mu.Unlock()
			s.answerProbes(chunk)
		}
		if err != nil {
			return
		}
	}
}

// answerProbes replies to the terminal-capability queries lipgloss/termenv emit
// at startup the way a real terminal would. Unanswered, the probe blocks
// waiting for its response and the program never draws.
func (s *Session) answerProbes(chunk []byte) {
	if bytes.Contains(chunk, []byte("\x1b]11;?")) {
		_, _ = s.ptmx.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
	}
	if bytes.Contains(chunk, []byte("\x1b[6n")) {
		_, _ = s.ptmx.Write([]byte("\x1b[1;1R"))
	}
}

func (s *Session) Send(keys string) {
	s.t.Helper()
	if _, err := s.ptmx.WriteString(keys); err != nil {
		s.t.Fatalf("send %q: %v", keys, err)
	}
}

func (s *Session) Resize(rows, cols int) {
	s.t.Helper()
	size := &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}
	if err := pty.Setsize(s.ptmx, size); err != nil {
		s.t.Fatalf("resize pty to %dx%d: %v", rows, cols, err)
	}
}

// Output is everything the session has written so far.
func (s *Session) Output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// Expect waits for pattern to appear anywhere in the output and returns the
// match.
func (s *Session) Expect(t *testing.T, pattern string, timeout time.Duration) string {
	t.Helper()
	return s.expectFrom(t, 0, pattern, timeout)
}

// ExpectAfter is Expect restricted to output written past off, so a marker the
// stream already carried cannot satisfy a wait for the same marker again.
func (s *Session) ExpectAfter(
	t *testing.T,
	off int,
	pattern string,
	timeout time.Duration,
) string {
	t.Helper()
	return s.expectFrom(t, off, pattern, timeout)
}

func (s *Session) expectFrom(t *testing.T, off int, pattern string, timeout time.Duration) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	deadline := time.Now().Add(timeout)
	for {
		out := s.Output()
		tail := out[min(off, len(out)):]
		if m := re.FindString(tail); m != "" {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; screen:\n%s", pattern, tail)
			return ""
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Wait blocks until the session's process exits. The process is bounded by the
// command timeout, so a session that never exits fails there rather than here.
func (s *Session) Wait(t *testing.T) Result {
	t.Helper()
	<-s.done
	// The reader ends on its own once the child closes the tty; give it that
	// moment so the last thing written is part of the result.
	select {
	case <-s.readerDone:
	case <-time.After(time.Second):
	}
	return Result{Stdout: strings.TrimSpace(s.Output()), Err: s.waitErr}
}

// waitAttachExit reaps a pty command the test started by hand, for the tests
// that have not moved to Session yet.
func waitAttachExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("attach exited with error: %v", err)
		}
	case <-time.After(timeout):
		t.Fatal("attach did not exit")
	}
}
