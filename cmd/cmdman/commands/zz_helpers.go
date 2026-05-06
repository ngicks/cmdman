package commands

import (
	"fmt"
	"strings"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func cmdmanService(rootCfg *cmdman.CmdmanConfig) (*cmdman.Service, error) {
	cfg, err := rootCfg.WithDefaults()
	if err != nil {
		return nil, err
	}
	return cmdman.NewService(cfg), nil
}

func parseLabels(labelSlice []string) (map[string]string, error) {
	if len(labelSlice) == 0 {
		return nil, nil
	}
	labels := make(map[string]string)
	for _, l := range labelSlice {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label format: %s (expected KEY=VALUE)", l)
		}
		labels[k] = v
	}
	return labels, nil
}
