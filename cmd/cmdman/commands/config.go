package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
)

// configLongFmt documents the resolved-config shape so users can write --format
// templates without reading the source. The %s is filled with the shared
// template-helper docs (cli.TemplateFuncHelp). Keep the field tree in sync with
// config.Config - it is a site on the add-a-field checklist. Help text only: a
// stale line never breaks --format, which runs against the live Config.
const configLongFmt = `config layers defaults < config file < environment, applies the
root flags that were explicitly set (--data-dir, --runtime-dir) on top, and
prints the configuration this invocation would actually use. With no flags it
prints indented JSON; with --format it renders a Go text/template against the
config value instead.

The value passed to --format has this shape (Go field name, type, JSON key):

  Config
  ├─ .DataDir                 string       # database and command dirs  (data_dir)
  ├─ .RuntimeDir              string       # sockets and pid files      (runtime_dir)
  ├─ .DefaultWorkingDir       string       # cwd for new commands       (default_working_dir)
  ├─ .DefaultEnvironment      []string     # env handed to commands     (not serialized)
  ├─ .DefaultScrollbackBytes  int          # scrollback ring size       (default_scrollback_bytes)
  ├─ .DefaultLogDriver        LogDriver    # log driver for commands    (default_log_driver)
  ├─ .EventWatcherKind        WatcherKind  # inotify (linux) or poll    (event_watcher_kind)
  └─ .DefaultHooks            HookSet      # OSC/BEL hooks by event     (default_hooks)

LogDriver and WatcherKind are string types; HookSet is a map keyed by event name.

Use the Go field names in --format (e.g. {{.DataDir}}); the default JSON output
uses the lower-case keys shown in parentheses. DefaultEnvironment is tagged
json:"-", so it never appears in the JSON output - but --format still reaches
it, and it holds the entire process environment. The template also sees these
helper functions:

%s`

const configExample = `  cmdman config
  cmdman config --format '{{.DataDir}}'
  cmdman config --format '{{ json .DefaultHooks }}'`

func configCmd(parent *cobra.Command, rf *rootFlags) {
	var flagFormat string

	cmd := &cobra.Command{
		Use:               "config",
		Short:             "Print the resolved configuration",
		Long:              fmt.Sprintf(configLongFmt, cli.TemplateFuncHelp()),
		Example:           configExample,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfig(cmd, rf, flagFormat)
		},
	}

	cmd.Flags().StringVarP(
		&flagFormat,
		"format",
		"f",
		"",
		"Go text/template rendered against the resolved config instead of JSON",
	)

	parent.AddCommand(cmd)
}

// runConfig loads through loadConfig - the same path every other subcommand
// takes - rather than config.Load alone, so the explicitly-set --data-dir /
// --runtime-dir show up here too: what this prints is what a sibling command
// invoked with the same flags would run with.
func runConfig(cmd *cobra.Command, rf *rootFlags, flagFormat string) error {
	cfg, err := loadConfig(cmd, rf)
	if err != nil {
		return err
	}
	// Presentation lives in cmdman/cli; ./cmd only wires it to stdout.
	// cmd.Println would route to stderr and the output would vanish from a pipe.
	return cli.RenderConfig(cmd.OutOrStdout(), cfg, flagFormat)
}
