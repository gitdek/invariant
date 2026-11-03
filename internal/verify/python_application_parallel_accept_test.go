package verify

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Exercise existing assertions, including their fixtures and cleanup, in the
// actual test binary. Source-level checks for t.Parallel would miss a call that
// is unreachable, placed after the expensive work, or hidden behind a helper.
func TestPythonApplicationIndependentCasesOverlapAndKeepResults(t *testing.T) {
	pa315RunnerPython(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "INVARIANT_") && strings.HasSuffix(key, "_REAL") {
			continue
		}
		env = append(env, entry)
	}

	groups := []struct {
		name     string
		path     string
		children []string
	}{
		{"proof refusals", "TestPythonApplicationProofRefusesTheReviewedHoles", []string{"the_function_rebound", "the_function_rebound_conditionally"}},
		{"module refusals", "TestPythonApplicationExecutionModuleRefusals/local", []string{"wrapper_with_wraps", "foreign_frame_globals"}},
		{"contract bypasses", "TestPythonApplicationContractTestsRefuseTheSecondReviewsBypasses", []string{"setUp's_assertion", "tearDown's_assertion"}},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			parts := strings.Split(group.path, "/")
			for i := range parts {
				parts[i] = "^" + regexp.QuoteMeta(parts[i]) + "$"
			}
			children := make([]string, len(group.children))
			wanted := make(map[string]bool, len(group.children))
			for i, child := range group.children {
				children[i] = regexp.QuoteMeta(child)
				wanted[group.path+"/"+child] = true
			}
			selection := strings.Join(parts, "/") + "/^(" + strings.Join(children, "|") + ")$"
			output := pa435RunTests(t, executable, env, selection)
			events := pa435TestEvents(output)
			seen := map[string][]string{}
			active := map[string]bool{}
			overlapped := false
			for _, event := range events {
				if !wanted[event.name] {
					continue
				}
				seen[event.name] = append(seen[event.name], event.action)
				switch event.action {
				case "cont":
					active[event.name] = true
					overlapped = overlapped || len(active) == len(wanted)
				case "pass", "fail", "skip":
					delete(active, event.name)
				}
			}
			for name := range wanted {
				if !reflect.DeepEqual(seen[name], []string{"run", "pause", "cont", "pass"}) {
					t.Errorf("%s must run in parallel and keep its passing result; events: %v", name, seen[name])
				}
			}
			if !overlapped {
				t.Error("the two existing sibling cases never ran concurrently")
			}
			if t.Failed() {
				t.Logf("child test output:\n%s", output)
			}
		})
	}

	// These include a helper that changes PATH, a direct Setenv, and a Chdir.
	// They must still execute their original assertions without becoming parallel.
	t.Run("process state stays serial", func(t *testing.T) {
		roots := []string{
			"TestPythonApplicationProofTheGateNeedsConformanceOnTheFunction",
			"TestPythonApplicationExecutionRealRunnerStartup",
			"TestPythonApplicationRelativeInvocationValidation",
		}
		selection := "^(" + strings.Join(roots, "|") + ")$/^application_root$"
		output := pa435RunTests(t, executable, env, selection)
		seen := map[string][]string{}
		for _, event := range pa435TestEvents(output) {
			seen[event.name] = append(seen[event.name], event.action)
			if event.action == "pause" || event.action == "cont" {
				t.Errorf("process-state test became parallel: %s %s", event.action, event.name)
			}
		}
		for _, name := range []string{roots[0], roots[1], roots[2] + "/application_root"} {
			if !reflect.DeepEqual(seen[name], []string{"run", "pass"}) {
				t.Errorf("%s must pass serially; events: %v", name, seen[name])
			}
		}
		if t.Failed() {
			t.Logf("child test output:\n%s", output)
		}
	})
}

func pa435RunTests(t *testing.T, executable string, env []string, selection string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	// test2json's verbose mode flushes each subtest's completion immediately;
	// ordinary -test.v can defer PASS lines until the parent finishes.
	cmd := exec.CommandContext(ctx, executable, "-test.v=test2json", "-test.run="+selection,
		"-test.count=1", "-test.parallel=2", "-test.shuffle=on", "-test.timeout=2m")
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("selected existing tests failed: %v\n%s", err, output)
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
		// The test2json mode prefixes test protocol lines with an ASCII SYN.
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
