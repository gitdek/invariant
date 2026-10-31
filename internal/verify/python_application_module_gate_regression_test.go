package verify_test

import (
	"html"
	"os"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
)

// Check the rendered literal rather than its HTML entity spelling. Also
// exercise the later gate assertions even when a raw-Markdown check fails.
func TestPythonApplicationModuleGateReadback(t *testing.T) {
	tc, log := pa327Fake(t)
	for _, explicit := range []bool{true, false} {
		t.Run(map[bool]string{true: "explicit", false: "omitted"}[explicit], func(t *testing.T) {
			f := pa321GateFixture(t, explicit)
			r := pa327Run(t, f, tc)
			pa327Assert(t, f, r, true)
			if !strings.Contains(html.UnescapeString(receipt.Markdown(r)), "tasks[*].status") {
				t.Fatal("rendered receipt lost read-back path")
			}
			second := pa327Run(t, f, tc)
			if !second.Passed || r.Fingerprint != second.Fingerprint {
				t.Fatal("module gate is not repeatable")
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			// The transport log is JSON, including the generated TLC module.
			if !strings.Contains(string(b), `<<\"done\", \"done\">>`) {
				t.Fatal("TLC did not receive an ordered sequence")
			}
			pa327Write(t, f.root, pa327DriverPath, strings.ReplaceAll(pa321GateDriver, "done", "broken"))
			bad := pa327Run(t, f, tc)
			pa327Assert(t, f, bad, false)
			app := pa327Application(t, bad)
			unit, driver := false, false
			for _, execution := range app.Executions {
				if execution.Key.Kind == "test" && execution.Success {
					unit = true
				}
				if execution.Key.Kind == "conformance" && execution.Success && len(execution.Observations) == 3 {
					driver = true
					if string(pa327JSON(t, execution.Observations[2].After["status"])) != `["broken","broken"]` {
						t.Fatal("raw receipt lists changed")
					}
				}
			}
			if !unit || !driver || len(app.Conformance) != 1 || bad.Fingerprint == r.Fingerprint {
				t.Fatal("TLC failure lost independent evidence or fingerprint binding")
			}
		})
	}
}
