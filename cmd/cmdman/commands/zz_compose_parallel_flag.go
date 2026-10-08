package commands

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// composeParallelFlagName is the --parallel flag of the compose group.
const composeParallelFlagName = "parallel"

// envComposeParallelLimit gives the limit when --parallel is not set.
const envComposeParallelLimit = "CMDMAN_COMPOSE_PARALLEL_LIMIT"

// defaultComposeParallel is the limit when neither --parallel nor
// envComposeParallelLimit gives one.
const defaultComposeParallel = 4

// addComposeParallelFlag registers --parallel on flags, bound to parallel.
func addComposeParallelFlag(flags *pflag.FlagSet, parallel *int) {
	flags.IntVar(parallel, composeParallelFlagName, defaultComposeParallel,
		"Most replicas one stop, down or restart stops at once, or -1 for no limit;"+
			" $"+envComposeParallelLimit+" sets it when the flag is not given")
}

// composeParallelOption returns the option that bounds the replica stops of a
// compose service to the --parallel value cmd was given. Without the flag it
// reads envComposeParallelLimit, and without that it takes the default.
func composeParallelOption(cmd *cobra.Command, cf *composeFlags) (compose.ServiceOption, error) {
	if cmd.Flags().Changed(composeParallelFlagName) {
		if err := checkComposeParallel(cf.Parallel); err != nil {
			return nil, fmt.Errorf("--%s: %w", composeParallelFlagName, err)
		}
		return compose.WithParallelLimit(cf.Parallel), nil
	}
	raw, ok := os.LookupEnv(envComposeParallelLimit)
	if !ok || raw == "" {
		return compose.WithParallelLimit(defaultComposeParallel), nil
	}
	n, err := strconv.Atoi(raw)
	if err == nil {
		err = checkComposeParallel(n)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envComposeParallelLimit, err)
	}
	return compose.WithParallelLimit(n), nil
}

// checkComposeParallel accepts a positive limit or -1. 0 is refused rather than
// read as no limit, so a mistyped value cannot lift the limit.
func checkComposeParallel(n int) error {
	if n == 0 || n < -1 {
		return fmt.Errorf("must be positive, or -1 for no limit: %d", n)
	}
	return nil
}
