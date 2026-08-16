package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/tui"
	"github.com/ngicks/cmdman/internal/libver"
)

// RunTUI runs the interactive TUI directly in the current terminal, opening
// initialTab on startup. workDir overrides the effective work directory used to
// discover the cwd-active compose project ("" keeps the process CWD).
func RunTUI(ctx context.Context, svc *cmdman.Service, initialTab tui.Tab, workDir string) error {
	return tui.Run(ctx, tui.Options{
		Backend:    newServiceBackend(svc, backendTarget{WorkDir: workDir}),
		Version:    libver.Version,
		AltScreen:  true,
		PopupMode:  false,
		InitialTab: initialTab,
	})
}

// TUIWidgetOptions is what a `cmdman tui widget <name>` invocation carries into
// RunTUIWidget: the widget to run plus the flags that name the project it works
// on.
type TUIWidgetOptions struct {
	// Widget is the widget mode to run. Required: WidgetNone would run the full
	// TUI, which is RunTUI's job.
	Widget tui.Widget
	// WorkDir overrides the effective work directory used to discover the
	// cwd-active compose project ("" keeps the process CWD), as it does for
	// RunTUI. It doubles as the explicit project target.
	WorkDir string
	// NoQuit unbinds the widget's quit keys, which is how a frame pane always
	// runs a widget (V6).
	NoQuit bool
	// MuxToken is the opaque mux window token the caller was summoned from
	// (D10), "" when unset. cmdman never parses it; the driver resolves it.
	MuxToken string
	// File and ProjectName name the compose file and the project, mirroring
	// `cmdman compose`'s -f/-p, for a workdir holding more than one project.
	File        string
	ProjectName string
}

// RunTUIWidget runs a single TUI widget standalone in the current terminal —
// the `cmdman tui widget <name>` entry point, which is also what a frame def's
// `component:` resolves to.
//
// Every widget takes the alternate screen: the switcher owns a whole pane and
// the launcher and the project manager a whole popup window, and all of them
// want it clean.
func RunTUIWidget(ctx context.Context, svc *cmdman.Service, opts TUIWidgetOptions) error {
	return tui.Run(ctx, tui.Options{
		Backend: newServiceBackend(svc, backendTarget{
			WorkDir:     opts.WorkDir,
			MuxToken:    opts.MuxToken,
			File:        opts.File,
			ProjectName: opts.ProjectName,
		}),
		Version:   libver.Version,
		AltScreen: true,
		Widget:    opts.Widget,
		NoQuit:    opts.NoQuit,
	})
}

// RunTUIChild runs the TUI inside a multiplexer popup, reporting startup and
// final status to the launcher over the IPC endpoint at ipcPath. It is the
// implementation of the hidden `cmdman tui __child` subcommand. workDir mirrors
// the launcher's --workdir override ("" keeps the process CWD).
func RunTUIChild(
	ctx context.Context,
	svc *cmdman.Service,
	ipcPath string,
	initialTab tui.Tab,
	workDir string,
) error {
	var enc *json.Encoder
	if ipcPath != "" {
		if conn, err := net.Dial("unix", ipcPath); err == nil {
			defer conn.Close()
			enc = json.NewEncoder(conn)
		}
	}
	send := func(m ipcMessage) {
		if enc != nil {
			_ = enc.Encode(m)
		}
	}
	send(ipcMessage{Kind: ipcStarted})
	err := tui.Run(ctx, tui.Options{
		Backend:    newServiceBackend(svc, backendTarget{WorkDir: workDir}),
		Version:    libver.Version,
		AltScreen:  true,
		PopupMode:  true,
		InitialTab: initialTab,
	})
	if err != nil {
		send(ipcMessage{Kind: ipcError, Error: err.Error()})
		return err
	}
	send(ipcMessage{Kind: ipcDone})
	return nil
}

