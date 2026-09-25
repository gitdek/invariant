package scope

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repo makes a git repository with one existing project, examples/01-old,
// on main, and returns its directory.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	write(t, dir, map[string]string{
		"README.md":                  "hello\n",
		".github/workflows/gate.yml": "name: gate\n",
		"examples/01-old/.invariant/invariant.json": `{"name": "old"}`,
		"examples/01-old/.invariant/ratified.lock":  `{"decision": "D-0001"}`,
		"examples/01-old/go.mod":                    "module example.com/old\n\ngo 1.27.1\n",
		"examples/01-old/old/old.go":                "package old\n",
	})
	commit(t, dir, "base")
	gitRun(t, dir, "checkout", "-q", "-b", "invariant/issue-7-new")
	return dir
}

const ratifiedLock = `{"ratified": {"by": "gitdek", "issue": 7, "comment": "https://example.com/c", "proposal": "sha256:ab"}, "bounds": {}, "statements": []}`

func newProject(extra map[string]string) map[string]string {
	files := map[string]string{
		"examples/02-new/.invariant/invariant.json": `{"name": "new"}`,
		"examples/02-new/.invariant/ratified.lock":  ratifiedLock,
		"examples/02-new/go.mod":                    "module example.com/new\n\ngo 1.27.1\n",
		"examples/02-new/buffer/buffer.go":          "package buffer\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestANewRatifiedProjectIsInScope(t *testing.T) {
	dir := repo(t)
	write(t, dir, newProject(nil))
	commit(t, dir, "new project")
	r, err := Check(context.Background(), dir, "main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || r.Project != "examples/02-new" || !r.New || len(r.Files) != 4 {
		t.Fatalf("result = %+v", r)
	}
}

func TestOutOfScope(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"ci config":       {newProject(map[string]string{".github/workflows/gate.yml": "name: pwned\n"}), "it edits CI configuration: .github/workflows/gate.yml"},
		"outside":         {newProject(map[string]string{"README.md": "changed\n"}), "it changes README.md, which isn't in a project"},
		"two projects":    {newProject(map[string]string{"examples/01-old/old/old.go": "package old // changed\n"}), "it changes more than one project: examples/01-old, examples/02-new"},
		"old lock":        {map[string]string{"examples/01-old/.invariant/ratified.lock": `{"decision": "D-0002"}`}, "it changes the ratified lock of an existing project"},
		"unratified":      {newProject(map[string]string{"examples/02-new/.invariant/ratified.lock": `{"bounds": {}, "statements": []}`}), "it adds a project whose lock has no ratification record"},
		"dependency":      {newProject(map[string]string{"examples/02-new/go.mod": "module example.com/new\n\ngo 1.27.1\n\nrequire (\n\tgolang.org/x/sync v0.1.0 // indirect\n)\n"}), "it adds a module dependency: golang.org/x/sync"},
		"one-line import": {newProject(map[string]string{"examples/02-new/go.mod": "module example.com/new\n\ngo 1.27.1\n\nrequire github.com/evil/dep v1.0.0\n"}), "it adds a module dependency: github.com/evil/dep"},
		"cgo":             {newProject(map[string]string{"examples/02-new/buffer/c.go": "package buffer\n\nimport \"C\"\n"}), "examples/02-new/buffer/c.go uses cgo"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := repo(t)
			write(t, dir, tc.files)
			commit(t, dir, name)
			r, err := Check(context.Background(), dir, "main", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if r.OK() || !contains(r.Problems, tc.want) {
				t.Errorf("problems = %q; want %q", r.Problems, tc.want)
			}
		})
	}
}

func TestRequires(t *testing.T) {
	got := requires("module m\n\ngo 1.27\n\nrequire a.com/x v1.0.0\nrequire (\n\tb.com/y v0.1.0 // indirect\n\n\tc.com/z v2.0.0\n)\n")
	if want := map[string]bool{"a.com/x": true, "b.com/y": true, "c.com/z": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("requires = %v", got)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", msg)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
