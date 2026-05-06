package tmux

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ngicks/cmdman/pkg/mux"
)

// pane implements mux.Pane for a tmux pane.
type pane struct {
	id        string
	sessionID string
	windowID  string
	exec      *executor
}

var _ mux.Pane = (*pane)(nil)

func (p *pane) Id() string {
	return p.id
}

func (p *pane) Index(ctx context.Context) (int, error) {
	out, err := p.exec.run(ctx, "display-message", "-t", p.id, "-p", "#{pane_index}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

func (p *pane) Name(ctx context.Context) (string, error) {
	out, err := p.exec.run(ctx, "display-message", "-t", p.id, "-p", "#{pane_title}")
	if err != nil {
		return "", err
	}
	return out, nil
}

// SendKeys sends each key string as a separate tmux send-keys invocation.
// Placeholders (#{SESSION_ID}, #{WINDOW_ID}, #{PANE_ID}, #{INJECT_META}) are
// interpolated before sending. Use ##{...} to produce a literal #{...}.
func (p *pane) SendKeys(ctx context.Context, keys []string) error {
	for key := range interpolateKeys(keys, p.sessionID, p.windowID, p.id) {
		_, err := p.exec.run(ctx, "send-keys", "-t", p.id, key)
		if err != nil {
			return err
		}
	}
	return nil
}

// Capture captures pane content from line `from` for `limit` lines.
func (p *pane) Capture(ctx context.Context, from int, limit int) (string, error) {
	end := from + limit - 1
	out, err := p.exec.run(ctx,
		"capture-pane", "-t", p.id, "-p",
		"-S", fmt.Sprintf("%d", from),
		"-E", fmt.Sprintf("%d", end),
	)
	if err != nil {
		return "", err
	}
	return out, nil
}

func (p *pane) Close(ctx context.Context) error {
	_, err := p.exec.run(ctx, "kill-pane", "-t", p.id)
	return err
}
