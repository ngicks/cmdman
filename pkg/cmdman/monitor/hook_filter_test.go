package monitor

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// filterChunks feeds every chunk through one filter and returns the
// concatenated result, the way an Attach stream sees it.
func filterChunks(f *hookFilter, chunks ...string) string {
	var out []byte
	for _, chunk := range chunks {
		out = append(out, f.filter([]byte(chunk))...)
	}
	return string(out)
}

func TestHookFilter_PassesEverythingWhenNothingIsBlocked(t *testing.T) {
	f := newHookFilter(hookBlocks{})
	assert.Assert(t, f == nil, "a filter with nothing to block should not exist")

	raw := "plain\a\x1b]2;title\x07\x9d9;note\x07\x1b[31mred\x1b[0m"
	assert.Equal(t, filterChunks(f, raw), raw)
}

func TestHookFilter_Blocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		blocks hookBlocks
		chunks []string
		want   string
	}{
		{
			name:   "bell is stripped",
			blocks: hookBlocks{Bell: true},
			chunks: []string{"ding\adong\a"},
			want:   "dingdong",
		},
		{
			// The BEL that ends an OSC is a string terminator, not a bell;
			// stripping it would leave the viewer inside an open sequence.
			name:   "osc terminating bel survives a bell block",
			blocks: hookBlocks{Bell: true},
			chunks: []string{"\x1b]2;title\x07after"},
			want:   "\x1b]2;title\x07after",
		},
		{
			name:   "title is stripped, bel terminated",
			blocks: hookBlocks{Title: true},
			chunks: []string{"before\x1b]2;title\x07after"},
			want:   "beforeafter",
		},
		{
			name:   "title is stripped, st terminated",
			blocks: hookBlocks{Title: true},
			chunks: []string{"before\x1b]0;title\x1b\\after"},
			want:   "beforeafter",
		},
		{
			// The emulator ends an OSC string on a raw 0x9C, so a filter that
			// did not would keep dropping the rest of the stream.
			name:   "title is stripped, c1 st terminated",
			blocks: hookBlocks{Title: true},
			chunks: []string{"before\x1b]2;title\x9cafter"},
			want:   "beforeafter",
		},
		{
			// An ESC ends the string whatever follows it; the sequence it
			// opens is the next one's and must survive.
			name:   "an esc that is not an st ends a blocked sequence",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b]2;t\x1b[0mkeep"},
			want:   "\x1b[0mkeep",
		},
		{
			name:   "the st of a blocked sequence split across chunks",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b]2;t\x1b", "\\rest"},
			want:   "rest",
		},
		{
			name:   "can aborts a blocked sequence",
			blocks: hookBlocks{Title: true},
			chunks: []string{"a\x1b]2;t\x18b"},
			want:   "ab",
		},
		{
			name:   "sub aborts a blocked sequence",
			blocks: hookBlocks{Title: true},
			chunks: []string{"a\x1b]2;t\x1ab"},
			want:   "ab",
		},
		{
			// The emulator decodes no UTF-8 inside an OSC string, so the 0x9C
			// of "М" (U+041C) ends the title there too: the filter drops
			// exactly what the emulator consumed and passes what it rendered.
			name:   "a c1 st inside a utf-8 payload ends the sequence early",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b]2;\xd0\x9cx\x07after"},
			want:   "x\x07after",
		},
		{
			name:   "an unhooked sequence keeps its c1 st",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b]8;;https://example.com\x9clink"},
			want:   "\x1b]8;;https://example.com\x9clink",
		},
		{
			name:   "an unhooked sequence keeps its st",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b]8;;https://example.com\x1b\\link"},
			want:   "\x1b]8;;https://example.com\x1b\\link",
		},
		{
			// OSC 1 sets the icon name, which the monitor does not capture,
			// so the hook does not speak for it either.
			name:   "icon name passes a title block",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b]1;icon\x07"},
			want:   "\x1b]1;icon\x07",
		},
		{
			name:   "osc 9 notification is stripped",
			blocks: hookBlocks{Notification: true},
			chunks: []string{"a\x1b]9;build done; deploy?\x07b"},
			want:   "ab",
		},
		{
			name:   "osc 777 notify is stripped",
			blocks: hookBlocks{Notification: true},
			chunks: []string{"a\x1b]777;notify;Agent;needs input\x1b\\b"},
			want:   "ab",
		},
		{
			// Not a notification (parseOsc9Notification skips it), so not a
			// hooked sequence.
			name:   "conemu progress passes a notification block",
			blocks: hookBlocks{Notification: true},
			chunks: []string{"\x1b]9;4;1;50\x07", "\x1b]9;4\x07"},
			want:   "\x1b]9;4;1;50\x07\x1b]9;4\x07",
		},
		{
			name:   "osc 9 message starting with 4 is still a notification",
			blocks: hookBlocks{Notification: true},
			chunks: []string{"\x1b]9;42 done\x07"},
			want:   "",
		},
		{
			name:   "other 777 subtypes pass",
			blocks: hookBlocks{Notification: true},
			chunks: []string{"\x1b]777;precmd\x07"},
			want:   "\x1b]777;precmd\x07",
		},
		{
			name:   "777 notify without a title is not captured, so not blocked",
			blocks: hookBlocks{Notification: true},
			chunks: []string{"\x1b]777;notify\x07"},
			want:   "\x1b]777;notify\x07",
		},
		{
			name:   "unhooked osc sequences pass while everything is blocked",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b]8;;https://example.com\x07link\x1b]8;;\x07"},
			want:   "\x1b]8;;https://example.com\x07link\x1b]8;;\x07",
		},
		{
			// The emulator opens an OSC on a bare 0x9D, so letting it through
			// would leave the viewer inside a string this filter thinks it is
			// outside of - and its BEL stripped as a bell, so the string never
			// ends for the viewer either.
			name:   "c1 osc introducer, title stripped, bel terminated",
			blocks: hookBlocks{Bell: true, Title: true},
			chunks: []string{"before\x9d2;title\x07after"},
			want:   "beforeafter",
		},
		{
			name:   "c1 osc introducer, st terminated",
			blocks: hookBlocks{Title: true},
			chunks: []string{"before\x9d0;title\x1b\\after"},
			want:   "beforeafter",
		},
		{
			name:   "c1 osc introducer, c1 st terminated",
			blocks: hookBlocks{Title: true},
			chunks: []string{"before\x9d2;title\x9cafter"},
			want:   "beforeafter",
		},
		{
			name:   "c1 osc introducer, aborted",
			blocks: hookBlocks{Title: true},
			chunks: []string{"a\x9d2;t\x18b"},
			want:   "ab",
		},
		{
			name:   "c1 osc introducer, notification stripped",
			blocks: hookBlocks{Notification: true},
			chunks: []string{"a\x9d777;notify;Agent;needs input\x07b"},
			want:   "ab",
		},
		{
			// Nothing hooked speaks for OSC 8, so the whole sequence travels on
			// with its introducer intact.
			name:   "an unhooked c1 osc sequence passes whole",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x9d8;;https://example.com\x07link"},
			want:   "\x9d8;;https://example.com\x07link",
		},
		{
			name:   "a blocked c1 osc sequence split across chunks",
			blocks: hookBlocks{Title: true},
			chunks: []string{"out\x9d2;ti", "tle\x07rest"},
			want:   "outrest",
		},
		{
			// The emulator drops the held ESC and starts the OSC on the 0x9D;
			// both bytes belong to the sequence, so both go or neither does.
			name:   "an esc before a c1 osc introducer goes with the sequence",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b\x9d2;t\x07keep"},
			want:   "keep",
		},
		{
			name:   "an esc before an unhooked c1 osc introducer survives",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b\x9d8;;u\x07keep"},
			want:   "\x1b\x9d8;;u\x07keep",
		},
		{
			// A second ESC restarts the escape and the emulator discards the
			// first, so releasing it early would leave the viewer's parser
			// inside an escape that nothing on the emulator's side opened -
			// eating the byte behind the sequence this one drops.
			name:   "a superseded esc goes with a blocked sequence",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b\x1b]2;t\x07keep"},
			want:   "keep",
		},
		{
			name:   "a superseded esc survives an unhooked sequence",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b\x1b]8;;u\x07keep"},
			want:   "\x1b\x1b]8;;u\x07keep",
		},
		{
			name:   "a superseded esc goes with a blocked c1 osc sequence",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b\x1b\x9d2;t\x07keep"},
			want:   "keep",
		},
		{
			name:   "a superseded esc before a byte that starts no sequence",
			blocks: hookBlocks{Bell: true},
			chunks: []string{"a\x1b\x1b=b"},
			want:   "a\x1b\x1b=b",
		},
		{
			// Only the last ESC of a chain opens the sequence; every one
			// before it is discarded by the emulator, so all of them share
			// the sequence's fate.
			name:   "a chain of escs goes with a blocked sequence",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b\x1b\x1b]2;t\x07keep"},
			want:   "keep",
		},
		{
			name:   "a chain of escs survives an unhooked sequence",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b\x1b\x1b]8;;u\x07keep"},
			want:   "\x1b\x1b\x1b]8;;u\x07keep",
		},
		{
			// The ESC that ended the first blocked sequence is superseded in
			// turn by the one that opens the second.
			name:   "the esc ending a blocked sequence is superseded in turn",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\x1b]2;t\x1b\x1b]2;u\x07keep"},
			want:   "keep",
		},
		{
			name:   "superseded escs split across chunks",
			blocks: hookBlocks{Title: true},
			chunks: []string{"out\x1b", "\x1b]2;ti", "tle\x07rest"},
			want:   "outrest",
		},
		{
			// そ is E3 81 9D: the emulator decodes the rune without consulting
			// its table, so that 0x9D is text, not an introducer. A filter that
			// read it as one would swallow the run after it as a title.
			name:   "a 0x9d inside a utf-8 rune is not an introducer",
			blocks: hookBlocks{Title: true},
			chunks: []string{"そ2;title\x07after"},
			want:   "そ2;title\x07after",
		},
		{
			// Н is D0 9D - the two byte form of the same hazard.
			name:   "a two byte rune carrying 0x9d is not an introducer",
			blocks: hookBlocks{Title: true, Notification: true},
			chunks: []string{"Н9;note\x07tail"},
			want:   "Н9;note\x07tail",
		},
		{
			name:   "a rune carrying 0x9d split across chunks",
			blocks: hookBlocks{Title: true},
			chunks: []string{"\xe3", "\x81\x9d2;title\x07after"},
			want:   "そ2;title\x07after",
		},
		{
			// 😝 is F0 9F 98 9D; misreading its last byte would hand the bell
			// behind it to the viewer as an OSC payload instead of blocking it.
			name:   "a four byte rune carrying 0x9d keeps the bell blocked",
			blocks: hookBlocks{Bell: true},
			chunks: []string{"😝\a!"},
			want:   "😝!",
		},
		{
			// A lead byte whose rune never arrives: advanceUtf8 takes the next
			// two bytes as continuations whatever they are and prints the
			// garbage rune, so those two bells are not bells for the emulator
			// either - and the one after it, back in ground, is.
			name:   "a truncated rune eats exactly its own continuation bytes",
			blocks: hookBlocks{Bell: true},
			chunks: []string{"\xe3", "\a\a\a"},
			want:   "\xe3\a\a",
		},
		{
			name:   "csi sequences pass untouched",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b[31mred\x1b[0m\x1b[?1049h"},
			want:   "\x1b[31mred\x1b[0m\x1b[?1049h",
		},
		{
			name:   "a blocked sequence split across chunks",
			blocks: hookBlocks{Title: true},
			chunks: []string{"out\x1b", "]2;ti", "tle", "\x07rest"},
			want:   "outrest",
		},
		{
			name:   "a passed sequence split across chunks",
			blocks: hookBlocks{Bell: true},
			chunks: []string{"out\x1b", "]2;ti", "tle\x07rest"},
			want:   "out\x1b]2;title\x07rest",
		},
		{
			name:   "an esc that starts no sequence is not swallowed",
			blocks: hookBlocks{Bell: true},
			chunks: []string{"a\x1b", "=b"},
			want:   "a\x1b=b",
		},
		{
			name:   "every hooked kind at once",
			blocks: hookBlocks{Bell: true, Title: true, Notification: true},
			chunks: []string{"\x1b]2;t\x07work\a\x1b]9;done\x07 end"},
			want:   "work end",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHookFilter(tc.blocks)
			assert.Assert(t, f != nil)
			assert.Equal(t, filterChunks(f, tc.chunks...), tc.want)
		})
	}
}

// A sequence that never terminates must not hold output back forever.
func TestHookFilter_UndecidableSequenceIsReleased(t *testing.T) {
	f := newHookFilter(hookBlocks{Title: true})
	got := filterChunks(f, "\x1b]"+strings.Repeat("9", oscDecideLimit))
	assert.Assert(t, len(got) > 0, "held bytes were never released")
}