// PopupChild is what a popup runs: a cmdman invocation of this same binary.
// Args is the subcommand and the flags that belong to it — the root flags every
// child needs (--data-dir/--runtime-dir/--config) are forwarded by the launcher
// rather than named here.
type PopupChild struct {
	// Args is the child argv after the executable, e.g. {"tui", "__child"} or
	// {"tui", "widget", "project-manager", "--file", "..."}.
	Args []string
	// ReportsStatus makes the launcher open its IPC endpoint and pass the child
	// --ipc, then wait for the child's final status over it. Only the full-TUI
	// child speaks that protocol; a widget child owns its popup for its whole
	// life, so its ending is the popup's ending and there is nothing to report.
	ReportsStatus bool
}

// PopupConfig describes how to launch a cmdman child as a multiplexer popup.
type PopupConfig struct {
	// Child is the cmdman invocation the popup runs. Required.
	Child PopupChild
	// Driver is the raw --popup value ("", "true", "tmux", or "zellij").
	// Empty or "true" means infer from the environment.
	Driver string
	// Env is the environment used for driver inference and forwarded to the
	// popup process. Defaults to os.Environ() when nil.
	Env []string
	// Cwd is the working directory forwarded to the popup so active-project
	// detection matches direct mode.
	Cwd string
	// Executable is the path to the cmdman binary launched inside the popup.
	Executable string
	// DataDir and RuntimeDir are forwarded so the popup uses the same store and
	// runtime targets as the launcher. Empty values are not forwarded.
	DataDir    string
	RuntimeDir string
	// ConfPath is the config file path forwarded to the popup: as --config on
	// the child argv, and as $CMDMAN_CONF for the config-directory lookups that
	// only consult the environment (config.ComposeConfigDir, FrameConfigDir).
	// Empty is not forwarded.
	ConfPath string
	// Silent keeps the multiplexer command off the caller's terminal: it is
	// given no stdio, and whatever it wrote to stderr comes back inside the
	// error instead. The `cmdman tui --popup` launcher owns its terminal and
	// leaves this false; a summon from inside a running TUI does not, and a
	// stray "no server running" landing on the rendered view — or a second
	// reader on the same tty — is not something the caller can undo.
	Silent bool
	// Width, Height, X and Y are explicit-percentage geometry values ("80%")
	// forwarded to `tmux display-popup` as -w/-h/-x/-y. Empty values are omitted,
	// leaving tmux's default geometry.
	Width  string
	Height string
	X      string
	Y      string
}

// popupPercentRe matches the explicit-percentage values accepted by the popup
// geometry flags (e.g. "80%"); bare numbers and tmux tokens like "C" are
// rejected.
var popupPercentRe = regexp.MustCompile(`^[0-9]{1,3}%$`)

// PopupGeometry holds the explicit-percentage size/position values forwarded to
// `tmux display-popup` (-w/-h/-x/-y). Empty fields keep tmux's default geometry.
type PopupGeometry struct {
	Width  string
	Height string
	X      string
	Y      string
}

// Validate reports an error when any set field is not an explicit percentage
// ("80%"). Empty fields are allowed: tmux defaults the corresponding dimension.
func (g PopupGeometry) Validate() error {
	for _, f := range []struct{ name, value string }{
		{"--popup-width", g.Width},
		{"--popup-height", g.Height},
		{"--popup-x", g.X},
		{"--popup-y", g.Y},
	} {
		if f.value != "" && !popupPercentRe.MatchString(f.value) {
			return fmt.Errorf(
				"invalid %s %q: want an explicit percentage like 80%%", f.name, f.value)
		}
	}
	return nil
}

