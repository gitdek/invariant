package plumbing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// A step's marker can land after other text on its line, as a gate run's did
// after unittest's dots in #123 (#124). Wherever a marker is on its line, it
// counts, so a run reads the same as it does with each marker on a line of
// its own: the same go vet and test results, and the same acceptance tests
// passed.
func TestAPlumbingMarkerAfterTextOnItsLineCountsTheSame(t *testing.T) {
	p := &Plan{Tests: []Test{{Name: "TestDoubleDoubles", File: "double_accept_test.go"}}}
	for _, code := range []int{0, 1} {
		own := strings.Join([]string{
			"go vet wrote this",
			fmt.Sprintf("@@invariant vet=%d", code),
			"go test wrote this",
			fmt.Sprintf("@@invariant test=%d", code),
			"=== RUN   TestDoubleDoubles",
			"--- PASS: TestDoubleDoubles (0.00s)",
			"PASS",
			"@@invariant accept=0",
		}, "\n") + "\n"
		want := parse(own, p)
		if want.Vet != (code == 0) || want.Tests != (code == 0) || !want.Accepted["double_accept_test.go:TestDoubleDoubles"] {
			t.Fatalf("with each marker on a line of its own and exit code %d, vet %v, tests %v, accepted %v", code, want.Vet, want.Tests, want.Accepted)
		}
		got := parse(strings.ReplaceAll(own, "\n@@invariant", "@@invariant"), p)
		if got.Vet != want.Vet || got.Tests != want.Tests || !reflect.DeepEqual(got.Accepted, want.Accepted) || !reflect.DeepEqual(got.Gofmt, want.Gofmt) {
			t.Errorf("with markers after other text on their lines and exit code %d, vet %v, tests %v, accepted %v, gofmt %q; want vet %v, tests %v, accepted %v, gofmt %q",
				code, got.Vet, got.Tests, got.Accepted, got.Gofmt, want.Vet, want.Tests, want.Accepted, want.Gofmt)
		}
	}
}

// Docker carries the sandbox's stdout and stderr apart, and may hand over
// what a step wrote to stderr after the marker echoed when it ended (#124).
// The sandbox sends its steps' stderr to stdout, so the run's output has what
// go vet, the tests and the acceptance tests wrote each before its own
// marker.
func TestThePlumbingSandboxKeepsEachStepsOutputBeforeItsMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker and go are shell scripts")
	}
	bin, root := t.TempDir(), t.TempDir()
	write := func(dir, name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Run the staged entry script from the mounted checkout with the run's
	// environment. Deliver stderr only after stdout to reproduce Docker's
	// separate streams and expose any step whose stderr isn't redirected.
	write(bin, "docker", `#!/usr/bin/env python3
import os, shlex, subprocess, sys
a = sys.argv[1:]
if not a or a[0] != "run": sys.exit(0)
env, mounts = dict(os.environ), {}
i = 1
while i < len(a):
    if a[i] == "-e":
        key, _, value = a[i+1].partition("=")
        env[key] = value
        i += 2
    elif a[i] == "-v":
        parts = a[i+1].split(":")
        mounts[parts[1]] = parts[0]
        i += 2
    else:
        i += 1
for key, value in list(env.items()):
    if value in mounts: env[key] = mounts[value]
script = a[-1].replace("cd /src", "cd " + shlex.quote(mounts["/src"]))
p = subprocess.run(["sh", "-c", script], cwd=mounts["/src"], env=env, stderr=subprocess.PIPE)
sys.stderr.buffer.write(p.stderr)
sys.exit(p.returncode)
`)
	// Compile a fixture executable once and let the real test2json converter
	// report its events. Like a Go test binary run with -test.v=test2json, it
	// marks its framing lines with ^V. Extra spacing in its ordinary PASS
	// report preserves the assertion below that identifies the acceptance
	// replay's own output.
	write(bin, "go", `#!/usr/bin/env python3
import json, os, pathlib, sys
a = sys.argv[1:]
if a[0] == "list":
    print(json.dumps(dict(ImportPath="example.com/double", Dir=os.getcwd(), TestGoFiles=["double_accept_test.go"])))
elif a[0] == "vet":
    print("go vet wrote this", file=sys.stderr)
elif a[0] == "test" and "-c" in a:
    target = pathlib.Path(a[a.index("-o")+1])
    target.write_text('''#!/usr/bin/env python3
import pathlib, sys
if any(arg.startswith("-test.list") for arg in sys.argv[1:]):
    print("TestDoubleDoubles")
    sys.exit(0)
seen = pathlib.Path(".marker-ordinary-completed")
space = " "
if not seen.exists():
    seen.write_text("completed")
    print("go test wrote this", file=sys.stderr)
    space = "  "
frame = "\\x16"
print(frame + "=== RUN   TestDoubleDoubles", file=sys.stderr)
print(frame + "--- PASS:" + space + "TestDoubleDoubles (0.00s)", file=sys.stderr)
print(frame + "PASS", file=sys.stderr)
''')
    target.chmod(0o755)
elif a[:2] == ["tool", "test2json"]:
    real = os.environ["MARKERS_REAL_GO"]
    os.execv(real, [real] + a)
else:
    sys.exit("unexpected fake Go invocation: " + repr(a))
`)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARKERS_REAL_GO", realGo)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	write(root, "go.mod", "module example.com/double\n\ngo 1.27.1\n")
	write(root, "double.go", "package double\n")

	p := &Plan{Name: "double", Summary: "Double doubles.", Files: []string{"double.go"},
		Tests: []Test{{Name: "TestDoubleDoubles", File: "double_accept_test.go", Says: "Double(2) is 4."}}}
	r, err := Sandbox{Image: "golang", ModCache: t.TempDir()}.Run(context.Background(), root, p)
	if err != nil {
		t.Fatalf("the sandbox: %q", err.Error())
	}
	if !r.Vet || !r.Tests || !r.Accepted["double_accept_test.go:TestDoubleDoubles"] {
		t.Errorf("vet %v, tests %v, accepted %v; want everything to pass: %q", r.Vet, r.Tests, r.Accepted, r.Output)
	}
	at := -1
	for _, s := range []string{"go vet wrote this", "@@invariant vet=0", "go test wrote this", "@@invariant test=0", "--- PASS: TestDoubleDoubles", "@@invariant accept=0"} {
		i := strings.Index(r.Output, s)
		if i <= at {
			t.Fatalf("%q doesn't come after what comes before it in the run's output: %q", s, r.Output)
		}
		at = i
	}
}
