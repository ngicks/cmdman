package compose_test

import (
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

// TestGenerateProjectIdentity verifies that GenerateProjectIdentity returns the
// expected <wdhash>-<escaped-project> format and that its output is always a
// strict prefix of GenerateName for the same wdHash and project.
func TestGenerateProjectIdentity(t *testing.T) {
	tests := []struct {
		name    string
		wdHash  string
		project string
		want    string
	}{
		{
			name:    "no dashes in project",
			wdHash:  "abcdef012345",
			project: "myproject",
			want:    "abcdef012345-myproject",
		},
		{
			name:    "single dash in project → doubled",
			wdHash:  "abcdef012345",
			project: "my-project",
			want:    "abcdef012345-my--project",
		},
		{
			name:    "multiple dashes → each doubled",
			wdHash:  "000000000000",
			project: "a-b-c",
			want:    "000000000000-a--b--c",
		},
		{
			name:    "no dashes, numeric chars",
			wdHash:  "123456789abc",
			project: "proj2",
			want:    "123456789abc-proj2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compose.GenerateProjectIdentity(tt.wdHash, tt.project)
			if got != tt.want {
				t.Fatalf("GenerateProjectIdentity(%q, %q) = %q, want %q",
					tt.wdHash, tt.project, got, tt.want)
			}
		})
	}
}

// TestGenerateProjectIdentity_PrefixOfGenerateName ensures that the project
// identity is always a strict prefix of GenerateName (with the command segment
// appended after a separator). This is the structural invariant the plan relies
// on: the identity uniquely identifies the project across all its commands.
func TestGenerateProjectIdentity_PrefixOfGenerateName(t *testing.T) {
	cases := []struct {
		wdHash  string
		project string
		command string
	}{
		{"abcdef012345", "myproject", "web"},
		{"abcdef012345", "my-project", "api-server"},
		{"000000000000", "a-b-c", "x-y"},
	}

	for _, c := range cases {
		identity := compose.GenerateProjectIdentity(c.wdHash, c.project)
		full := compose.GenerateName(c.wdHash, c.project, c.command)

		if !strings.HasPrefix(full, identity+"-") {
			t.Errorf(
				"GenerateName(%q,%q,%q)=%q is not prefixed by identity %q+'-'",
				c.wdHash, c.project, c.command, full, identity,
			)
		}
	}
}

// TestProjectSelection_ProjectIdentity verifies the ProjectSelection method
// that derives the identity from the selection's WorkDir and Project.
func TestProjectSelection_ProjectIdentity(t *testing.T) {
	t.Run("non-empty project returns non-empty identity", func(t *testing.T) {
		sel := compose.ProjectSelection{
			WorkDir: "/home/user/myproject",
			Project: "demo",
		}
		id := sel.ProjectIdentity()
		if id == "" {
			t.Fatal("ProjectIdentity() returned empty for non-empty project")
		}
		// The identity must contain the escaped project name after the first '-'.
		if !strings.Contains(id, "-demo") {
			t.Fatalf("identity %q does not contain escaped project name", id)
		}
	})

	t.Run("empty project returns empty identity", func(t *testing.T) {
		sel := compose.ProjectSelection{
			WorkDir: "/home/user/myproject",
			Project: "",
		}
		if id := sel.ProjectIdentity(); id != "" {
			t.Fatalf("ProjectIdentity() = %q for empty project, want \"\"", id)
		}
	})

	t.Run("same workdir+project → same identity each call", func(t *testing.T) {
		sel := compose.ProjectSelection{
			WorkDir: "/stable/workdir",
			Project: "stable-project",
		}
		id1 := sel.ProjectIdentity()
		id2 := sel.ProjectIdentity()
		if id1 != id2 {
			t.Fatalf("identity is not deterministic: %q vs %q", id1, id2)
		}
	})

	t.Run("dash in project name is escaped", func(t *testing.T) {
		sel := compose.ProjectSelection{
			WorkDir: "/home/user/work",
			Project: "my-service",
		}
		id := sel.ProjectIdentity()
		// The escaped project segment must contain '--' (doubled dash).
		if !strings.Contains(id, "--") {
			t.Fatalf("identity %q: dash in project name not escaped", id)
		}
	})

	t.Run("consistent with GenerateProjectIdentity for same wdHash", func(t *testing.T) {
		// Two selections with different WorkDirs but same canonical hash path
		// would differ — verify the hash is embedded. We can't easily compute
		// the expected hash here without reimplementing it, but we can verify
		// the identity starts with a 12-char hex prefix followed by '-'.
		sel := compose.ProjectSelection{
			WorkDir: "/some/path",
			Project: "proj",
		}
		id := sel.ProjectIdentity()
		parts := strings.SplitN(id, "-", 2)
		if len(parts) != 2 {
			t.Fatalf("identity %q has unexpected format (want <hash>-<escaped>)", id)
		}
		hash := parts[0]
		if len(hash) != 12 {
			t.Fatalf("identity hash segment %q: want 12 hex chars, got %d", hash, len(hash))
		}
		for _, c := range hash {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("identity hash segment %q contains non-hex char %q", hash, c)
			}
		}
	})
}
