package verify_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/verify/internal/pythongatetest"
)

func TestPythonApplicationSerialGateSuitesMoveWithoutLosingCoverage(t *testing.T) {
	if os.Getenv("INVARIANT_ISSUE435_SHARED_GATE_CHILD") == "1" {
		// Exercise the new shared suite API itself, with the normal testing
		// selector choosing the original missing/malformed transport cases.
		t.Run("shared_failure_cases", func(t *testing.T) {
			pythongatetest.GateFailures(t, false)
		})
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "INVARIANT_") && strings.HasSuffix(key, "_REAL") {
			continue
		}
		env = append(env, entry)
	}

	t.Run("ordinary root package delegates", func(t *testing.T) {
		// Select no nested case, so the old implementation fails this check
		// promptly instead of re-running minutes of known serial work.
		selection := "^TestPythonApplicationGate(Failures|Fingerprints)$/^issue435_selection_probe$"
		output := pa435RunTests(t, executable, env, selection)
		seen := map[string]string{}
		for _, event := range pa435TestEvents(output) {
			seen[event.name] = event.action
		}
		for _, name := range []string{"TestPythonApplicationGateFailures", "TestPythonApplicationGateFingerprints"} {
			if seen[name] != "skip" {
				t.Errorf("%s still belongs to the ordinary root run (last event %q); its local cases must run in pythongate instead", name, seen[name])
			}
		}
	})

	// These are existing assertions, not replacement smoke tests: a real
	// invalid transition must be refused, and changed helper source must
	// change the fingerprint while identical relocated inputs do not.
	selection := "^TestPythonApplicationGate(Failures|Fingerprints)$/^local_Python$/^(explicit=true|true)$/^(invalid_transition|tasks)$/^helper\\.py$"
	want := []string{
		"TestPythonApplicationGateFailures/local_Python/explicit=true/invalid_transition",
		"TestPythonApplicationGateFingerprints/local_Python/true/tasks/helper.py",
	}
	assertSerial := func(t *testing.T, events []pa435TestEvent, output string, want []string) {
		t.Helper()
		passed := map[string]bool{}
		for _, event := range events {
			switch event.action {
			case "pause", "cont":
				t.Errorf("environment-changing gate suite became parallel: %s %s", event.action, event.name)
			case "skip", "fail":
				t.Errorf("gate coverage did not pass: %s %s", event.action, event.name)
			case "pass":
				passed[event.name] = true
			}
		}
		for _, name := range want {
			if !passed[name] {
				t.Errorf("missing passing original gate case %s", name)
			}
		}
		if t.Failed() {
			t.Logf("child test output:\n%s", output)
		}
	}

	t.Run("separate package owns local cases", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		// Standard go test executes in the package directory. This command
		// tests only the new sibling package, so it cannot recurse into us.
		cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-parallel=2", "-shuffle=on", "-timeout=2m", "-run="+selection, "./pythongate")
		cmd.Env = env
		cmd.WaitDelay = 5 * time.Second
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the separate local gate package must build and pass: %v\n%s", err, output)
		}
		var events []pa435TestEvent
		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var event struct {
				Action string
				Test   string
			}
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				t.Fatalf("invalid go test event: %v: %s", err, scanner.Text())
			}
			if event.Test != "" && event.Action != "output" {
				events = append(events, pa435TestEvent{event.Action, event.Test})
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		assertSerial(t, events, string(output), want)
	})

	t.Run("existing real suite keeps local checks", func(t *testing.T) {
		// The original entry points still run their local branch when the
		// real suite is enabled. The selector excludes its Docker branch;
		// CI runs the full real branch with its existing prerequisites.
		realEnv := append(append([]string(nil), env...), "INVARIANT_PYTHON_APPLICATION_GATE_REAL=1")
		output := pa435RunTests(t, executable, realEnv, selection)
		assertSerial(t, pa435TestEvents(output), output, want)
	})

	t.Run("shared suite refuses corrupted transport", func(t *testing.T) {
		childEnv := append(append([]string(nil), env...), "INVARIANT_ISSUE435_SHARED_GATE_CHILD=1")
		selection := "^TestPythonApplicationSerialGateSuitesMoveWithoutLosingCoverage$/^shared_failure_cases$/^local_Python$/^(missing|malformed)$"
		output := pa435RunTests(t, executable, childEnv, selection)
		prefix := "TestPythonApplicationSerialGateSuitesMoveWithoutLosingCoverage/shared_failure_cases/local_Python/"
		assertSerial(t, pa435TestEvents(output), output, []string{prefix + "missing", prefix + "malformed"})
	})
}

func pa435RunTests(t *testing.T, executable string, env []string, selection string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.v=test2json", "-test.run="+selection,
		"-test.count=1", "-test.parallel=2", "-test.shuffle=on", "-test.timeout=2m")
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("selected gate tests failed: %v\n%s", err, output)
	}
	return string(output)
}

type pa435TestEvent struct {
	action string
	name   string
}

func pa435TestEvents(output string) []pa435TestEvent {
	var events []pa435TestEvent
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimPrefix(line, "\x16")
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		var action string
		switch fields[0] + " " + fields[1] {
		case "=== RUN":
			action = "run"
		case "=== PAUSE":
			action = "pause"
		case "=== CONT":
			action = "cont"
		case "--- PASS:":
			action = "pass"
		case "--- FAIL:":
			action = "fail"
		case "--- SKIP:":
			action = "skip"
		default:
			continue
		}
		events = append(events, pa435TestEvent{action, fields[2]})
	}
	return events
}
