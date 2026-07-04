package commands

import (
	"io"
	"os"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

// specDriverOpts extracts the driver and driver_opt fields from the spec at
// path. It is used by runMuxDown and runMuxLs to honour a custom socket when
// one is declared in the spec without requiring the caller to resolve the full
// layout. The stdin default ("-") is treated as no file, so teardown/listing
// uses the default driver rather than blocking on stdin.
func specDriverOpts(path string) (driver string, driverOpt map[string]string, err error) {
	if path == "-" {
		return "", nil, nil
	}
	src, closer, err := openSpecSource(path)
	if err != nil {
		return "", nil, err
	}
	defer closer()
	spec, err := mux.Decode(src)
	if err != nil {
		return "", nil, err
	}
	return spec.Driver, spec.DriverOpt, nil
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
