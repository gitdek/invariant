package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestSharedModelKeepsEachProjectsBugReplayEvidence(t *testing.T) {
	graph := Graph{Bugs: []BugPath{{Name: "Overflow", Path: []int{0, 1}, Action: "Put", Escape: "n = 3"}}}
	b, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"code_behavior", "replay_status"} {
		if strings.Contains(string(b), field) {
			t.Fatalf("shared model graph carries implementation evidence: %s", b)
		}
	}
	old := verify.Report{Bugs: []verify.Bug{{Name: "Overflow", Caught: true}}}
	current := verify.Report{Bugs: []verify.Bug{{Name: "Overflow", Caught: true, CodeBehavior: "current", Replay: &verify.BugReplayEvidence{Required: true, Status: "replay-unavailable"}}}}
	a := projectOf(projectSource{key: "same-model", manifest: project.Manifest{Language: "go"}}, &old)
	c := projectOf(projectSource{key: "same-model", manifest: project.Manifest{Language: "typescript"}}, &current)
	if a.Model != c.Model || a.Bugs[0].ReplayStatus.Label != "not replayed: not classified" || !c.Bugs[0].ReplayStatus.Required || c.Bugs[0].ReplayStatus.Replayed {
		t.Fatalf("shared graph conflated project evidence: %+v %+v", a.Bugs, c.Bugs)
	}
	current.Bugs[0].CodeBehavior, current.Bugs[0].Replay = "hypothetical", nil
	c = projectOf(projectSource{key: "same-model"}, &current)
	if c.Model != a.Model || c.Bugs[0].ReplayStatus.Required || c.Bugs[0].ReplayStatus.Label != "not replayed: hypothetical" {
		t.Fatalf("fresh classification requires a new model graph: %+v", c)
	}
}
