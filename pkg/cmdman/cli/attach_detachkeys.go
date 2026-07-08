package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// detachKeyReader wraps stdin and scans for the detach-key sequence, returning
// [errDetach] once the full sequence is seen. Bytes that partially matched
// the sequence but then diverged are forwarded verbatim, in order. Literal
// (non-matching) bytes are copied straight into the caller's buffer; pending is
// only ever the bounded overflow from flushing a matched prefix that did not
// fit, so it never grows past len(detachKey).
type detachKeyReader struct {
	r         *bufio.Reader
	detachKey []byte
	match     int
	pending   []byte
	detached  bool
}

func newDetachKeyReader(r io.Reader, detachKeys []byte) io.Reader {
	if len(detachKeys) == 0 {
		return r
	}
	return &detachKeyReader{
		r:         bufio.NewReaderSize(r, 32*1024),
		detachKey: append([]byte(nil), detachKeys...),
	}
}

func (r *detachKeyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	n := 0
	if len(r.pending) > 0 {
		copied := copy(p, r.pending)
		n += copied
		if copied < len(r.pending) {
			r.pending = r.pending[copied:]
			return n, nil
		}
		r.pending = r.pending[:0]
	}
	if r.detached {
		return n, errDetach
	}

	for n < len(p) {
		// Respect io.Reader semantics: once we have bytes for the caller and
		// nothing more is immediately buffered, return instead of blocking on
		// another ReadByte to fill p. Any partial match is carried in r.match
		// across calls, so this never drops or reorders input. Without this a
		// single keystroke would stall until the buffer filled or detach hit.
		if n > 0 && r.r.Buffered() == 0 {
			return n, nil
		}

		// Fast path: with no partial match in progress, bulk-copy the run of
		// already-buffered bytes up to the next possible detach-key start.
		if r.match == 0 && r.r.Buffered() > 0 {
			chunk, _ := r.r.Peek(r.r.Buffered())
			run := chunk
			if i := bytes.IndexByte(chunk, r.detachKey[0]); i >= 0 {
				run = chunk[:i]
			}
			if len(run) > 0 {
				copied := copy(p[n:], run)
				_, _ = r.r.Discard(copied)
				n += copied
				continue
			}
		}

		b, err := r.r.ReadByte()
		if err != nil {
			if r.match > 0 {
				n = r.emit(p, n, r.detachKey[:r.match])
				r.match = 0
			}
			if n > 0 {
				return n, nil
			}
			return 0, err
		}

		if b == r.detachKey[r.match] {
			r.match++
			if r.match == len(r.detachKey) {
				r.match = 0
				r.detached = true
				return n, errDetach
			}
			continue
		}

		// b diverges from detachKey[r.match]: flush the matched prefix, then
		// reconsider b as either the start of a fresh match or a literal byte.
		if r.match > 0 {
			matched := r.match
			r.match = 0
			n = r.emit(p, n, r.detachKey[:matched])
			if b == r.detachKey[0] {
				r.match = 1
				if len(r.pending) > 0 || n == len(p) {
					return n, nil
				}
				continue
			}
		}
		if len(r.pending) > 0 || n == len(p) {
			r.pending = append(r.pending, b)
			return n, nil
		}
		p[n] = b
		n++
	}
	return n, nil
}

// emit copies src into p starting at offset n, spilling any remainder that does
// not fit into r.pending, and returns the new offset.
func (r *detachKeyReader) emit(p []byte, n int, src []byte) int {
	copied := copy(p[n:], src)
	if copied < len(src) {
		r.pending = append(r.pending, src[copied:]...)
	}
	return n + copied
}

// ctrlKeyBytes maps the key part of a control-key token (the character after a
// "ctrl-"/"c-" prefix) to the ASCII control byte it produces: the 0x00..0x1f
// block, i.e. @ a-z [ \ ] ^ _. Keys are lower-cased because parseDetachKeys
// lower-cases input before lookup. Edit this table to add or change a mapping.
var ctrlKeyBytes = map[byte]byte{
	'@': 0x00,
	'a': 0x01, 'b': 0x02, 'c': 0x03, 'd': 0x04, 'e': 0x05, 'f': 0x06, 'g': 0x07,
	'h': 0x08, 'i': 0x09, 'j': 0x0a, 'k': 0x0b, 'l': 0x0c, 'm': 0x0d, 'n': 0x0e, 'o': 0x0f,
	'p': 0x10, 'q': 0x11, 'r': 0x12, 's': 0x13, 't': 0x14, 'u': 0x15, 'v': 0x16, 'w': 0x17,
	'x': 0x18, 'y': 0x19, 'z': 0x1a,
	'[': 0x1b, '\\': 0x1c, ']': 0x1d, '^': 0x1e, '_': 0x1f,
}

// detachKeyPrefixes is the nested lookup table for "<prefix><key>" detach
// tokens: the outer key is the spelled prefix, the inner table maps the single
// key character to its byte. ctrl- and the tmux-style C- share one inner table,
// so teaching the parser a new spelling is a single extra row. (Lower-cased;
// see parseDetachKeys.)
var detachKeyPrefixes = map[string]map[byte]byte{
	"ctrl-": ctrlKeyBytes,
	"c-":    ctrlKeyBytes,
}

// parseDetachKeys parses a detach-key sequence string into the raw byte
// sequence that signals detach. Tokens are comma-separated; each is either a
// single literal character or a control key spelled "ctrl-<c>" or the tmux-
// style "C-<c>" (case-insensitive), where <c> is one of @ A-Z [ \ ] ^ _ (the
// 0x00..0x1f control range). An empty string disables detach.
//
// e.g. "ctrl-p,ctrl-q" and "C-p,C-q" both parse to {0x10, 0x11}.
func parseDetachKeys(detachKeys string) ([]byte, error) {
	if detachKeys == "" {
		return nil, nil
	}
	var codes []byte
	for token := range strings.SplitSeq(strings.ToLower(detachKeys), ",") {
		code, err := parseDetachKeyToken(token)
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// parseDetachKeyToken resolves one token through the nested prefix/key table. A
// single character is a literal byte; otherwise the token splits into its last
// character (the key) and everything before it (the prefix), and both must hit
// the table. token is assumed already lower-cased.
func parseDetachKeyToken(token string) (byte, error) {
	if len(token) == 1 {
		return token[0], nil
	}
	prefix, key := token[:len(token)-1], token[len(token)-1]
	keys, ok := detachKeyPrefixes[prefix]
	if !ok {
		return 0, fmt.Errorf("invalid detach key %q", token)
	}
	code, ok := keys[key]
	if !ok {
		return 0, fmt.Errorf("detach key %q is not a control character", token)
	}
	return code, nil
}
