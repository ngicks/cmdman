package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/moby/term"
	cmdsignals "github.com/ngicks/crabswarm/cmd/internal/signals"
	"github.com/ngicks/crabswarm/pkg/cmdman"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(attachCmd)
	attachCmd.Flags().Bool("no-stdin", false, "Output-only mode")
	attachCmd.Flags().Bool("sig-proxy", true, "Forward signals to command")
	attachCmd.Flags().String("detach-keys", "ctrl-p,ctrl-q", "Key sequence to detach")
}

var attachCmd = &cobra.Command{
	Use:   "attach [flags] ID|NAME",
	Short: "Attach to a running command's PTY",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAttach(cmd, args[0])
	},
}

// signals forwarded to the remote command during attach.
var forwardedSignals = []os.Signal{
	syscall.SIGINT,
	syscall.SIGTERM,
	syscall.SIGQUIT,
	syscall.SIGUSR1,
	syscall.SIGUSR2,
	syscall.SIGTSTP,
	syscall.SIGCONT,
	syscall.SIGWINCH,
}

func runAttach(cmd *cobra.Command, idOrName string) error {
	noStdin, _ := cmd.Flags().GetBool("no-stdin")
	sigProxy, _ := cmd.Flags().GetBool("sig-proxy")
	detachKeysStr, _ := cmd.Flags().GetString("detach-keys")

	detachKeys, err := parseDetachKeys(detachKeysStr)
	if err != nil {
		return fmt.Errorf("invalid detach-keys: %w", err)
	}

	svc, err := cmdmanService()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	attachCtx, cancelAttach := context.WithCancel(ctx)
	defer cancelAttach()

	session, err := svc.OpenAttachSession(attachCtx, idOrName)
	if err != nil {
		return err
	}
	defer session.Close()

	// Put terminal into raw mode so keystrokes pass through to the remote PTY.
	var (
		stdinFd         int
		savedState      *term.State
		restoreTerminal = func() {}
	)
	if !noStdin {
		stdinFd = int(os.Stdin.Fd())
		if term.IsTerminal(uintptr(stdinFd)) {
			oldState, err := term.SetRawTerminal(uintptr(stdinFd))
			if err == nil {
				savedState = oldState

				// Some call chain exits by os.Exit,
				// which forcefully terminates the processes
				// without invoking deferred functions.
				// In case of panic, we defer this func but
				// also wrapping it in sync.Once
				// to allow racy callers.
				restoreTerminal = sync.OnceFunc(func() {
					if savedState != nil {
						_ = term.RestoreTerminal(uintptr(stdinFd), savedState)
					}
					// stdin and stdout can differ in odd half-interactive setups
					// such as `cmd attach ID | tee out.log`: stdin is still the
					// user's tty, so termios restore is valid, while stdout is a
					// pipe, so writing display-reset escapes there would just emit
					// junk into the pipeline.
					if term.IsTerminal(os.Stdout.Fd()) {
						restoreDisplayModes(os.Stdout)
					}
				})
				defer restoreTerminal()
			}
		}
	}

	// We can't do simply `signal.Reset()` without any argument
	// since gRPC handles SIGPIPE.
	signal.Reset(cmdsignals.ExitSignals[:]...)

	// Send initial terminal size.
	sendResize(session)

	// HandleAllSignals: forward signals to remote command, handle SIGWINCH
	// locally as resize, and force-exit after 3 consecutive SIGINT/SIGTERM.
	if sigProxy {
		sigCh := make(chan os.Signal, 4)
		signal.Notify(sigCh, forwardedSignals...)
		defer signal.Stop(sigCh)

		go handleAllSignals(attachCtx, sigCh, session, restoreTerminal)
	}

	// Read from stream -> stdout.
	errCh := make(chan error, 2)
	go func() {
		for {
			data, err := session.Recv()
			if err != nil {
				errCh <- err
				return
			}
			os.Stdout.Write(data)
		}
	}()

	// Read stdin -> stream, with detach key detection.
	if !noStdin {
		go func() {
			var r io.Reader = os.Stdin
			if len(detachKeys) > 0 {
				r = term.NewEscapeProxy(os.Stdin, detachKeys)
			}
			buf := make([]byte, 32*1024)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					data := make([]byte, n)
					copy(data, buf[:n])
					if sendErr := session.SendStdin(data); sendErr != nil {
						errCh <- sendErr
						return
					}
				}
				if err != nil {
					if err != io.EOF {
						errCh <- err
					}
					return
				}
			}
		}()
	}

	// Wait for either direction to finish.
	var exitErr error
	select {
	case err := <-errCh:
		var escapeErr term.EscapeError
		if err != io.EOF && !errors.As(err, &escapeErr) {
			exitErr = err
		}
	case <-ctx.Done():
	}

	// Cancel the attach RPC so detach does not depend on transport-side
	// half-close propagation, then best-effort close the send side.
	cancelAttach()
	session.CloseSend()
	restoreTerminal()

	return exitErr
}

