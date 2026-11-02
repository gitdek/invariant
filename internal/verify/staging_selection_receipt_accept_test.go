package verify_test

// Issue #400 records selection evidence at the root each language stages:
// Go's module (which may be below the project), standalone TypeScript's
// project, and existing TypeScript's containing package. The public Report
// adds optional FileSelection, serialized as file_selection. Its values are
// git and directory, and Markdown explains git ls-files or whole directory.
// Model-only and old decoded reports with no evidence must not invent it.
//
// These tests exercise the real gate, copying, hashing, JSON and Markdown.
// Only Docker/verifier processes are doubled; the doubles inspect the staged
// files before returning tool results. No daemon, network or proof is needed.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func se400Git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func se400IsolateGit(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

func se400Tools(t *testing.T) toolchain.Toolchain {
	t.Helper()
	bin := t.TempDir()
	assuranceWrite(t, bin, "go", "#!/bin/sh\necho 'go version go1.27.1 linux/amd64'\n", 0755)
	assuranceWrite(t, bin, "docker", `#!/bin/sh
if [ "$1" = image ] || [ "$1" = rm ]; then exit 0; fi
src= out= kind=
for arg in "$@"; do
  case "$arg" in
    *:/src) src=${arg%:/src} ;;
    *:/out) out=${arg%:/out} ;;
    selection400-java) kind=java ;;
    selection400-gobra) kind=gobra ;;
    selection400-go) kind=go ;;
    selection400-node) kind=node ;;
    invariant-deps:*) kind=existing ;;
  esac
done
case "$kind" in
  java)
    cat <<'TLC'
@!@!@STARTMSG 2193:0 @!@!@
Model checking completed. No error has been found.
@!@!@ENDMSG 2193 @!@!@
@!@!@STARTMSG 2199:0 @!@!@
1 states generated, 1 distinct states found, 0 states left on queue.
@!@!@ENDMSG 2199 @!@!@
@!@!@STARTMSG 2194:0 @!@!@
The depth of the complete state graph search is 1.
@!@!@ENDMSG 2194 @!@!@
TLC
    exit 0 ;;
  gobra) echo 'Gobra found 0 errors'; exit 0 ;;
  go) required=core.go; cache=local.cache; [ -f "$src/go.mod" ] || exit 81 ;;
  node) required=core.js; cache=local.cache ;;
  existing) required=src/lib/core.js; cache=src/lib/local.cache; [ -f "$src/package-lock.json" ] || exit 82 ;;
  *) echo 'unexpected tool invocation' >&2; exit 83 ;;
esac
[ -f "$src/$required" ] || { echo 'required source was not staged' >&2; exit 84; }
if [ "$SE400_SELECTION" = git ]; then
  [ ! -e "$src/$cache" ] || exit 85
else
  [ -f "$src/$cache" ] || { echo 'directory selection omitted payload' >&2; exit 86; }
fi
case "$kind" in
  go) printf '@@invariant vet=0\n@@invariant test=0\ninvariant-agreement states=1 depth=1\n@@invariant agree=0\n' ;;
  node|existing)
    printf '{"traces":[[{"n":0},{"n":0}]]}' > "$out/traces.json"
    if [ "$kind" = node ]; then printf '@@invariant test=0\n'; fi
    printf '@@invariant conform=0\n' ;;
esac
`, 0755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return toolchain.Toolchain{JavaImage: "selection400-java", GobraImage: "selection400-gobra", GoImage: "selection400-go", NodeImage: "selection400-node", TLCJar: filepath.Join(bin, "tla.jar")}
}

func se400Fixture(t *testing.T, kind, mode string) (dir, stagingRoot string, existing []verify.ExistingCode) {
	t.Helper()
	outer := t.TempDir()
	dir = filepath.Join(outer, "project")
	stagingRoot = dir
	language, code, driver := "typescript", ".", "driver.js"
	var selections []string
	switch kind {
	case "go":
		language, code, driver = "go", "implementation", ""
		stagingRoot = filepath.Join(dir, code)
		assuranceWrite(t, stagingRoot, "go.mod", "module example.test/selection\n\ngo 1.27.1\n", 0644)
		assuranceWrite(t, stagingRoot, "core.go", "package core\ntype State int\nfunc Init() State { return 0 }\nfunc Successors(State) []State { return nil }\n", 0644)
		assuranceWrite(t, stagingRoot, "local.cache", "directory payload\n", 0644)
	case "typescript":
		assuranceWrite(t, dir, "core.js", "export const n = 0;\n", 0644)
		assuranceWrite(t, dir, driver, "import { n } from './core.js';\n", 0644)
		assuranceWrite(t, dir, "local.cache", "directory payload\n", 0644)
	case "existing-typescript":
		stagingRoot = filepath.Join(outer, "package")
		dir = filepath.Join(stagingRoot, "verification", "check")
		selections = []string{"src/lib"}
		assuranceWrite(t, stagingRoot, "package.json", `{"name":"selection","type":"module"}`, 0644)
		assuranceWrite(t, stagingRoot, "package-lock.json", `{"lockfileVersion":3}`, 0644)
		assuranceWrite(t, stagingRoot, "src/lib/core.js", "export const n = 0;\n", 0644)
		assuranceWrite(t, stagingRoot, "src/lib/local.cache", "directory payload\n", 0644)
		assuranceWrite(t, dir, driver, "import { n } from '../../src/lib/core.js';\n", 0644)
	default:
		t.Fatalf("unknown fixture kind %s", kind)
	}
	src := "---- MODULE Selection ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_vars\nSafe == n = 0 /\\ n <= Limit\n====\n"
	statements := []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}
	for i := range statements {
		h, err := tla.PinHash(src, statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		statements[i].SHA256 = h
	}
	manifest := project.Manifest{Name: "selection evidence", Language: language, Code: code, Module: ".invariant/specs/Selection.tla", Conformance: driver, Existing: selections}
	lock := project.Lock{Decision: "selection-fixture-only", Bounds: map[string]string{"Limit": "1"}, Statements: statements}
	for name, value := range map[string]any{".invariant/invariant.json": manifest, ".invariant/ratified.lock": lock} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		assuranceWrite(t, dir, name, string(data), 0644)
	}
	assuranceWrite(t, dir, ".invariant/specs/Selection.tla", src, 0644)
	if mode != "directory" {
		se400Git(t, outer, "init", "--quiet")
		if mode == "git" {
			assuranceWrite(t, outer, ".gitignore", "*.cache\n", 0644)
			se400Git(t, outer, "add", "--all")
		} else {
			// Keep the project's model visible; for Go only its nested module
			// is ignored. Inferring selection from p.Dir would say git here.
			rel, err := filepath.Rel(outer, stagingRoot)
			if err != nil {
				t.Fatal(err)
			}
			assuranceWrite(t, outer, ".gitignore", "/"+filepath.ToSlash(rel)+"/\n", 0644)
			assuranceWrite(t, stagingRoot, "retired.txt", "indexed then deleted\n", 0644)
			se400Git(t, outer, "add", "-f", filepath.ToSlash(filepath.Join(rel, "retired.txt")))
			if err := os.Remove(filepath.Join(stagingRoot, "retired.txt")); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Expected TypeScript digest comes from a directory with exactly the
	// files StagePackage must copy, independent of the report's calculation.
	if kind == "existing-typescript" {
		expected := t.TempDir()
		assuranceWrite(t, expected, "src/lib/core.js", "export const n = 0;\n", 0644)
		if mode != "git" {
			assuranceWrite(t, expected, "src/lib/local.cache", "directory payload\n", 0644)
		}
		var err error
		existing, err = verify.HashExisting(expected, selections)
		if err != nil {
			t.Fatal(err)
		}
	}
	return dir, stagingRoot, existing
}

func se400Receipt(t *testing.T, kind string) {
	t.Helper()
	se400IsolateGit(t)
	tc := se400Tools(t)
	for _, mode := range []string{"git", "directory", "ignored-deleted"} {
		t.Run(mode, func(t *testing.T) {
			want := mode
			if mode == "ignored-deleted" {
				want = "directory"
			}
			t.Setenv("SE400_SELECTION", want)
			dir, _, existing := se400Fixture(t, kind, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			r, err := verify.Run(ctx, dir, "", tc)
			if err != nil || r == nil {
				t.Fatalf("gate did not return a receipt: %v", err)
			}
			if !r.Passed || !r.Build.Passed || !r.Design.Passed {
				t.Fatalf("fixture did not finish its checks: %+v", r)
			}
			if !reflect.DeepEqual(r.Existing, existing) {
				t.Errorf("existing-code digest = %+v, want %+v", r.Existing, existing)
			}
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["file_selection"]) != `"`+want+`"` {
				t.Errorf("receipt file_selection = %s, want %q", fields["file_selection"], want)
			}
			phrase := "git ls-files"
			if want == "directory" {
				phrase = "whole directory"
			}
			var decoded verify.Report
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			for _, report := range []*verify.Report{r, &decoded} {
				if markdown := strings.ToLower(receipt.Markdown(report)); !strings.Contains(markdown, phrase) {
					t.Errorf("Markdown does not explain %q selection:\n%s", want, markdown)
				}
			}
			if r.Fingerprint != verify.Fingerprint(*r) || verify.Fingerprint(decoded) != r.Fingerprint {
				t.Error("fingerprint was not finalized with portable selection evidence")
			}
			other := "directory"
			if want == "directory" {
				other = "git"
			}
			fields["file_selection"] = json.RawMessage(`"` + other + `"`)
			changed, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var changedReport verify.Report
			if err := json.Unmarshal(changed, &changedReport); err != nil {
				t.Fatal(err)
			}
			if verify.Fingerprint(changedReport) == r.Fingerprint {
				t.Error("fingerprint does not bind file_selection")
			}
			delete(fields, "file_selection")
			oldJSON, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var oldReport verify.Report
			if err := json.Unmarshal(oldJSON, &oldReport); err != nil {
				t.Fatal(err)
			}
			oldMarkdown := strings.ToLower(receipt.Markdown(&oldReport))
			if strings.Contains(oldMarkdown, "git ls-files") || strings.Contains(oldMarkdown, "whole directory") {
				t.Error("an old receipt without selection evidence invented a method")
			}
			model, err := verify.RunModel(ctx, dir, tc)
			if err != nil || model == nil || !model.Passed {
				t.Fatalf("model-only fixture: %v", err)
			}
			modelJSON, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(modelJSON), `"file_selection"`) {
				t.Error("model-only receipt invented a code selection")
			}
		})
	}
}

func TestGoStagingSelectionReceipt(t *testing.T) {
	se400Receipt(t, "go")
}

func TestTypeScriptStagingSelectionReceipt(t *testing.T) {
	se400Receipt(t, "typescript")
}

func TestExistingTypeScriptStagingSelectionReceipt(t *testing.T) {
	se400Receipt(t, "existing-typescript")
}
