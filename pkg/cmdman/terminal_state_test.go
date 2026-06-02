package cmdman

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestTerminalPaneState_ReplaysActiveModes(t *testing.T) {
	st := newTerminalPaneState()

	st.Observe([]byte("\x1b[?1000h"))
	st.Observe([]byte("\x1b[?1006;2004h"))
	st.Observe([]byte("\x1b="))

	assert.Equal(t, string(st.Replay()), "\x1b[?1000;1006;2004h\x1b=")
}

func TestTerminalPaneState_TracksChunkedAndResetModes(t *testing.T) {
	st := newTerminalPaneState()

	st.Observe([]byte("\x1b[?100"))
	st.Observe([]byte("0;1006h"))
	st.Observe([]byte("\x1b[?1000l"))

	assert.Equal(t, string(st.Replay()), "\x1b[?1006h")
}

func TestTerminalPaneState_ResetClearsModes(t *testing.T) {
	st := newTerminalPaneState()

	st.Observe([]byte("\x1b[?1000;1006;2004h\x1b="))
	st.Observe([]byte("\x1bc"))

	assert.Equal(t, string(st.Replay()), "")
}

func TestTerminalPaneState_SoftResetClearsModes(t *testing.T) {
	st := newTerminalPaneState()

	st.Observe([]byte("\x1b[?1000;1006;2004h\x1b="))
	st.Observe([]byte("\x1b[!p"))

	assert.Equal(t, string(st.Replay()), "")
}
