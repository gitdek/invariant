package plumbing

import (
	"context"
	"fmt"
	"os"
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
		t.Skip("the fake docker and go need POSIX processes")
	}
	bin, root := t.TempDir(), t.TempDir()
	write := func(dir, name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// docker image inspect finds every image, and docker run runs its script
	// on this machine, with the run's -e variables, and hands over everything
	// the script wrote to stderr only after everything it wrote to stdout.
	write(bin, "docker", `#!/usr/bin/env python3
import os, shlex, subprocess, sys, tempfile
args = sys.argv[1:]
if not args or args[0] != "run":
    sys.exit(0)
env = os.environ.copy()
src = None
for i, arg in enumerate(args[:-1]):
    if arg == "-e":
        name, value = args[i + 1].split("=", 1)
        env[name] = value
    elif arg == "-v" and args[i + 1].endswith(":/src"):
        src = args[i + 1][:-len(":/src")]
if src is None:
    sys.exit("fake docker did not find the source mount")
script = args[-1].replace("cd /src\n", "cd " + shlex.quote(src) + "\n")
with tempfile.TemporaryFile() as stderr:
    result = subprocess.run(["sh", "-c", script], env=env,
                            cwd=env["FAKE_DOCKER_WORK"], stderr=stderr)
    stderr.seek(0)
    sys.stderr.buffer.write(stderr.read())
sys.exit(result.returncode)
`)
	write(bin, "go", `#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
if args[0] == "vet":
    print("go vet wrote this", file=sys.stderr)
elif args[0] == "list":
    print(json.dumps({"ImportPath": "example.com/double", "Dir": os.getcwd()}))
elif args[0] == "test" and "-c" in args:
    binary = pathlib.Path(args[args.index("-o") + 1])
    binary.write_text('''#!/usr/bin/env python3
import sys
args = sys.argv[1:]
if any(arg.startswith("-test.list=") for arg in args):
    print("TestOrdinary")
elif any(arg.startswith("-test.run=") and "TestDoubleDoubles" in arg for arg in args):
    print("=== RUN   TestDoubleDoubles\\n--- PASS: TestDoubleDoubles (0.00s)\\nPASS", file=sys.stderr)
else:
    print("go test wrote this\\n=== RUN   TestOrdinary\\n--- PASS: TestOrdinary (0.00s)\\nPASS", file=sys.stderr)
''')
    binary.chmod(0o755)
elif args[:2] == ["tool", "test2json"]:
    package = args[args.index("-p") + 1]
    def emit(action, **fields):
        print(json.dumps({"Action": action, "Package": package, **fields}), flush=True)
    for line in sys.stdin:
        emit("output", Output=line)
        if line.startswith("=== RUN   "):
            emit("run", Test=line.split()[2])
        elif line.startswith("--- PASS: "):
            emit("pass", Test=line.split()[2])
        elif line.strip() == "PASS":
            emit("pass")
else:
    sys.exit("unexpected fake go invocation: " + repr(args))
`)
	t.Setenv("FAKE_DOCKER_WORK", t.TempDir())
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
