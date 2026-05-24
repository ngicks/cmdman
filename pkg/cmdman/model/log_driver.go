package model

import "github.com/ngicks/cmdman/pkg/cmdman/logdriver"

// DefaultLogDriver is the log driver used when no explicit value is supplied.
const DefaultLogDriver = logdriver.DriverK8sFile

// IsLogDriver reports whether s is a valid LogDriver value.
func IsLogDriver(s string) bool {
	return logdriver.IsDriver(s)
}
