//go:build integration

package verify_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// The integration shards discover this test without an environment opt-in.
// Both cases use real Docker Python and TLC, including the session's private
// runtime/output directories and the collector's private authentication file.
func TestPythonApplicationGatePrivateMounts(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatalf("real Docker and TLC prerequisites: %v", err)
	}
	for _, invalid := range []bool{false, true} {
		name := "honest application"
		if invalid {
			name = "invalid transition"
		}
		t.Run(name, func(t *testing.T) {
			f := pa327New(t, true, false)
			if invalid {
				// The selected unittest still passes pending -> running. The
				// driver completes normally but also observes running -> broken.
				pa327Write(t, f.root, "tasks/helper.py", "def next_state(state):\n    return 'running' if state == 'pending' else 'broken'\n")
			}
			before := pa327Tree(t, f.root)
			r, err := verify.Run(ctx, f.p.Dir, "", tc)
			if err != nil || r == nil {
				t.Fatalf("real application gate: report=%+v err=%v", r, err)
			}
			pa327Assert(t, f, r, !invalid)
			if !r.Build.Passed || !r.Design.Passed {
				t.Fatalf("Python execution and model checking must pass independently: %s", verify.Feedback(r))
			}
			a := r.Application
			if len(a.Executions) != 2 || len(a.Conformance) != 1 {
				t.Fatalf("want a test, a driver and its TLC result: %+v", a)
			}
			kinds := map[string]int{}
			var driver verify.PythonApplicationInvocationKey
			for _, execution := range a.Executions {
				kinds[execution.Key.Kind]++
				if !execution.Success || !execution.Complete || execution.ExitCode != 0 {
					t.Fatalf("Python must successfully read and write its private mounts: %+v", execution)
				}
				if execution.Provenance.Dependencies != nil {
					t.Fatalf("standard-library fixture acquired dependencies: %+v", execution.Provenance.Dependencies)
				}
				switch execution.Key.Kind {
				case "test":
					if len(execution.Tests) != 1 || !execution.Tests[0].Exercised || execution.Tests[0].Outcome != "passed" {
						t.Fatalf("selected unittest did not pass: %+v", execution.Tests)
					}
				case "conformance":
					driver = execution.Key
					if execution.ObservationMode != "sampled" || len(execution.Observations) != 4 {
						t.Fatalf("driver did not produce complete authenticated observations: %+v", execution)
					}
				}
			}
			if kinds["test"] != 1 || kinds["conformance"] != 1 {
				t.Fatalf("missing test or driver execution: %v", kinds)
			}
			c := a.Conformance[0]
			if c.Key != driver || c.OperationalError || c.Result.Passed != !invalid {
				t.Fatalf("TLC did not check the successful driver execution: %+v", c)
			}
			if invalid {
				bad := c.Result.BadStep
				if bad == nil || !strings.Contains(bad.From, `"running"`) || !strings.Contains(bad.To, `"broken"`) {
					t.Fatalf("want TLC's running -> broken BadStep: %+v", c.Result)
				}
				deadlock := false
				for _, check := range c.Checks {
					deadlock = deadlock || (check.Module == "Invariant_Conformance" && check.Error == "" && check.Result.Outcome == tlc.Deadlock)
				}
				if !deadlock {
					t.Fatalf("BadStep must come from real TLC's conformance counterexample: %+v", c.Checks)
				}
			}
			if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
				t.Fatal("gate modified the original application")
			}
		})
	}
}
