package monitor

import "bytes"

// hookBlocks says which captured event kinds must never reach a viewer
// (model.HookActionBlock). It is derived from the resolved hook config.
type hookBlocks struct {
	Bell         bool
	Title        bool
	Notification bool
}

func (b hookBlocks) any() bool {
	return b.Bell || b.Title || b.Notification
}

// Parser states. Bytes are held in hookFilterOscID, where the OSC command
// number is still being read, and in hookFilterEsc, which holds the ESC itself
// plus however many earlier ones it superseded.
const (
	hookFilterGround = iota
	hookFilterEsc
	hookFilterUtf8
	hookFilterOscID
	hookFilterOscPass
	hookFilterOscDrop
	hookFilterOscDropEsc
)

// How a byte inside an OSC string ends it. These mirror the transitions out of
// the emulator's OSC state (charmbracelet/x/ansi, parser/transition_table.go):
// BEL and the C1 ST dispatch the sequence, ESC dispatches it and opens the next
// one, and CAN and SUB abort it. Recognizing fewer terminators than the
// emulator would leave a blocked sequence dropping the rest of the stream
// forever, since the filter would never see its string end.
const (
	oscByteData = iota
	oscByteEnd
	oscByteEsc
	oscByteAbort
)

// classifyOscByte reports how b ends - or does not end - an OSC string. The C1
// ST is matched on the raw byte because the emulator matches it there too: it
// decodes no UTF-8 inside an OSC string, so the 0x9C of a rune like U+041C ends
// the sequence for both, and the filter stays in step by ending it as well.
func classifyOscByte(b byte) int {
	switch b {
	case 0x07, 0x9c:
		return oscByteEnd
	case 0x1b:
		return oscByteEsc
	case 0x18, 0x1a:
		return oscByteAbort
	}
	return oscByteData
}

// oscDecideLimit caps how many bytes of an OSC sequence are held while its
// identity is undecided. Every hooked form declares itself well within it, so
// the limit only bounds the damage of a malformed sequence.
const oscDecideLimit = 32

// hookFilter removes blocked sequences from a viewer's byte stream. It runs on
// the way out to a viewer, not on the way in: the ring buffer, the log driver,
// and the monitor's own emulator keep receiving what the command actually
// wrote, so blocking a bell never costs the command's log record or the
// unconditional state capture behind it.
//
// One filter belongs to one stream. It carries parse state across calls
// because a sequence can straddle two chunks; a fresh stream starts at ground,
// which is correct for an attach that begins mid-output.
type hookFilter struct {
	blocks hookBlocks
	state  int
	// pending holds the bytes of an OSC sequence whose command number is not
	// yet known, so a blocked sequence can be dropped whole.
	pending []byte
	// prefixLen is how much of pending is the introducer - one byte for a bare
	// C1 OSC, two for `ESC ]`, more once superseded ESCs ride along - so the
	// body starts after it.
	prefixLen int
	// utf8Rest counts the continuation bytes still owed to the rune in flight.
	utf8Rest int
	// escCarry counts the ESCs the currently held one superseded, kept until
	// whatever that ESC opens is decided.
	escCarry int
}

// newHookFilter returns nil when nothing is blocked; the nil filter passes
// every byte through untouched and unallocated.
func newHookFilter(blocks hookBlocks) *hookFilter {
	if !blocks.any() {
		return nil
	}
	return &hookFilter{blocks: blocks}
}

// filter returns data with blocked sequences removed. The returned slice
// aliases nothing in data.
func (f *hookFilter) filter(data []byte) []byte {
	if f == nil || len(data) == 0 {
		return data
	}
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch f.state {
		case hookFilterGround:
			out = f.ground(out, b)
		case hookFilterEsc:
			out = f.esc(out, b)
		case hookFilterUtf8:
			out = f.utf8(out, b)
		case hookFilterOscID:
			out = f.oscID(out, b)
		case hookFilterOscPass:
			out = f.oscPass(out, b)
		case hookFilterOscDrop:
			out = f.oscDrop(out, b)
		case hookFilterOscDropEsc:
			out = f.oscDropEsc(out, b)
		}
	}
	return out
}

