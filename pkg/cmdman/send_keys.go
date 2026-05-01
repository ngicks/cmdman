package cmdman

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cmdmanv1pb "github.com/ngicks/crabswarm/pkg/api/gen/proto/go/cmdman/v1"
)

// SendKeysRequest defines a send-keys operation against a running command PTY.
type SendKeysRequest struct {
	Keys        []string
	Literal     bool
	Hex         bool
	RepeatCount int
}

type specialKey struct {
	seq             []byte
	allowModifiers  bool
	allowShiftAlias bool
}

// sendKeysNamedKeys follows tmux send-keys/key-string names as the de facto CLI
// convention for terminal key tokens. The authoritative upstream references are:
// https://github.com/tmux/tmux/blob/master/cmd-send-keys.c
// https://github.com/tmux/tmux/blob/master/key-string.c
//
// This table maps the recognized tmux-style key names to the PTY byte
// sequences cmdman emits. For terminal function and navigation keys, cmdman
// uses common ANSI/xterm-compatible escape sequences because, unlike tmux,
// cmdman writes bytes to a PTY rather than injecting tmux-internal key codes.
var sendKeysNamedKeys = map[string]specialKey{
	"space":     {seq: []byte{' '}, allowModifiers: true},
	"bspace":    {seq: []byte{0x7f}, allowModifiers: true},
	"backspace": {seq: []byte{0x7f}, allowModifiers: true},
	"tab":       {seq: []byte{'\t'}, allowModifiers: true, allowShiftAlias: true},
	"btab":      {seq: []byte("\x1b[Z")},
	"enter":     {seq: []byte{'\r'}, allowModifiers: true},
	"return":    {seq: []byte{'\r'}, allowModifiers: true},
	"escape":    {seq: []byte{0x1b}, allowModifiers: true},
	"esc":       {seq: []byte{0x1b}, allowModifiers: true},
	"up":        {seq: []byte("\x1b[A"), allowModifiers: true},
	"down":      {seq: []byte("\x1b[B"), allowModifiers: true},
	"right":     {seq: []byte("\x1b[C"), allowModifiers: true},
	"left":      {seq: []byte("\x1b[D"), allowModifiers: true},
	"home":      {seq: []byte("\x1b[H"), allowModifiers: true},
	"end":       {seq: []byte("\x1b[F"), allowModifiers: true},
	"insert":    {seq: []byte("\x1b[2~"), allowModifiers: true},
	"ic":        {seq: []byte("\x1b[2~"), allowModifiers: true},
	"delete":    {seq: []byte("\x1b[3~"), allowModifiers: true},
	"dc":        {seq: []byte("\x1b[3~"), allowModifiers: true},
	"pagedown":  {seq: []byte("\x1b[6~"), allowModifiers: true},
	"pgdn":      {seq: []byte("\x1b[6~"), allowModifiers: true},
	"npage":     {seq: []byte("\x1b[6~"), allowModifiers: true},
	"pageup":    {seq: []byte("\x1b[5~"), allowModifiers: true},
	"pgup":      {seq: []byte("\x1b[5~"), allowModifiers: true},
	"ppage":     {seq: []byte("\x1b[5~"), allowModifiers: true},
	"f1":        {seq: []byte("\x1bOP")},
	"f2":        {seq: []byte("\x1bOQ")},
	"f3":        {seq: []byte("\x1bOR")},
	"f4":        {seq: []byte("\x1bOS")},
	"f5":        {seq: []byte("\x1b[15~")},
	"f6":        {seq: []byte("\x1b[17~")},
	"f7":        {seq: []byte("\x1b[18~")},
	"f8":        {seq: []byte("\x1b[19~")},
	"f9":        {seq: []byte("\x1b[20~")},
	"f10":       {seq: []byte("\x1b[21~")},
	"f11":       {seq: []byte("\x1b[23~")},
	"f12":       {seq: []byte("\x1b[24~")},
}

// EncodeSendKeys converts key arguments into PTY input bytes.
func EncodeSendKeys(req SendKeysRequest) ([]byte, error) {
	repeat := req.RepeatCount
	if repeat <= 0 {
		repeat = 1
	}

	var data []byte
	for range repeat {
		for _, key := range req.Keys {
			b, err := encodeOneKey(key, req)
			if err != nil {
				return nil, err
			}
			data = append(data, b...)
		}
	}
	return data, nil
}

func encodeOneKey(key string, req SendKeysRequest) ([]byte, error) {
	switch {
	case req.Hex:
		n, err := strconv.ParseUint(key, 16, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid hex byte %q: %w", key, err)
		}
		return []byte{byte(n)}, nil
	case req.Literal:
		return []byte(key), nil
	default:
		if b, ok := parseKeyToken(key); ok {
			return b, nil
		}
	}
	return []byte(key), nil
}

