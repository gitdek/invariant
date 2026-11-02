package verify_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// The real public gate uses the existing local-Python transport fixture. The
// runner executes staged Python; only Docker transport and TLC are doubles.
// TypeScript preservation is part of this failing Python regression, since
// changing HashExisting globally would fix Python by breaking StagePackage.
func TestPythonSnapshotExistingEvidence(t *testing.T) {
	for _, mode := range []string{"directory", "git"} {
		t.Run(mode, func(t *testing.T) {
			pse400IsolateGit(t)
			tc, _ := pa327Fake(t)
			pse400TypeScriptHash(t, mode)
			f := pa327New(t, false, false)
			f.p.Manifest.Existing = []string{"tasks", "settings.py"}
			pa327Write(t, f.root, "settings.py", "VALUE = 23\n")
			pa327Write(t, f.root, "tasks/bin/tool.py", "def running():\n    return 'running'\n")
			pa327Write(t, f.root, "tasks/out/report.py", "def done():\n    return 'done'\n")
			pa327Write(t, f.root, "tasks/helper.py", "from .bin.tool import running\nfrom .out.report import done\ndef next_state(state):\n    return running() if state == 'pending' else done()\n")
			pa327Write(t, f.root, ".gitignore", "/tasks/ignored.py\n")
			pa327Write(t, f.root, "tasks/ignored.py", "IGNORED = True\n")
			pa327Write(t, f.root, "tasks/.venv/omitted.py", "NOT_STAGED = True\n")
			pa327Write(t, f.root, "tasks/__pycache__/omitted.py", "NOT_STAGED = True\n")
			pa327Save(t, f)
			if mode == "git" {
				pse400Git(t, f.root, "init", "--quiet")
				pse400Git(t, f.root, "add", "--all")
			}

			// Independently frame the actual staged bytes; never use HashExisting
			// as the Python oracle, since its directory exclusions are the bug.
			check := func() *verify.Report {
				t.Helper()
				w, err := verify.StagePythonApplication(f.p, filepath.Join(t.TempDir(), "snapshot"))
				if err != nil {
					t.Fatal(err)
				}
				if w.FileSelection != mode {
					t.Fatalf("snapshot selection = %q, want %q", w.FileSelection, mode)
				}
				want := make([]verify.ExistingCode, 0, len(f.p.Manifest.Existing))
				for _, selection := range f.p.Manifest.Existing {
					files := map[string][]byte{}
					for _, file := range w.Files {
						if file.Path == selection || strings.HasPrefix(file.Path, selection+"/") {
							body, err := os.ReadFile(filepath.Join(w.Root, filepath.FromSlash(file.Path)))
							if err != nil {
								t.Fatal(err)
							}
							files[file.Path] = body
						}
					}
					want = append(want, verify.ExistingCode{Path: selection, Files: len(files), SHA256: pse400Digest(files)})
				}
				taskFiles := 9
				if mode == "directory" {
					taskFiles++ // A directory snapshot retains ignored.py.
				}
				if want[0].Files != taskFiles || want[1].Files != 1 {
					t.Fatalf("unexpected staged fixture: %+v", want)
				}
				r := pa327Run(t, f, tc)
				pa327Assert(t, f, r, true)
				if !reflect.DeepEqual(r.Existing, want) {
					t.Errorf("Existing code must hash every staged file under each selection: got %+v, want %+v", r.Existing, want)
				}
				var wire struct {
					Existing []verify.ExistingCode `json:"existing"`
				}
				if err := json.Unmarshal(pa327JSON(t, r), &wire); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(wire.Existing, want) {
					t.Errorf("JSON existing evidence = %+v, want %+v", wire.Existing, want)
				}
				var row string
				for _, line := range strings.Split(receipt.Markdown(r), "\n") {
					if strings.HasPrefix(line, "| Existing code |") {
						row = line
					}
				}
				for _, code := range want {
					if !strings.Contains(row, code.Path) || !strings.Contains(row, fmt.Sprintf("%d file", code.Files)) || !strings.Contains(row, code.SHA256[:19]) {
						t.Errorf("Markdown Existing code row omits staged evidence %+v: %s", code, row)
					}
				}
				for _, execution := range r.Application.Executions {
					if execution.Provenance.FileSelection != mode || !reflect.DeepEqual(execution.Provenance.Files, w.Files) {
						t.Errorf("receipt provenance differs from the staged snapshot: %+v", execution.Provenance)
					}
				}
				return r
			}
			first := check()
			for _, name := range []string{"tasks/bin/tool.py", "tasks/out/report.py"} {
				body, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
				if err != nil {
					t.Fatal(err)
				}
				pa327Write(t, f.root, name, string(body)+"# changed staged bytes\n")
				next := check()
				if first.Existing[0].SHA256 == next.Existing[0].SHA256 {
					t.Errorf("changing staged %s did not change the Existing code hash", name)
				}
				if !reflect.DeepEqual(first.Existing[1], next.Existing[1]) {
					t.Error("changing another selection changed the explicit file's receipt")
				}
				first = next
			}
		})
	}
}

func pse400Digest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s\n%d\n", name, len(files[name]))
		h.Write(files[name])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func pse400TypeScriptHash(t *testing.T, mode string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"package.json": "{}", "package-lock.json": "{}",
		"lib/keep.ts": "export const kept = 1;\n", "lib/bin/tool.ts": "excluded bin bytes\n", "lib/out/report.ts": "excluded out bytes\n",
	} {
		pa327Write(t, root, name, body)
	}
	if mode == "git" {
		pse400Git(t, root, "init", "--quiet")
		pse400Git(t, root, "add", "--all")
	}
	want := []verify.ExistingCode{{Path: "lib", Files: 1, SHA256: pse400Digest(map[string][]byte{"lib/keep.ts": []byte("export const kept = 1;\n")})}}
	stage := filepath.Join(t.TempDir(), "package")
	if err := verify.StagePackage(root, []string{"lib"}, stage); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lib/bin/tool.ts", "lib/out/report.ts"} {
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Errorf("TypeScript staged an excluded path %s: %v", name, err)
		}
	}
	for _, dir := range []string{root, stage} {
		got, err := verify.HashExisting(dir, []string{"lib"})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("TypeScript HashExisting changed its established package selection: got %+v, err %v, want %+v", got, err, want)
		}
	}
	pa327Write(t, root, "lib/bin/tool.ts", "changed excluded bin bytes\n")
	pa327Write(t, root, "lib/out/report.ts", "changed excluded out bytes\n")
	got, err := verify.HashExisting(root, []string{"lib"})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("excluded TypeScript changes altered its existing-code hash: got %+v, err %v, want %+v", got, err, want)
	}
}

func pse400IsolateGit(t *testing.T) {
	t.Helper()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

func pse400Git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