// LaunchTUIPopup gathers the launcher's process context (executable path,
// working directory) and starts the popup running the full TUI. It is the entry
// point the cobra command calls; gathering process/env state here keeps ./cmd
// thin. The dirs and confPath come from the caller's already-resolved
// configuration instead, so the popup child is handed what this process runs
// with rather than re-resolving it.
func LaunchTUIPopup(
	ctx context.Context,
	driverValue, dataDir, runtimeDir, confPath string,
	initialTab tui.Tab,
	workDir string,
	geom PopupGeometry,
) error {
	if err := geom.Validate(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("tui: locate executable: %w", err)
	}
	cwd, _ := os.Getwd()
	return RunTUIPopup(ctx, PopupConfig{
		Child:      tuiChildArgs(tabToken(initialTab), workDir),
		Driver:     driverValue,
		Cwd:        cwd,
		Executable: exe,
		DataDir:    dataDir,
		RuntimeDir: runtimeDir,
		ConfPath:   confPath,
		Width:      geom.Width,
		Height:     geom.Height,
		X:          geom.X,
		Y:          geom.Y,
	})
}

// tuiChildArgs is the full-TUI popup child: `tui __child` with the startup tab
// and the --workdir override the launcher itself was given, so the popup
// discovers the same cwd-active compose project. Empty values are omitted
// rather than passed as empty flags, which would override a lower config layer
// with nothing.
func tuiChildArgs(tab, workDir string) PopupChild {
	args := []string{"tui", "__child"}
	if tab != "" {
		args = append(args, "--tab", tab)
	}
	if workDir != "" {
		args = append(args, "--workdir", workDir)
	}
	return PopupChild{Args: args, ReportsStatus: true}
}

// tabToken maps a tui.Tab back to its --tab token so the popup child can be
// launched with the same startup tab. It returns "" for an out-of-range tab.
func tabToken(t tui.Tab) string {
	keys := tui.TabKeys()
	if int(t) < 0 || int(t) >= len(keys) {
		return ""
	}
	return keys[t]
}

// RunTUIPopup opens a multiplexer popup running cfg.Child and returns when the
// popup closes. It is the one popup seam in cmdman (D1/D5): the `cmdman tui
// --popup` launcher runs the full TUI through it and waits for the child's
// final status over a Unix-socket IPC channel, the switcher's summon runs the
// project-manager widget through it, and a driver that grows a popup
// implementation lights both up at once.
func RunTUIPopup(ctx context.Context, cfg PopupConfig) error {
	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}
	driver, err := resolvePopupDriver(cfg.Driver, env)
	if err != nil {
		return err
	}
	switch driver {
	case "tmux":
		return runTmuxPopup(ctx, cfg, env)
	default:
		return fmt.Errorf(
			"tui: popup driver %q is not implemented yet (v1 ships tmux only)",
			driver,
		)
	}
}

// resolvePopupDriver selects the popup backend. Bare/empty values infer from
// the environment; zellij is accepted by inference/selection only to report
// that it is not implemented in v1.
func resolvePopupDriver(value string, env []string) (string, error) {
	driver := value
	if driver == "" || driver == "true" {
		driver = inferMuxDriver(env)
	}
	switch driver {
	case "tmux":
		return "tmux", nil
	case "zellij":
		return "", errors.New("tui: --popup=zellij is not implemented yet (v1 ships tmux only)")
	default:
		return "", fmt.Errorf("tui: unknown popup driver %q", driver)
	}
}

// inferMuxDriver mirrors mux driver inference: prefer an active tmux server,
// then zellij, then fall back to tmux.
func inferMuxDriver(env []string) string {
	if envOf(env, "TMUX") != "" {
		return "tmux"
	}
	if envOf(env, "ZELLIJ") != "" {
		return "zellij"
	}
	return "tmux"
}

func envOf(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}

// childCommand builds the argv for the popup child process: this binary, the
// child's own subcommand and flags, and the root flags every child needs — the
// store and runtime targets and the config file this process resolved, so the
// popup does not re-resolve them from an environment the multiplexer server
// handed it. ipcPath is "" for a child that reports nothing.
func (cfg PopupConfig) childCommand(ipcPath string) []string {
	args := append([]string{cfg.Executable}, cfg.Child.Args...)
	if ipcPath != "" {
		args = append(args, "--ipc", ipcPath)
	}
	if cfg.DataDir != "" {
		args = append(args, "--data-dir", cfg.DataDir)
	}
	if cfg.RuntimeDir != "" {
		args = append(args, "--runtime-dir", cfg.RuntimeDir)
	}
	if cfg.ConfPath != "" {
		args = append(args, "--config", cfg.ConfPath)
	}
	return args
}

