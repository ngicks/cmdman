package cmdman

import "github.com/ngicks/cmdman/pkg/cmdman/config"

// CmdmanConfig is an alias for config.CmdmanConfig. The config type and its
// helpers were hoisted into the leaf package pkg/cmdman/config so that both
// pkg/cmdman and pkg/cmdman/monitor can depend on it without an import cycle;
// this alias keeps the pkg/cmdman API surface unchanged for existing callers.
type CmdmanConfig = config.CmdmanConfig

// Environment variable names, re-exported from pkg/cmdman/config.
const (
	ENV_CMDMAN_DATA_DIR     = config.ENV_CMDMAN_DATA_DIR
	ENV_CMDMAN_RUNTIME_DIR  = config.ENV_CMDMAN_RUNTIME_DIR
	ENV_CMDMAN_CMD_DATA_DIR = config.ENV_CMDMAN_CMD_DATA_DIR
	ENV_CMDMAN_CMD_ID       = config.ENV_CMDMAN_CMD_ID
	ENV_CMDMAN_CONF         = config.ENV_CMDMAN_CONF
)

// ComposeConfigDir is re-exported from pkg/cmdman/config; see
// config.ComposeConfigDir.
var ComposeConfigDir = config.ComposeConfigDir
