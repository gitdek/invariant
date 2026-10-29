package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Shell operands and the workspace must be resolved before inside compares
// them. These pairs represent already canonical paths, so /private/work
// and /work are distinct directories, not aliases to guess from spelling.
// Testing that boundary directly needs no permission to create root-level
// directories and catches the prefix collision on both Linux and macOS.
// The transcript cases below also exercise real resolution and publication.
func TestShellFilesUseCanonicalContainment(t *testing.T) {
	for _, c := range []struct {
		name, root, candidate, want string
		ok                          bool
	}{
		{"private-descendant", "/private/work/agent", "/private/work/agent/src/app.go", "src/app.go", true},
		{"ordinary-descendant", "/work/agent", "/work/agent/src/app.go", "src/app.go", true},
		{"private-root-escape", "/private/work/agent", "/work/agent/OUTSIDE_CANARY", "", false},
		{"private-target-escape", "/work/agent", "/private/work/agent/OUTSIDE_CANARY", "", false},
		{"sibling-prefix", "/private/work/agent", "/private/work/agent-other/OUTSIDE_CANARY", "", false},
		{"parent", "/private/work/agent", "/private/work/OUTSIDE_CANARY", "", false},
	} {
		t.Logf("canonical case: %s", c.name)
		got, ok := inside(filepath.FromSlash(c.root), filepath.FromSlash(c.candidate))
		if got != c.want || ok != c.ok {
			t.Errorf("canonical containment of %q in %q = (%q, %v); want (%q, %v)", c.candidate, c.root, got, ok, c.want, c.ok)
		}
	}

	for _, provider := range []string{"codex", "claude-code"} {
		t.Logf("transcript provider: %s", provider)
		f := newShell211Fixture(t, provider)
		safe, err := filepath.EvalSymlinks(f.file(t, "safe.go"))
		if err != nil {
			t.Fatal(err)
		}
		// A genuine workspace alias is allowed after resolution. Its
		// canonical absolute operand must still appear as safe.go.
		alias := filepath.Join(t.TempDir(), "copy")
		if err := os.Symlink(f.workspace, alias); err != nil {
			t.Fatal(err)
		}
		f.workspace = alias
		init := map[string]any{"type": "system", "subtype": "init", "cwd": alias}
		if provider == "codex" {
			init = map[string]any{"type": "invariant.workspace", "cwd": alias, "timestamp": f.at(0)}
		}
		if err := os.WriteFile(f.transcript, []byte(shell211JSON(t, init)), 0o644); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "OUTSIDE_CANARY"), []byte("PRIVATE_OUTSIDE_CONTENT"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(alias, "ESCAPING_DIRECTORY")); err != nil {
			t.Fatal(err)
		}
		f.call(t, "read", `cat "`+safe+`" ESCAPING_DIRECTORY/OUTSIDE_CANARY`, 1)
		f.call(t, "search", `rg -n PRIVATE_PATTERN "`+safe+`" ESCAPING_DIRECTORY/OUTSIDE_CANARY`, 10)
		l := f.read(t)
		if want := map[string]int{"read": 1, "search": 1}; !reflect.DeepEqual(l.Tools, want) {
			t.Errorf("tools = %v; want %v", l.Tools, want)
		}
		if want := map[string][2]int{"safe.go": {2, 0}}; !reflect.DeepEqual(shell211Files(l), want) {
			t.Errorf("files = %v; want %v, with the escaping directory omitted", shell211Files(l), want)
		}
		rec := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/live.json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/live.json: status %d, want 200", rec.Code)
		}
		for _, private := range []string{"OUTSIDE_CANARY", "ESCAPING_DIRECTORY", "PRIVATE_", alias, outside, safe} {
			if strings.Contains(rec.Body.String(), private) {
				t.Errorf("public live response contains %q", private)
			}
		}
	}
}

// Shell cd defaults to logical traversal: with link -> real/sub, the
// parent of the logical link is the workspace, not real. Both possible
// top.go files exist so physical traversal cannot hide behind a missing
// operand. The issue also permits rejecting these ambiguous invocations
// as other, in which case none of their operands may be published.
// The ordinary cd case requires useful classification and makes this test
// fail on a reader that simply treats every shell command as other.
func TestShellCDThroughSymlinksUsesLogicalPathsOrOther(t *testing.T) {
	cases := []struct {
		name, command, kind, path string
		mayBeOther                bool
	}{
		{"ordinary-parent", `cd real/sub/.. && cat top.go`, "read", "real/top.go", false},
		{"logical-parent", `cd link/.. && cat top.go`, "read", "top.go", true},
		{"successive-cd", `cd link && cd .. && cat top.go`, "read", "top.go", true},
		{"search", `cd link/.. && rg -n PRIVATE_PATTERN top.go`, "search", "top.go", true},
		{"shell-wrapper", `/bin/sh -lc 'cd link/.. && cat top.go'`, "read", "top.go", true},
	}
	for _, provider := range []string{"codex", "claude-code"} {
		t.Logf("transcript provider: %s", provider)
		for _, c := range cases {
			t.Logf("%s: %s", c.name, c.command)
			f := newShell211Fixture(t, provider)
			f.file(t, "top.go")
			f.file(t, "real/top.go")
			if err := os.MkdirAll(filepath.Join(f.workspace, "real", "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join("real", "sub"), filepath.Join(f.workspace, "link")); err != nil {
				t.Fatal(err)
			}
			f.call(t, "cd", c.command, 1)
			l := f.read(t)
			kind := c.kind
			wantFiles := map[string][2]int{c.path: {1, 0}}
			if c.mayBeOther && l.Tools["other"] == 1 {
				kind = "other"
				wantFiles = map[string][2]int{}
			}
			if want := map[string]int{kind: 1}; !reflect.DeepEqual(l.Tools, want) {
				t.Errorf("%q: tools = %v; want %v", c.command, l.Tools, want)
			}
			if got := shell211Files(l); !reflect.DeepEqual(got, wantFiles) {
				t.Errorf("%q: files = %v; want %v for %s", c.command, got, wantFiles, kind)
			}
			if len(l.Marks) != 1 {
				t.Fatalf("marks = %+v; want one invocation", l.Marks)
			}
			shell211Mark(t, f, l.Marks[0], kind, 1, 3)
		}
	}
}