// ground handles a byte outside any sequence. ESC is held back until the next
// byte says whether it opens an OSC, while the C1 OSC introducer 0x9D opens one
// by itself - the emulator accepts both, and a filter that let 0x9D through
// would then eat the sequence's own BEL as a bell, leaving the viewer's parser
// inside an OSC string that swallows the rest of the stream.
func (f *hookFilter) ground(out []byte, b byte) []byte {
	switch {
	case b == 0x1b:
		f.state = hookFilterEsc
		return out
	case b == 0x9d:
		f.beginOsc(0x9d)
		return out
	case b == 0x07 && f.blocks.Bell:
		return out
	}
	if n := utf8RuneLen(b); n > 1 {
		f.state = hookFilterUtf8
		f.utf8Rest = n - 1
	}
	return append(out, b)
}

// utf8 passes the continuation bytes of a multi-byte rune through as data. The
// emulator decodes a rune without consulting its transition table at all
// (ansi.Parser.Advance hands Utf8State to advanceUtf8), so the 0x9D inside a
// rune like そ (U+305D, E3 81 9D) is not an introducer there and must not be one
// here. The bytes are taken on the lead byte's word rather than validated as
// continuations, because that is what advanceUtf8 does: staying in step with the
// emulator matters more than being right about the encoding.
func (f *hookFilter) utf8(out []byte, b byte) []byte {
	f.utf8Rest--
	if f.utf8Rest <= 0 {
		f.state = hookFilterGround
	}
	return append(out, b)
}

// utf8RuneLen reports the length of the rune a byte introduces, covering exactly
// the lead-byte ranges the emulator's table routes to its UTF-8 state.
func utf8RuneLen(b byte) int {
	switch {
	case b >= 0xc2 && b <= 0xdf:
		return 2
	case b >= 0xe0 && b <= 0xef:
		return 3
	case b >= 0xf0 && b <= 0xf4:
		return 4
	}
	return 1
}

// beginOsc starts holding an OSC sequence, whose introducer is the bytes given.
// Superseded ESCs join the introducer so that they are released or dropped with
// the sequence they turned out to precede.
func (f *hookFilter) beginOsc(introducer ...byte) {
	f.state = hookFilterOscID
	f.pending = f.pending[:0]
	for range f.escCarry {
		f.pending = append(f.pending, 0x1b)
	}
	f.escCarry = 0
	f.pending = append(f.pending, introducer...)
	f.prefixLen = len(f.pending)
}

func (f *hookFilter) esc(out []byte, b byte) []byte {
	switch b {
	case ']':
		f.beginOsc(0x1b, ']')
		return out
	case 0x9d:
		// The emulator discards the held ESC and starts an OSC on the 0x9D. The
		// held ESC still travels with the sequence: a viewer parses these two
		// bytes the same way, so passing them on keeps it in step, and dropping
		// them together keeps a blocked sequence whole.
		f.beginOsc(0x1b, 0x9d)
		return out
	case 0x1b:
		// The held ESC was not a sequence start we track; this one takes over.
		// It cannot be released yet: the emulator discards a superseded ESC
		// (an ESC in its escape state executes as a control code no handler
		// claims), so releasing it before a blocked sequence would leave the
		// viewer's parser mid-escape and eat the byte behind the sequence.
		f.escCarry++
		return out
	}
	f.state = hookFilterGround
	out = f.flushEscCarry(out)
	return append(out, 0x1b, b)
}

// flushEscCarry releases the superseded ESCs ahead of the held one. Passing
// them on verbatim keeps the stream byte-exact, and a viewer reads a run of
// ESCs the way the emulator did: only the last one opens anything.
func (f *hookFilter) flushEscCarry(out []byte) []byte {
	for range f.escCarry {
		out = append(out, 0x1b)
	}
	f.escCarry = 0
	return out
}

// oscID accumulates an OSC sequence until it can be identified, then either
// drops it or releases the held bytes and stops holding the rest.
func (f *hookFilter) oscID(out []byte, b byte) []byte {
	class := classifyOscByte(b)
	if class != oscByteEsc {
		// An ESC ends this sequence and opens the next one, so it belongs to
		// the next one; hookFilterEsc is where it is held.
		f.pending = append(f.pending, b)
	}
	body := f.pending[f.prefixLen:] // after the introducer
	if class == oscByteEnd || class == oscByteAbort {
		body = body[:len(body)-1] // the terminator is not part of the payload
	}

	block, decided := f.decideOsc(body, class != oscByteData)
	if !decided && class == oscByteData && len(body) < oscDecideLimit {
		return out
	}

	held := f.pending
	f.pending = f.pending[:0]
	if !block {
		out = append(out, held...)
	}
	f.state = oscIDNext(class, block)
	return out
}

