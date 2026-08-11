package cmdman

import "os"

func testEnv() []string {
	return append([]string(nil), os.Environ()...)
}
