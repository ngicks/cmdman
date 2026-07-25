package tmux

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestExecutorBuildArgs pins the path-aware socket flag selection (D7): a socket
// value containing a path separator is a socket file path (tmux -S <path>), a
// bare name selects a named server (tmux -L <name>), and empty selects the
// default server (no flag). The bare-name and empty forms must stay byte-for-byte
// identical to the pre-path behavior.
func TestExecutorBuildArgs(t *testing.T) {
	base := []string{"list-panes", "-t", "@1"}
	sockPath := filepath.Join("tmp", "cmdman.sock") // contains os.PathSeparator
	for _, tc := range []struct {
		name   string
		socket string
		want   []string
	}{
		{"empty selects the default server, no flag", "", base},
		{"bare name selects -L named server", "cmdman", append([]string{"-L", "cmdman"}, base...)},
		{"path value selects -S socket file", sockPath, append([]string{"-S", sockPath}, base...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newExecutor("", tc.socket).buildArgs(base)
			if !slices.Equal(got, tc.want) {
				t.Errorf("buildArgs(socket=%q) = %v, want %v", tc.socket, got, tc.want)
			}
		})
	}
}
