package commands

import (
	"io"
	"os"

	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// specDriver extracts the driver spec (name, path, socket, opts) from the spec
// at path. It is used by runMuxDown and runMuxLs to honour a custom socket when
// one is declared in the spec without requiring the caller to resolve the full
// layout. The stdin default ("-") is treated as no file, so teardown/listing
// uses the default driver rather than blocking on stdin.
func specDriver(path string) (muxctl.DriverSpec, error) {
	if path == "-" {
		return muxctl.DriverSpec{}, nil
	}
	src, closer, err := openSpecSource(path)
	if err != nil {
		return muxctl.DriverSpec{}, err
	}
	defer closer()
	spec, err := mux.Decode(src)
	if err != nil {
		return muxctl.DriverSpec{}, err
	}
	return spec.Driver, nil
}

// openSpecSource opens the spec source. An empty or "-" path reads from stdin
// (returns a no-op closer); anything else opens the file.
//
// A spec read from stdin is the one case a mux operation cannot be handed to a
// detached worker: the worker is given /dev/null for stdin, so a spec that only
// ever existed on the invoking process's stdin is gone by the time the worker
// looks for it. Those runs stay in the invoking process — which is what every
// mux verb did before any worker existed — and give up surviving their own pane
// in exchange. Naming a file is what buys that back.
func openSpecSource(path string) (io.Reader, func(), error) {
	if path == "" || path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}