// tmuxPopupArgs builds the `tmux display-popup` argv: -E, an optional working
// directory (-d), any set geometry values (-w/-h/-x/-y), and finally the shell
// command to run inside the popup. Empty geometry values are omitted so tmux
// keeps its default.
func tmuxPopupArgs(cfg PopupConfig, cmdStr string) []string {
	args := []string{"display-popup", "-E"}
	if cfg.Cwd != "" {
		args = append(args, "-d", cfg.Cwd)
	}
	for _, f := range []struct{ flag, value string }{
		{"-w", cfg.Width},
		{"-h", cfg.Height},
		{"-x", cfg.X},
		{"-y", cfg.Y},
	} {
		if f.value != "" {
			args = append(args, f.flag, f.value)
		}
	}
	args = append(args, cmdStr)
	return args
}

func runTmuxPopup(ctx context.Context, cfg PopupConfig, env []string) error {
	var (
		ipcPath   string
		childErr  = make(chan error, 1)
		waitChild = func() error { return nil }
	)
	if cfg.Child.ReportsStatus {
		path, ln, cleanup, err := newIPCEndpoint()
		if err != nil {
			return err
		}
		defer cleanup()
		ipcPath = path
		go func() { childErr <- waitForChild(ln) }()
		waitChild = func() error {
			// Unblock the IPC accept if the child never connected.
			_ = ln.Close()
			return <-childErr
		}
	}

	cmdStr := shellJoin(cfg.childCommand(ipcPath))
	args := tmuxPopupArgs(cfg, cmdStr)

	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.Env = env
	if cfg.ConfPath != "" {
		cmd.Env = append(cmd.Env, "CMDMAN_CONF="+cfg.ConfPath)
	}
	var diag bytes.Buffer
	if cfg.Silent {
		cmd.Stderr = &diag
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	tmuxErr := cmd.Run()
	if err := waitChild(); err != nil {
		return err
	}
	if tmuxErr != nil {
		return fmt.Errorf("tui: tmux popup failed: %w", popupDiag(tmuxErr, diag.String()))
	}
	return nil
}

// popupDiag folds what tmux said into the error a silent caller reports, since
// nothing else read its stderr — "no current client" is the whole answer to why
// no popup opened, and an exit status alone is not.
func popupDiag(err error, stderr string) error {
	if s := strings.TrimSpace(stderr); s != "" {
		return fmt.Errorf("%s (%w)", s, err)
	}
	return err
}

// ipcMessage is the small launcher<->child control payload. Normal rendered UI
// never travels over this channel.
type ipcMessage struct {
	Kind  string `json:"kind"`
	Error string `json:"error,omitzero"`
}

const (
	ipcStarted = "started"
	ipcDone    = "done"
	ipcError   = "error"
)

// newIPCEndpoint creates a user-only Unix-domain socket for popup launcher IPC.
func newIPCEndpoint() (path string, ln net.Listener, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "cmdman-tui-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("tui: create ipc dir: %w", err)
	}
	sockPath := filepath.Join(dir, "ipc.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, nil, fmt.Errorf("tui: listen ipc: %w", err)
	}
	_ = os.Chmod(sockPath, 0o600)
	cleanup = func() {
		_ = l.Close()
		_ = os.RemoveAll(dir)
	}
	return sockPath, l, cleanup, nil
}

// waitForChild accepts the child connection and reads control messages until
// the connection closes, returning any reported error.
func waitForChild(ln net.Listener) error {
	conn, err := ln.Accept()
	if err != nil {
		return nil // listener closed before the child connected
	}
	defer conn.Close()
	dec := json.NewDecoder(conn)
	var finalErr error
	for {
		var m ipcMessage
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m.Kind == ipcError {
			finalErr = errors.New(m.Error)
		}
	}
	return finalErr
}

// shellJoin quotes argv into a single POSIX shell command string.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
