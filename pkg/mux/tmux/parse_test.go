package tmux

import (
	"testing"
)

func TestParsePaneInfo(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantID  string
		wantIdx int
		wantErr bool
	}{
		{
			name:    "valid line",
			line:    "%0\t2",
			wantID:  "%0",
			wantIdx: 2,
		},
		{
			name:    "zero index",
			line:    "%5\t0",
			wantID:  "%5",
			wantIdx: 0,
		},
		{
			name:    "missing tab",
			line:    "%0",
			wantErr: true,
		},
		{
			name:    "invalid index",
			line:    "%0\tabc",
			wantErr: true,
		},
		{
			name:    "empty string",
			line:    "",
			wantErr: true,
		},
		{
			name:    "tab only",
			line:    "\t",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, idx, err := parsePaneInfo(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got id=%q idx=%d", id, idx)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID {
				t.Errorf("id = %q, want %q", id, tt.wantID)
			}
			if idx != tt.wantIdx {
				t.Errorf("idx = %d, want %d", idx, tt.wantIdx)
			}
		})
	}
}

func TestParsePaneIDs(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{
			name: "normal",
			out:  "%0\n%1\n%2",
			want: []string{"%0", "%1", "%2"},
		},
		{
			name: "single",
			out:  "%0",
			want: []string{"%0"},
		},
		{
			name: "empty",
			out:  "",
			want: nil,
		},
		{
			name: "trailing newline includes empty element",
			out:  "%0\n%1\n",
			want: []string{"%0", "%1", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePaneIDs(tt.out)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d; got %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseWindows(t *testing.T) {
	exec := newExecutor("", "")

	tests := []struct {
		name    string
		out     string
		wantLen int
		wantIDs []string
	}{
		{
			name:    "three windows",
			out:     "@0\t0\twindow0\n@1\t1\twindow1\n@2\t2\twindow2",
			wantLen: 3,
			wantIDs: []string{"@0", "@1", "@2"},
		},
		{
			name:    "empty",
			out:     "",
			wantLen: 0,
		},
		{
			name:    "malformed line with only two fields is skipped",
			out:     "@0\t0\twindow0\n@1\t1\n@2\t2\twindow2",
			wantLen: 2,
			wantIDs: []string{"@0", "@2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWindows(tt.out, "test-session", exec, nil, nil, "$0")
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i, wantID := range tt.wantIDs {
				if got[i].Id() != wantID {
					t.Errorf("got[%d].Id() = %q, want %q", i, got[i].Id(), wantID)
				}
			}
		})
	}
}

func TestParsePanes(t *testing.T) {
	exec := newExecutor("", "")

	tests := []struct {
		name    string
		out     string
		wantLen int
		wantIDs []string
	}{
		{
			name:    "two panes",
			out:     "%0\t0\n%1\t1",
			wantLen: 2,
			wantIDs: []string{"%0", "%1"},
		},
		{
			name:    "empty",
			out:     "",
			wantLen: 0,
		},
		{
			name:    "malformed line skipped",
			out:     "%0\t0\nbadline\n%2\t2",
			wantLen: 2,
			wantIDs: []string{"%0", "%2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePanes(tt.out, "$0", "@0", exec)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i, wantID := range tt.wantIDs {
				if got[i].Id() != wantID {
					t.Errorf("got[%d].Id() = %q, want %q", i, got[i].Id(), wantID)
				}
			}
		})
	}
}
