package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A loaded runner can take seconds to start a step's watchdog. Its stand-in
// here is an interpreter that starts every watchdog late, by more than the
// step's whole two-second budget. That start spends none of the budget
// (#645).
func TestPythonApplicationModuleDeadlineStartsWithItsWatchdog(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	// The stand-in re-executes the interpreter under its own name, so it stays
	// sys.executable, which starts the watchdog. Only the watchdog's program
	// is late; the gate's own Python checks find it on PATH too. It's Python,
	// not a shell script, because hosts such as Alpine have no bash.
	script := "#!" + python + " -IS\nimport os, sys, time\n" +
		"if sys.argv[1:4] == ['-I', '-S', '-c'] and sys.argv[4].startswith('import os, select, signal, sys, time'):\n" +
		"    with open(" + strconv.Quote(started) + ", 'a') as f:\n        f.write('watchdog ' + sys.argv[6] + '\\n')\n" +
		"    time.sleep(2.5)\n" +
		"os.execv(" + strconv.Quote(python) + ", [sys.argv[0]] + sys.argv[1:])\n"
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, p := pa321Fixture(t, pa321State+pa321Worker, pa321Driver)
	s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
	r := pa318Success(t, pa321Context(t), s)
	if len(r.Observations) != 3 {
		t.Fatalf("want initial and two steps: %+v", r)
	}
	if b, err := os.ReadFile(started); err != nil || string(b) != "watchdog 2\nwatchdog 2\n" {
		t.Fatalf("the stand-in did not start each step's watchdog late: %q, %v", b, err)
	}
}

// A step its deadline stops ends the runner before it seals its stream,
// whether the step expires itself or the watchdog kills it. The failure
// names the stop, not the collector's authentication (#645).
func TestPythonApplicationModuleTimeoutReportsTheStop(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{
		"expired": strings.Replace(pa321State, "time.sleep(0.03)", "time.sleep(30)", 1) + pa321Worker,
		// The busy loop holds the GIL, so only the watchdog can stop it.
		"killed": pa321State + "def advance(key, status):\n    finish(key, status)\ndef submit(request, *, status):\n    sum(range(2000000000))\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, p := pa321Fixture(t, source, strings.Replace(pa321Driver, "timeout=2", "timeout=0.5", 1))
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			ctx := pa321Context(t)
			r, err := s.Run(ctx, pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if ctx.Err() != nil {
				t.Fatal("the step's deadline did not stop the run before the test's own")
			}
			if err == nil || strings.Contains(err.Error(), "authentication") || !strings.Contains(err.Error(), "stopped before sealing its observations") {
				t.Fatalf("the stop was reported as %v", err)
			}
			if !strings.Contains(strings.Join(r.Diagnostics, "\n"), "module observation timed out") {
				t.Fatalf("the diagnostics don't name the timeout: %q", r.Diagnostics)
			}
		})
	}
}
