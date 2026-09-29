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
		t.Skip("the fake docker and go are shell scripts")
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
	write(bin, "docker", `#!/bin/sh
[ "$1" = run ] || exit 0
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
`)
	write(bin, "go", `#!/bin/sh
case "$*" in
*-run*) printf '=== RUN   TestDoubleDoubles\n--- PASS: TestDoubleDoubles (0.00s)\nPASS\n' >&2 ;;
vet*) echo "go vet wrote this" >&2 ;;
test*) echo "go test wrote this" >&2 ;;
esac
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
