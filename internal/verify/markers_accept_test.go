package verify

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// A step's marker can land after other text on its line: after unittest's
// dots in #123's run, and after the last line of any step whose output ends
// without a newline (#124). Wherever it is on its line, it counts and closes
// its step, so the run splits into the same sections, with the same exit
// codes, as it does with each marker on a line of its own.
func TestAMarkerAfterTextOnItsLineSplitsTheSame(t *testing.T) {
	same := func(own string, want ...section) {
		t.Helper()
		sections, err := splitMarkers(own, len(want))
		if err != nil {
			t.Fatalf("with each marker on a line of its own: %q", err.Error())
		}
		if !reflect.DeepEqual(sections, want) {
			t.Fatalf("with each marker on a line of its own, the sections are %#v; want %#v", sections, want)
		}
		after := strings.ReplaceAll(own, "\n@@invariant", "@@invariant")
		got, err := splitMarkers(after, len(want))
		if err != nil {
			t.Fatalf("with markers after other text on their lines, not every marker counted: %q", err.Error())
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("with markers after other text on their lines, the sections are %#v; want %#v", got, want)
		}
	}
	same("@@invariant compile=0\n.......\n@@invariant test=0\nthe driver wrote this\n@@invariant conform=0\n",
		section{"", 0}, section{".......", 0}, section{"the driver wrote this", 0})
	same("./counter.go:4:2: unreachable code\n@@invariant vet=1\nFAIL\texample.com/counter\t0.01s\n@@invariant test=1\ninvariant-agreement states=3 depth=2\n@@invariant agree=0",
		section{"./counter.go:4:2: unreachable code", 1}, section{"FAIL\texample.com/counter\t0.01s", 1}, section{"invariant-agreement states=3 depth=2", 0})
}

// dockerHandsOverStderrLast puts a docker first on PATH, with the given
// tools beside it. It finds every image, and it runs a run's script on this
// machine, with the run's -e variables, and hands over everything the script
// wrote to stderr only after everything it wrote to stdout: an order Docker,
// which carries a container's two streams apart, may deliver them in.
func dockerHandsOverStderrLast(t *testing.T, tools map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker and its tools are shell scripts")
	}
	bin := t.TempDir()
	tools["docker"] = `[ "$1" = run ] || exit 0
prev=
for a in "$@"; do
	if [ "$prev" = -e ]; then export "$a"; fi
	prev=$a
done
cd "$FAKE_DOCKER_WORK" || exit 1
sh -c "$prev" 2>stderr
code=$?
cat stderr >&2
exit $code
`
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_DOCKER_WORK", t.TempDir())
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Docker carries a sandbox's stdout and stderr apart, and may hand over what
// a step wrote to stderr after the marker echoed when it ended (#124). Every
// sandbox of the gate's sends its steps' stderr to stdout, so what a step
// writes, on either stream, lands in its own step's section: in the build's
// output, the explorer's count, and what a driver that left no traces said.
func TestEveryGateSandboxKeepsEachStepsOutputBeforeItsMarker(t *testing.T) {
	tools := map[string]string{"cp": "", "ln": ""}
	tools["go"] = `case "$*" in
*TestInvariantAgreement*) echo "invariant-agreement states=3 depth=2" >&2 ;;
vet*) echo "go vet wrote this" >&2 ;;
test*) echo "go test wrote this" >&2 ;;
esac
`
	tools["node"] = `if [ "$1" = --test ]; then echo "node --test wrote this" >&2; else echo "the driver wrote this" >&2; fi
`
	tools["python"] = `case "$*" in
*compileall*) ;;
*unittest*) printf '.......\n----------------------------------------------------------------------\nRan 7 tests in 0.002s\n\nOK\n' >&2 ;;
*) echo "the driver wrote this" >&2 ;;
esac
`
	dockerHandsOverStderrLast(t, tools)
	ctx := context.Background()

	t.Run("go", func(t *testing.T) {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{
			"go.mod":             "module example.com/counter\n\ngo 1.27.1\n",
			"counter/counter.go": "package counter\n",
			"counter/explore.go": "package counter\n",
		})
		p := &project.Project{Dir: dir, Manifest: project.Manifest{Language: "go", Code: "counter"}}
		b, ev, err := Go{GoImage: "golang"}.Check(ctx, p)
		if err != nil {
			t.Fatalf("the Go sandbox: %q", err.Error())
		}
		if !strings.Contains(b.Output, "go vet wrote this") || !strings.Contains(b.Output, "go test wrote this") {
			t.Errorf("the build's output is %q; want what go vet and go test wrote, each before its marker", b.Output)
		}
		if e := ev.Exploration; e == nil || !e.OK || e.States != 3 || e.Depth != 2 {
			t.Errorf("the exploration is %#v; want the 3 states in 2 levels the explorer wrote before its marker", e)
		}
		larger, err := Go{GoImage: "golang"}.exploreLarger(ctx, dir, p.CodeDir(), nil)
		if err != nil {
			t.Fatalf("the Go sandbox one size larger: %q", err.Error())
		}
		if !larger.OK || larger.States != 3 || larger.Depth != 2 {
			t.Errorf("one size larger, the exploration is %#v; want the 3 states in 2 levels the explorer wrote before its marker", larger)
		}
	})

	t.Run("typescript", func(t *testing.T) {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{"conformance.ts": ""})
		p := &project.Project{Dir: dir, Manifest: project.Manifest{Language: "typescript", Code: "src", Conformance: "conformance.ts"}}
		b, ev, err := TypeScript{Image: "node"}.Check(ctx, p)
		if err != nil {
			t.Fatalf("the TypeScript sandbox: %q", err.Error())
		}
		if !strings.Contains(b.Output, "node --test wrote this") {
			t.Errorf("the build's output is %q; want what node --test wrote before its marker", b.Output)
		}
		if !strings.Contains(ev.Message, "the driver wrote this") {
			t.Errorf("the evidence says %q; want what the driver wrote before its marker", ev.Message)
		}
	})

	t.Run("python", func(t *testing.T) {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{"conformance.py": ""})
		p := &project.Project{Dir: dir, Manifest: project.Manifest{Language: "python", Code: "core", Conformance: "conformance.py"}}
		b, ev, err := Python{Image: "python"}.Check(ctx, p)
		if err != nil {
			t.Fatalf("the Python sandbox: %q", err.Error())
		}
		if !strings.Contains(b.Output, "Ran 7 tests") {
			t.Errorf("the build's output is %q; want what unittest wrote before its marker", b.Output)
		}
		if !strings.Contains(ev.Message, "the driver wrote this") {
			t.Errorf("the evidence says %q; want what the driver wrote before its marker", ev.Message)
		}
	})

	t.Run("existing typescript", func(t *testing.T) {
		root := t.TempDir()
		writeFiles(t, root, map[string]string{
			"package.json":         `{"name": "counter"}`,
			"package-lock.json":    `{"lockfileVersion": 3}`,
			"src/counter.ts":       "export const count = 0;\n",
			"model/conformance.ts": "import { count } from \"../src/counter\";\n",
		})
		p := &project.Project{Dir: filepath.Join(root, "model"), Manifest: project.Manifest{Language: "typescript", Code: "src", Conformance: "conformance.ts", Existing: []string{"src"}}}
		_, ev, err := ExistingTypeScript{}.Check(ctx, p)
		if err != nil {
			t.Fatalf("the existing TypeScript sandbox: %q", err.Error())
		}
		if !strings.Contains(ev.Message, "the driver wrote this") {
			t.Errorf("the evidence says %q; want what the driver wrote before its marker", ev.Message)
		}
	})
}
