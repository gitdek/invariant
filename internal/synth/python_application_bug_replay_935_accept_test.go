package synth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

type pa935Reviewer struct {
	t        *testing.T
	original string
	called   *bool
}

func (pa935Reviewer) Name() string { return "fixture reviewer" }
func (r pa935Reviewer) Run(_ context.Context, job Job) (Usage, error) {
	r.t.Helper()
	*r.called = true
	if !job.ReadOnly || job.Workspace == r.original {
		r.t.Fatal("replay review lost isolated read-only authority")
	}
	// Keep application-relative paths so the reviewer can follow actual imports.
	for name, want := range map[string]string{"tasks/lifecycle.py": "def advance(state):\n    return state\n", "tasks/application.py": "from .lifecycle import advance\ndef tick(state):\n    return advance(state)\n"} {
		b, err := os.ReadFile(filepath.Join(job.Workspace, name))
		if err != nil || string(b) != want {
			r.t.Fatalf("replay review lost application entry %s: %s %v", name, b, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(job.Workspace, "invariant_observe.py"))
	if err != nil || !strings.Contains(string(b), "def replay(") || strings.Contains(string(b), "untrusted fixture collector") {
		r.t.Fatalf("review lacks fresh collector interface: %v", err)
	}
	for _, want := range []string{"ReplayCompletedTask", "invariant_observe", "selected", "read-back"} {
		if !strings.Contains(job.Prompt, want) {
			r.t.Errorf("review omits %q", want)
		}
	}
	return Usage{Outcome: "success", Summary: "Read the actual replay bindings."}, nil
}

func TestPythonApplicationReplayGuidance935(t *testing.T) {
	root := t.TempDir()
	p := &project.Project{Dir: filepath.Join(root, "verification", "delivery"), Manifest: project.Manifest{Name: "delivery", Language: "python", Code: ".", Module: ".invariant/specs/Delivery.tla", Conformance: "driver.py", Existing: []string{"tasks/lifecycle.py"}}, Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}, {Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"}}}}
	for _, text := range []string{Prompt(p, "", "", 4, true, false), reviewPrompt(p)} {
		for _, want := range []string{"invariant_observe", "replay(start, steps)", "Refusal", "authenticated", "selected", "ReplayCompletedTask"} {
			if !strings.Contains(text, want) {
				t.Errorf("existing Python guidance lacks %q: %s", want, text)
			}
		}
		if strings.Contains(text, "replay-unavailable") || strings.Contains(text, "Export invariant_replay(initial)") || strings.Contains(text, "Inspect invariant_replay(initial)") {
			t.Errorf("existing Python is sent to unavailable or unauthenticated command adapter: %s", text)
		}
	}
	// D-0483 applies to the existing-Python collector too: the driver shares
	// its process, so review is the check against tampering with its internals.
	writer := strings.ToLower(Prompt(p, "", "", 4, true, false))
	for _, want := range []string{"the harness's own process", "never reach into invariant_observe", "frames", "objects", "closures", "builtins", "modules", "descriptors", "output files", "the process's exit", "tampering with the gate"} {
		if !strings.Contains(writer, want) {
			t.Errorf("existing Python writer is not told %q", want)
		}
	}
	reviewer := strings.ToLower(reviewPrompt(p))
	for _, want := range []string{"report as a problem any driver code that reaches into invariant_observe", "your review is the check against it"} {
		if !strings.Contains(reviewer, want) {
			t.Errorf("existing Python reviewer is not told %q", want)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	for name, body := range map[string]string{
		"tasks/__init__.py": "", "tasks/lifecycle.py": "def advance(state):\n    return state\n",
		"tasks/application.py":                                "from .lifecycle import advance\ndef tick(state):\n    return advance(state)\n",
		"verification/delivery/driver.py":                     "from invariant_observe import replay\nfrom tasks import application\ndef drive():\n    pass\n",
		"verification/delivery/invariant_observe.py":          "untrusted fixture collector\n",
		"verification/delivery/.invariant/specs/Delivery.tla": "---- MODULE Delivery ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == UNCHANGED n\nSpec == Init /\\ [][Next]_n\nSafe == n <= Limit\nReplayCompletedTask == n' = Limit + 1\n====\n",
	} {
		if err := writeFile(filepath.Join(root, name), body); err != nil {
			t.Fatal(err)
		}
	}
	for name, v := range map[string]any{".invariant/invariant.json": p.Manifest, ".invariant/ratified.lock": p.Lock} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(p.Dir, name), string(b)); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	if err := Prepare(p, "---- MODULE Delivery ----\n====\n", ws); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(ws, "invariant_observe.py"))
	if err != nil || !strings.Contains(string(b), "def replay(") {
		t.Fatalf("writer cannot read actual replay interface: %v", err)
	}
	if owned(p.Manifest, "invariant_observe.py") || owned(p.Manifest, "helpers/invariant_observe.py") {
		t.Fatal("agent's collector copy can ship as application code")
	}
	called := false
	if _, err := ReviewDriver(context.Background(), pa935Reviewer{t, root, &called}, p.Dir, filepath.Join(t.TempDir(), "review.jsonl")); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("driver review was omitted")
	}
	b, err = os.ReadFile(filepath.Join(p.Dir, "invariant_observe.py"))
	if err != nil || string(b) != "untrusted fixture collector\n" {
		t.Fatal("preparing review changed original source")
	}
}
