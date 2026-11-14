package synth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func TestSynthesisAndReviewRequireRealReplayBindings913(t *testing.T) {
	// ReviewDriver also supplies the fresh replay harness and selected existing
	// application source in its read-only workspace, so the reviewer can trace
	// every binding into the real implementation; the usual existing-source
	// import/hash boundaries still apply. Add a regression of that staged job
	// alongside TestReviewDriver in synth_test.go, retaining ReadOnly and all
	// its current exploration and no-write assertions.
	for _, route := range []string{"typescript", "existing", "python"} {
		t.Run(route, func(t *testing.T) {
			language, driver, harness, factory := "typescript", "driver.ts", "invariant-replay.ts", "invariantReplay"
			if route == "python" {
				language, driver, harness, factory = "python", "driver.py", "invariant_replay.py", "invariant_replay"
			}
			p := &project.Project{Dir: t.TempDir(), Manifest: project.Manifest{Name: "delivery", Language: language, Module: ".invariant/specs/Delivery.tla", Code: "core", Conformance: driver},
				Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", Says: "A completed task is delivered twice.", CodeBehavior: "current"}}}}
			if route == "existing" {
				p.Manifest.Existing = []string{"src/delivery.ts"}
			}
			prompt := Prompt(p, "", "", 4, true, false)
			for _, want := range []string{factory, "bind", "read", "close", "ReplayCompletedTask", "replay"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("%s synthesis omits replay interface %q", route, want)
				}
			}
			// Existing D-0082/85 instructions remain. The replay additions explain
			// that binding a bug must call the real operation even if the model
			// rules it out; a fake refusal or state mutation is not evidence.
			for _, want := range []string{"real", "operation", "refus", "initial", "prefix"} {
				if !strings.Contains(strings.ToLower(prompt), want) {
					t.Errorf("synthesis lacks %q requirement", want)
				}
			}
			ws := t.TempDir()
			if err := Prepare(p, "---- MODULE Delivery ----\n====\n", ws); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(filepath.Join(ws, harness)); err != nil || len(b) == 0 {
				t.Errorf("replay harness not available to writer: %v", err)
			}
			if owned(p.Manifest, harness) {
				t.Error("the agent's copy of the gate replay harness may ship as project code")
			}
			// Read the review prompt on the same project. It must preserve the
			// review's read-only authority and the concrete calls/read-back checks.
			if err := os.WriteFile(filepath.Join(p.Dir, driver), []byte("# sample"), 0644); err != nil {
				t.Fatal(err)
			}
			review := reviewPrompt(p)
			for _, want := range []string{factory, "bind", "ReplayCompletedTask", "prefix", "read", "refus"} {
				if !strings.Contains(review, want) {
					t.Errorf("review omits %q: %s", want, review)
				}
			}
			if !strings.Contains(review, "change nothing") || !strings.Contains(review, "Claim nothing you didn't read") {
				t.Error("review lost read-only limits")
			}
		})
	}
	// Implemented adapters must never be advertised for existing Python.
	p := &project.Project{Manifest: project.Manifest{Language: "python", Existing: []string{"delivery.py"}, Conformance: "driver.py"}, Lock: project.Lock{Statements: []project.Statement{{Name: "ReplayCompletedTask", Kind: project.Bug, CodeBehavior: "current", Expect: "Safe"}}}}
	if message := strings.ToLower(Prompt(p, "", "", 4, true, false)); !strings.Contains(message, "replay-unavailable") {
		t.Error("existing Python limitation absent from synthesis instructions")
	}
}
