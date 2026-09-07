package commands

import (
	"fmt"
	"io"
	"iter"
)

// reportTargetErrors prints one line per failed target and turns any failure
// into a non-zero exit unless ignore is set.
//
// errs is drained completely: a multi-target verb attempts every target, so the
// failure only surfaces in the exit status once every attempt has been made.
// Callers whose work happens lazily inside the sequence rely on that.
func reportTargetErrors(
	errOut io.Writer,
	verb string,
	ignore bool,
	errs iter.Seq2[string, error],
) error {
	var failed bool
	for id, err := range errs {
		if err == nil {
			continue
		}
		fmt.Fprintf(errOut, "%s %s: %v\n", verb, id, err)
		failed = true
	}
	if failed && !ignore {
		return fmt.Errorf("one or more %s operations failed", verb)
	}
	return nil
}
