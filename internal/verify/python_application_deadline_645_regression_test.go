package verify

import (
	"os"
	"os/exec"
	"path/filepath"
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
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	// exec -a keeps this script as sys.executable, which starts the
	// watchdog. Only the watchdog's program is late; the gate's own Python
	// checks find this script on PATH too.
	script := "#!/bin/bash\ncase \"$1 $2 $3 $4\" in\n'-I -S -c import os, select, signal, sys, time'*)\n  echo \"watchdog $6\" >> " + quote(started) +
		"\n  sleep 2.5;;\nesac\nexec -a \"$0\" " + quote(python) + " \"$@\"\n"
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
