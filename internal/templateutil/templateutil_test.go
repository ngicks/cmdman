package templateutil_test

import (
	"bytes"
	"maps"
	"slices"
	"strings"
	"testing"
	"text/template"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/internal/templateutil"
)

// TestFuncDocs_MatchesFuncMap is the lockstep guard: FuncHelp is generated from
// FuncDocs and embedded in `cmdman config --help`, so a helper added to FuncMap
// without a doc — or a doc left behind by a removed helper — would silently make
// that help wrong.
func TestFuncDocs_MatchesFuncMap(t *testing.T) {
	registered := slices.Sorted(maps.Keys(templateutil.FuncMap()))

	docs := templateutil.FuncDocs()
	documented := make([]string, len(docs))
	for i, d := range docs {
		documented[i] = d.Name
	}
	slices.Sort(documented)

	assert.DeepEqual(t, documented, registered)

	for _, d := range docs {
		assert.Assert(t, d.Desc != "", "%s: empty desc", d.Name)
		assert.Assert(
			t,
			strings.HasPrefix(d.Usage, d.Name),
			"%s: usage %q does not start with the name", d.Name, d.Usage,
		)
	}
}

func TestFuncHelp(t *testing.T) {
	docs := templateutil.FuncDocs()
	help := templateutil.FuncHelp()
	assert.Assert(t, strings.HasSuffix(help, "\n"))

	lines := strings.Split(strings.TrimRight(help, "\n"), "\n")
	assert.Equal(t, len(lines), len(docs))

	// The usage column is padded to a common width, so every description starts
	// at the same offset.
	want := strings.Index(lines[0], docs[0].Desc)
	assert.Assert(t, want > 0)
	for i, l := range lines {
		assert.Assert(t, strings.HasPrefix(l, "  "), "line %d = %q", i, l)
		assert.Equal(t, strings.Index(l, docs[i].Desc), want, "line %d = %q", i, l)
	}
}

// TestFuncMapRenders exercises each helper through an actual template, which is
// the only way the func map is ever used.
func TestFuncMapRenders(t *testing.T) {
	type payload struct {
		Name  string
		Count *int
		Empty *int
		Argv  []string
	}
	n := 3
	data := payload{Name: "日本語サーバ", Count: &n, Argv: []string{"sh", "-c", "sleep 1"}}

	for _, tc := range []struct {
		name   string
		format string
		want   string
	}{
		{"json", `{{json .Argv}}`, `["sh","-c","sleep 1"]`},
		{"json stays one line", `{{json .Count}} {{json .Empty}}`, "3 null"},
		{"deref", `{{deref .Count}}`, "3"},
		{"deref nil", `{{deref .Empty}}`, "<no value>"},
		{"join", `{{join " " .Argv}}`, "sh -c sleep 1"},
		{"width", `{{width .Name}}`, "12"}, // 6 runes, double width each
		{"pad", `{{pad "hi" 5}}|`, "hi   |"},
		{"trunc", `{{trunc .Name 6}}`, "日本…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := template.New("t").Funcs(templateutil.FuncMap()).Parse(tc.format)
			assert.NilError(t, err)

			var got bytes.Buffer
			assert.NilError(t, tmpl.Execute(&got, data))
			assert.Equal(t, got.String(), tc.want)
		})
	}
}

// TestFuncMapIsFresh documents that callers may extend the returned map — the
// cmdman/cli renderers add their domain helpers to it — without the addition
// leaking into the next caller's map.
func TestFuncMapIsFresh(t *testing.T) {
	m := templateutil.FuncMap()
	m["extra"] = func() string { return "" }
	_, ok := templateutil.FuncMap()["extra"]
	assert.Assert(t, !ok)
}

// TestJSONReportsErrorInline pins the deliberate deviation from the scaffold
// template: a marshal failure renders inline instead of aborting execution, so
// one unmarshalable field cannot blank out a whole listing.
func TestJSONReportsErrorInline(t *testing.T) {
	got := templateutil.JSON(make(chan int))
	assert.Assert(t, strings.HasPrefix(got, "ERR: "), "got %q", got)
}
