package verify_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// #893: a gate run reads each source's syntax once, and a second run of the
// same application reads none of it again. Before, each of a run's six
// resolutions of the inventory started an interpreter for every source, and
// those starts were half of what a test of the gate took.
//
// The stand-in is an interpreter that notes a digest of each source the
// syntax parser is given, and is the real one for everything else.
func TestPythonApplicationGateReadsEachSourceOnce893(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := "#!" + python + " -IS\nimport hashlib, os, subprocess, sys\n" +
		"if sys.argv[1:4] == ['-I', '-S', '-c'] and sys.argv[4].startswith('\"\"\"Inventory syntax'):\n" +
		"    data = sys.stdin.buffer.read()\n" +
		"    with open(" + strconv.Quote(calls) + ", 'a') as f:\n        f.write(hashlib.sha256(data).hexdigest() + '\\n')\n" +
		"    sys.exit(subprocess.run([" + strconv.Quote(python) + "] + sys.argv[1:], input=data).returncode)\n" +
		"os.execv(" + strconv.Quote(python) + ", [sys.argv[0]] + sys.argv[1:])\n"
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	read := func() (starts, sources int) {
		t.Helper()
		b, err := os.ReadFile(calls)
		if err != nil {
			t.Fatalf("the stand-in noted no source: %v", err)
		}
		seen := map[string]bool{}
		for _, line := range strings.Fields(string(b)) {
			starts++
			seen[line] = true
		}
		return starts, len(seen)
	}
	tc, _ := pa327Fake(t)
	f := pa327New(t, true, false)
	pa327Admit(t, f)
	pa327Assert(t, f, pa327Run(t, f, tc), true)
	starts, sources := read()
	if sources == 0 || starts != sources {
		t.Fatalf("admission and one gate run started the syntax parser %d times for %d different sources", starts, sources)
	}
	pa327Assert(t, f, pa327Run(t, f, tc), true)
	if again, _ := read(); again != starts {
		t.Fatalf("a second run of the same application read %d sources again", again-starts)
	}
}