func parseKeyToken(s string) ([]byte, bool) {
	if s == "" {
		return nil, false
	}
	if len(s) >= 2 && s[0] == '^' {
		if len(s) == 2 {
			if b, ok := ctrlASCIIByte(rune(s[1])); ok {
				return []byte{b}, true
			}
		}
		s = "C-" + s[1:]
	}

	mods, rest, ok := parseKeyModifiers(s)
	if !ok || rest == "" {
		return nil, false
	}

	if b, ok := parseSpecialKey(rest, mods); ok {
		return b, true
	}
	if b, ok := parseRuneKey(rest, mods); ok {
		return b, true
	}
	return nil, false
}

func parseKeyModifiers(s string) (keyModifiers, string, bool) {
	var mods keyModifiers
	for len(s) >= 2 && s[1] == '-' {
		switch unicode.ToLower(rune(s[0])) {
		case 'c':
			mods.ctrl = true
		case 'm':
			mods.meta = true
		case 's':
			mods.shift = true
		default:
			return keyModifiers{}, "", false
		}
		s = s[2:]
	}
	return mods, s, true
}

type keyModifiers struct {
	ctrl  bool
	meta  bool
	shift bool
}

func parseSpecialKey(name string, mods keyModifiers) ([]byte, bool) {
	lower := strings.ToLower(name)
	if mods.shift && lower == "tab" {
		lower = "btab"
		mods.shift = false
	}

	special, ok := sendKeysNamedKeys[lower]
	if !ok {
		return nil, false
	}
	if (mods.ctrl || mods.shift || mods.meta) && !special.allowModifiers {
		return nil, false
	}
	seq := append([]byte(nil), special.seq...)
	if !mods.ctrl && !mods.shift && !mods.meta {
		return seq, true
	}
	if len(seq) == 1 && special.allowModifiers {
		return applySingleByteModifiers(seq[0], mods)
	}
	return applyCSIOrMetaModifiers(seq, mods)
}

func parseRuneKey(s string, mods keyModifiers) ([]byte, bool) {
	if utf8.RuneCountInString(s) != 1 {
		return nil, false
	}
	r, _ := utf8.DecodeRuneInString(s)
	if mods.shift && unicode.IsLower(r) {
		r = unicode.ToUpper(r)
		mods.shift = false
	}
	if mods.ctrl {
		if b, ok := ctrlASCIIByte(r); ok {
			if mods.meta {
				return []byte{0x1b, b}, true
			}
			return []byte{b}, true
		}
		return nil, false
	}

	buf := []byte(string(r))
	if mods.meta {
		return append([]byte{0x1b}, buf...), true
	}
	if mods.shift && len(buf) == 1 {
		if 'a' <= buf[0] && buf[0] <= 'z' {
			buf[0] = buf[0] - 'a' + 'A'
		}
	}
	return buf, true
}

func ctrlASCIIByte(r rune) (byte, bool) {
	switch {
	case r == '?':
		return 0x7f, true
	case 'a' <= r && r <= 'z':
		return byte(r-'a') + 1, true
	case 'A' <= r && r <= 'Z':
		return byte(r-'A') + 1, true
	case r == '@' || r == ' ':
		return 0x00, true
	case r == '[':
		return 0x1b, true
	case r == '\\':
		return 0x1c, true
	case r == ']':
		return 0x1d, true
	case r == '^':
		return 0x1e, true
	case r == '_':
		return 0x1f, true
	default:
		return 0, false
	}
}

func applySingleByteModifiers(b byte, mods keyModifiers) ([]byte, bool) {
	if mods.shift && 'a' <= b && b <= 'z' {
		b = b - 'a' + 'A'
		mods.shift = false
	}
	if mods.ctrl {
		ctrl, ok := ctrlASCIIByte(rune(b))
		if !ok {
			return nil, false
		}
		b = ctrl
	}
	if mods.meta {
		return []byte{0x1b, b}, true
	}
	if mods.shift {
		return nil, false
	}
	return []byte{b}, true
}

func applyCSIOrMetaModifiers(seq []byte, mods keyModifiers) ([]byte, bool) {
	if mods.meta && !mods.ctrl && !mods.shift {
		return append([]byte{0x1b}, seq...), true
	}
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return nil, false
	}

	modParam := 1
	if mods.shift {
		modParam += 1
	}
	if mods.meta {
		modParam += 2
	}
	if mods.ctrl {
		modParam += 4
	}

	suffix := seq[len(seq)-1]
	body := string(seq[2 : len(seq)-1])
	if body == "" {
		body = "1"
	}
	return fmt.Appendf(nil, "\x1b[%s;%d%c", body, modParam, suffix), true
}

// SendKeys writes encoded key input to a running command's PTY.
func (s *Service) SendKeys(ctx context.Context, idOrName string, req SendKeysRequest) error {
	data, err := EncodeSendKeys(req)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	endpoint, err := s.ResolveMonitor(ctx, idOrName)
	if err != nil {
		return err
	}

	conn, err := grpc.NewClient(
		"unix://"+endpoint.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("connect to monitor: %w", err)
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	if _, err := client.WriteStdin(ctx, &cmdmanv1pb.WriteStdinRequest{Stdin: data}); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}
