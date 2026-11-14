package synth

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// A replay's driver runs in the harness's own process, so it can reach the
// harness's seal and records, and nothing the gate checks can tell a replay
// it forges from inside from a real one. The writer is told never to, and
// the reviewer, the only check against it, is told to report any of it.
func TestReplayGuidanceNamesTheHarnessTrustBoundary913(t *testing.T) {
	for _, route := range []string{"typescript", "existing", "python"} {
		t.Run(route, func(t *testing.T) {
			language, driver := "typescript", "driver.ts"
			if route == "python" {
				language, driver = "python", "driver.py"
			}
			p := &project.Project{Dir: t.TempDir(), Manifest: project.Manifest{Name: "delivery", Language: language, Module: ".invariant/specs/Delivery.tla", Code: "core", Conformance: driver},
				Lock: project.Lock{Statements: []project.Statement{{Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"}}}}
			if route == "existing" {
				p.Manifest.Existing = []string{"src/delivery.ts"}
			}
			prompt := strings.ToLower(Prompt(p, "", "", 4, true, false))
			for _, want := range []string{"the harness's own process", "never reach into the harness", "frames", "descriptors", "the process's exit", "tampering with the gate"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("synthesis doesn't tell the writer %q", want)
				}
			}
			review := reviewPrompt(p)
			for _, want := range []string{"Report as a problem any driver code that reaches into the replay harness", "your review is the check against it"} {
				if !strings.Contains(review, want) {
					t.Errorf("review doesn't ask for %q", want)
				}
			}
		})
	}
	// Existing Python runs no command replay, so it has no harness to name.
	p := &project.Project{Manifest: project.Manifest{Language: "python", Existing: []string{"delivery.py"}, Conformance: "driver.py"},
		Lock: project.Lock{Statements: []project.Statement{{Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"}}}}
	if strings.Contains(strings.ToLower(Prompt(p, "", "", 4, true, false)), "never reach into the harness") {
		t.Error("existing Python is told about a replay harness it doesn't run")
	}
}