// oscIDNext is the state an identified OSC sequence continues in: the rest of
// its string, or - once class ended the string - what follows it.
func oscIDNext(class int, block bool) int {
	switch class {
	case oscByteEnd, oscByteAbort:
		return hookFilterGround
	case oscByteEsc:
		if block {
			return hookFilterOscDropEsc
		}
		return hookFilterEsc
	}
	if block {
		return hookFilterOscDrop
	}
	return hookFilterOscPass
}

// oscPass relays the rest of an OSC string that is not blocked.
func (f *hookFilter) oscPass(out []byte, b byte) []byte {
	switch classifyOscByte(b) {
	case oscByteEsc:
		// hookFilterEsc holds the ESC and emits it with whatever it opens.
		f.state = hookFilterEsc
		return out
	case oscByteEnd, oscByteAbort:
		f.state = hookFilterGround
	}
	return append(out, b)
}

// oscDrop swallows the rest of a blocked OSC string. Bytes inside an OSC string
// are consumed by a terminal rather than rendered, so dropping them costs a
// viewer nothing beyond the sequence itself.
func (f *hookFilter) oscDrop(out []byte, b byte) []byte {
	switch classifyOscByte(b) {
	case oscByteEsc:
		f.state = hookFilterOscDropEsc
	case oscByteEnd, oscByteAbort:
		f.state = hookFilterGround
	}
	return out
}

// oscDropEsc decides the ESC that ended a blocked OSC string: with a '\' it
// completes that string's own ST and goes with it, anything else hands it to
// esc as the start of a sequence of its own.
func (f *hookFilter) oscDropEsc(out []byte, b byte) []byte {
	if b == '\\' {
		f.state = hookFilterGround
		return out
	}
	f.state = hookFilterEsc
	return f.esc(out, b)
}

// decideOsc reports whether the OSC identified by body is blocked. decided is
// false while the body is too short to tell. The forms recognized here mirror
// what the monitor captures: OSC 0/2 titles (OSC 1 sets only the icon name,
// which has no callback registered), and the OSC 9 / OSC 777 notification
// forms accepted by parseOsc9Notification / parseOsc777Notification.
func (f *hookFilter) decideOsc(body []byte, terminated bool) (block, decided bool) {
	num, rest, hasSep := bytes.Cut(body, []byte{';'})
	if len(num) == 0 || !isDigits(num) {
		return false, true
	}
	if !hasSep {
		// A parameterless OSC matches none of the hooked forms.
		if terminated || len(num) > 3 {
			return false, true
		}
		return false, false
	}
	switch string(num) {
	case "0", "2":
		return f.blocks.Title, true
	case "9":
		return f.decideOsc9(rest, terminated)
	case "777":
		return f.decideOsc777(rest, terminated)
	}
	return false, true
}

// decideOsc9 separates iTerm2 notifications from ConEmu's progress form
// (9;4[;…]), which carries no message.
func (f *hookFilter) decideOsc9(rest []byte, terminated bool) (block, decided bool) {
	switch {
	case len(rest) == 0:
		if terminated {
			return false, true
		}
		return false, false
	case rest[0] != '4':
		return f.blocks.Notification, true
	case len(rest) == 1:
		if terminated {
			return false, true
		}
		return false, false
	case rest[1] == ';':
		return false, true
	}
	return f.blocks.Notification, true
}

// decideOsc777 accepts only `777;notify;<title>[;<body>]`; other subtypes and a
// notify without a title are not captured, so they are not blocked either.
func (f *hookFilter) decideOsc777(rest []byte, terminated bool) (block, decided bool) {
	notify := []byte("notify")
	sub, _, hasSep := bytes.Cut(rest, []byte{';'})
	if !hasSep {
		if terminated || len(sub) > len(notify) || !bytes.HasPrefix(notify, sub) {
			return false, true
		}
		return false, false
	}
	if !bytes.Equal(sub, notify) {
		return false, true
	}
	return f.blocks.Notification, true
}

func isDigits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
