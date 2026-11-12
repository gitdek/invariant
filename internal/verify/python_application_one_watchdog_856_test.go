package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// One watchdog serves every step of a run (#856), and each step still has a
// deadline of its own: here the first step finishes, and the second sleeps
// on the driver's thread, where the collector can't run to expire it. Only
// the watchdog, armed again for that step, can stop it.
func TestPythonApplicationModuleLaterStepKeepsItsDeadline856(t *testing.T) {
	t.Parallel()
	source := pa321State + "def advance(key, status):\n    finish(key, status)\n" +
		"def submit(request, *, status):\n    if status == 'other':\n        time.sleep(30)\n    advance(reserve(request), status)\n"
	_, p := pa321Fixture(t, source, pa321Deadline)
	s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
	ctx := pa321Context(t)
	r, err := s.Run(ctx, pa318Driver(t, s).Key)
	pa318Reject(t, r, err)
	if ctx.Err() != nil {
		t.Fatal("the second step's deadline did not stop the run before the test's own")
	}
	if err == nil || !strings.Contains(err.Error(), "stopped before sealing its observations") {
		t.Fatalf("the stop was reported as %v", err)
	}
	if !strings.Contains(strings.Join(r.Diagnostics, "\n"), "module observation timed out") {
		t.Fatalf("the diagnostics don't name the timeout: %q", r.Diagnostics)
	}
}

// A run can't go on without its watchdog (#856). One that dies before it
// says a step's deadline is disarmed, or answers anything but armed when a
// step arms it, fails the run, and none of the run's observations count.
// The stand-in is an interpreter that changes one line of the watchdog's
// program, as the stand-in for a late start does (#645).
func TestPythonApplicationModuleWatchdogMustAnswer856(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, line, with, want string }{
		{"dies before it disarms", "control.sendall(b'0')", "os._exit(3)", "module deadline watchdog failed"},
		{"answers wrongly when armed", "control.sendall(b'1')", "control.sendall(b'2')", "module deadline watchdog failed to start"},
		{"dies when armed", "control.sendall(b'1')", "os._exit(3)", "module deadline watchdog failed to start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!" + python + " -IS\nimport os, sys\n" +
				"if sys.argv[1:4] == ['-I', '-S', '-c'] and sys.argv[4].startswith('import os, select, signal, socket, sys, time'):\n" +
				"    if " + strconv.Quote(tc.line) + " not in sys.argv[4]:\n        sys.exit('the watchdog program has no such line')\n" +
				"    sys.argv[4] = sys.argv[4].replace(" + strconv.Quote(tc.line) + ", " + strconv.Quote(tc.with) + ", 1)\n" +
				"os.execv(" + strconv.Quote(python) + ", [sys.argv[0]] + sys.argv[1:])\n"
			if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			_, p := pa321Fixture(t, pa321State+pa321Worker, pa321Deadline)
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			ctx := pa321Context(t)
			r, err := s.Run(ctx, pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if ctx.Err() != nil {
				t.Fatal("the run waited on its watchdog until the test's own deadline")
			}
			if r.HarnessError != tc.want {
				t.Fatalf("want the run to fail on %q, got %q: %q, %v", tc.want, r.HarnessError, r.Diagnostics, err)
			}
		})
	}
}
