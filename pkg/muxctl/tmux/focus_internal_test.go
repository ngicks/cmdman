package tmux

import "testing"

// TestNewestClient pins which client a landing moves when several are attached
// to the caller's session: the one tmux saw activity on last, which is the
// terminal the summon came from. Lines whose activity stamp does not parse keep
// their list position rather than being dropped, so an unexpected wording costs
// the preference, not the landing.
func TestNewestClient(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "one client is the pick",
			lines: []string{"1786383198\t/dev/pts/8"},
			want:  "/dev/pts/8",
		},
		{
			name: "the most recently active of several",
			lines: []string{
				"1786383198\t/dev/pts/8",
				"1786383777\t/dev/pts/10",
				"1786383400\t/dev/pts/12",
			},
			want: "/dev/pts/10",
		},
		{
			name:  "equal stamps keep list order",
			lines: []string{"1786383198\t/dev/pts/8", "1786383198\t/dev/pts/10"},
			want:  "/dev/pts/8",
		},
		{
			name:  "no stamp at all falls back to the first line",
			lines: []string{"/dev/pts/8", "/dev/pts/10"},
			want:  "/dev/pts/8",
		},
		{
			name:  "a stamp that will not parse loses only its preference",
			lines: []string{"n/a\t/dev/pts/8", "1786383198\t/dev/pts/10"},
			want:  "/dev/pts/10",
		},
		{
			name:  "nothing attached",
			lines: []string{""},
			want:  "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newestClient(tc.lines); got != tc.want {
				t.Errorf("newestClient(%q) = %q, want %q", tc.lines, got, tc.want)
			}
		})
	}
}
