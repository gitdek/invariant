//go:build integration

package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// The actual pinned TLC extracts both recovery paths and checks their actual
// Python outcomes. Small identities fit TLC's integers; the runtime suite
// separately checks exact integers beyond JavaScript's number precision.
func TestPythonApplicationBugReplayPinned935(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	t.Cleanup(cancel)
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, explicit := range []bool{false, true} {
		for _, mode := range []string{"fixed", "replay", "reuse", "both", "alternative", "missing", "exception"} {
			t.Run(fmt.Sprintf("explicit=%v/%s", explicit, mode), func(t *testing.T) {
				t.Parallel()
				driver := pa935Driver
				if mode == "missing" {
					driver = strings.Replace(driver, "    if replay(start, steps + bugs):\n        return\n", "", 1)
				}
				root, p := pa935Fixture(t, explicit, mode, driver)
				for _, file := range []string{pa315Target, pa315Project + "/driver.py", "tests/test_tasks.py"} {
					b, err := os.ReadFile(filepath.Join(root, file))
					if err != nil {
						t.Fatal(err)
					}
					body := strings.NewReplacer("9007199254740993", "1", "9007199254740994", "2").Replace(string(b))
					if mode == "exception" && file == pa315Target {
						body = strings.Replace(body, "raise Refused(409)", "raise Refused(410)", 1)
					}
					pa310Write(t, root, file, body)
				}
				path := pa935Request(t, p, false).Path
				p.Lock.Bounds["Token"] = "1"
				pa935Rebind(t, p, path, pa935Model)
				r, err := Run(ctx, p.Dir, "", tc, Options{TLCParallel: 1})
				if err != nil || r == nil {
					t.Fatalf("real gate: %v", err)
				}
				if r.Application == nil || !r.Design.Passed || !r.Build.Passed || len(r.Application.Conformance) != 1 || !r.Application.Conformance[0].Result.Passed || len(r.Application.BugReplays) != 2 {
					t.Fatalf("lost independent real evidence: %s", Feedback(r))
				}
				if r.Passed != (mode == "fixed" || mode == "alternative") {
					t.Fatalf("wrong real gate verdict: %s", Feedback(r))
				}
				for _, row := range r.Application.BugReplays {
					e := row.Evidence
					if e.Request == nil || e.Request.Path.ModelSHA256 == "" || e.Request.Path.Tool.JarSHA256 == "" || e.Counterexample == nil {
						t.Fatalf("no extracted path: %+v", e)
					}
					broken := mode == "both" || mode == "replay" && row.Bug == "ReplayCompletedTask" || mode == "reuse" && row.Bug == "ReuseRecoveredName"
					failed := broken || mode == "missing" || mode == "exception"
					if e.Passed == failed || broken && e.Status != "reproduced" || !failed && e.Status != "refused" && e.Status != "complete" {
						t.Fatalf("wrong checked real replay: %+v", e)
					}
					if e.Passed && (e.Result == nil || e.Observation == nil || !e.Observation.Completed) {
						t.Fatal("real TLC accepted no execution")
					}
				}
				c := r.Application.Conformance[0].Result
				if c.Runs != 1 || c.Steps != 1 || c.States != 2 || c.Transitions != 1 || c.Exhaustive {
					t.Fatalf("replay inflated sampling: %+v", c)
				}
			})
		}
	}
	for _, mode := range []string{"cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			pa935RuntimeFailure(t, true, mode)
		})
	}
	t.Run("held cleanup", func(t *testing.T) {
		t.Parallel()
		pa935HeldCalls(t, true, true)
	})
}
