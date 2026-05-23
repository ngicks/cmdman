package cmdman

import "strings"

func withCommandContextEnv(env []string, cfg CmdmanConfig, id, commandDir string) []string {
	prefixes := []string{
		ENV_CMDMAN_DATA_DIR + "=",
		ENV_CMDMAN_RUNTIME_DIR + "=",
		ENV_CMDMAN_CMD_DATA_DIR + "=",
		ENV_CMDMAN_CMD_ID + "=",
	}

	out := make([]string, 0, len(env)+len(prefixes))
	for _, entry := range env {
		if hasAnyPrefix(entry, prefixes) {
			continue
		}
		out = append(out, entry)
	}
	return append(
		out,
		ENV_CMDMAN_DATA_DIR+"="+cfg.DataDir,
		ENV_CMDMAN_RUNTIME_DIR+"="+cfg.RuntimeDir,
		ENV_CMDMAN_CMD_DATA_DIR+"="+commandDir,
		ENV_CMDMAN_CMD_ID+"="+id,
	)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
