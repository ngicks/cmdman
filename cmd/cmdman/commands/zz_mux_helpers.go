package commands

import (
	"io"
	"os"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
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
