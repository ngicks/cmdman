package cli

import (
	"fmt"
	"io"
)

// PrintResourceValue writes the value of a compose resource as one line.
func PrintResourceValue(w io.Writer, value string) error {
	_, err := fmt.Fprintln(w, value)
	return err
}
