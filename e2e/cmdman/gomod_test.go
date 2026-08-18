package cmdman_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoModIsInstallable guards `go install github.com/ngicks/cmdman/cmd/cmdman@<version>`:
// the go command refuses to install a module whose go.mod carries replace or
// exclude directives. Patched dependencies are folded into the module under
// internal/third_party instead (see internal/third_party/README.md).
func TestGoModIsInstallable(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		trimmed := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(trimmed, "replace") || strings.HasPrefix(trimmed, "exclude") {
			t.Errorf(
				"go.mod line %d: directive breaks `go install ...@<version>`: %s",
				line, sc.Text(),
			)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}