func parseDetachKeys(detachKeys string) ([]byte, error) {
	if detachKeys == "" {
		return nil, nil
	}
	return term.ToBytes(strings.ToLower(detachKeys))
}

// handleAllSignals processes signals during attach:
//   - SIGWINCH → send resize event
//   - SIGINT/SIGTERM → forward to remote; after 3 consecutive, force exit with terminal restore
//   - All others → forward to remote
func handleAllSignals(
	ctx context.Context,
	sigCh <-chan os.Signal,
	session *cmdman.Session,
	restoreTerminal func(),
) {
	forceCount := 0
	for {
		select {
		case sig, ok := <-sigCh:
			if !ok {
				return
			}
			sigNum, ok := sig.(syscall.Signal)
			if !ok {
				continue
			}

			if sigNum == syscall.SIGWINCH {
				sendResize(session)
				forceCount = 0
				continue
			}

			// Forward to remote command.
			_ = session.Signal(ctx, int32(sigNum))

			// Count consecutive SIGINT/SIGTERM for forced exit.
			if sigNum == syscall.SIGINT || sigNum == syscall.SIGTERM {
				forceCount++
				if forceCount >= 3 {
					restoreTerminal()
					os.Exit(1)
				}
			} else {
				forceCount = 0
			}

		case <-ctx.Done():
			return
		}
	}
}

func sendResize(session *cmdman.Session) {
	rows, cols := getTerminalSize()
	if rows > 0 && cols > 0 {
		_ = session.Resize(rows, cols)
	}
}

func getTerminalSize() (rows, cols int) {
	return getTerminalSizeImpl()
}

// restoreDisplayModes resets tty-driven display state that the attached program
// may have left behind. Terminal state restore only restores termios, not
// screen modes.
// This remains heuristic: attach is cleaning up after arbitrary programs whose
// terminal feature set we do not control or track.
//
// Side note: Bubble Tea's (*Program).restoreTerminal is narrower and
// state-driven. It only emits cleanup for modes Bubble Tea knows it enabled,
// for example \033[?1049l when alt screen was active, \033[?2004l when
// bracketed paste was enabled, and mouse-disable sequences when mouse mode was
// enabled. attach differs because it is cleaning up after arbitrary remote
// programs whose terminal state it did not enable and cannot observe, so it
// falls back to a broader unconditional best-effort cleanup sequence.
func restoreDisplayModes(w io.Writer) {
	_, _ = io.WriteString(w, displayModeResetSeq)
}

const displayModeResetSeq = "" +
	"\033[0m" + // Reset SGR (colors/bold).
	"\033[?25h" + // Show cursor.
	"\033[?1l" + // Leave application cursor-key mode.
	"\033[?1000l" + // Disable normal mouse tracking.
	"\033[?1002l" + // Disable button-event mouse tracking.
	"\033[?1003l" + // Disable any-event mouse tracking.
	"\033[?1004l" + // Disable focus reporting.
	"\033[?1006l" + // Disable SGR mouse reporting.
	"\033[?1015l" + // Disable urxvt mouse reporting.
	"\033[?2004l" + // Disable bracketed paste.
	"\033[?1049l" + // Leave the alternate screen buffer.
	"\033>\r\n" // Leave application keypad mode and move to a fresh shell line.
