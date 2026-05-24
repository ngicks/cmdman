package k8sfile

import (
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

const (
	// DefaultLogMaxSize is the default active log size limit for k8s-file.
	DefaultLogMaxSize = 5 * 1024 * 1024
	// DefaultLogMaxFile is the default number of active plus archived log files.
	DefaultLogMaxFile = 3
)

func parseLogMaxSizeOption(opts map[string]string) (int64, error) {
	value, ok := opts[logdriver.LogOptMaxSize]
	if !ok {
		return DefaultLogMaxSize, nil
	}
	return logdriver.ParseMaxSize(value)
}

func parseLogMaxFileOption(opts map[string]string) (int, error) {
	value, ok := opts[logdriver.LogOptMaxFile]
	if !ok {
		return DefaultLogMaxFile, nil
	}
	return logdriver.ParseMaxFile(value)
}
