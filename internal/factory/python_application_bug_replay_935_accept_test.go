package factory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
)

func TestPythonApplicationReplayAvailability935(t *testing.T) {
	p := &formalize.Proposal{Draft: formalize.Draft{Name: "delivery", Language: "python", Statements: []project.Statement{
		{Name: "Safe", Kind: project.Invariant}, {Name: "Drains", Kind: project.Property},
		{Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"},
		{Name: "NeverCompletes", Kind: project.Bug, Expect: "Drains", CodeBehavior: "current"},
	}}}
	p.Target = &formalize.Target{Manifest: project.Manifest{Language: "python", Existing: []string{"tasks/lifecycle.py"}}}
	if got := Unreplayed(p); !reflect.DeepEqual(got, []string{"NeverCompletes"}) {
		t.Fatalf("implemented Python safety replay still unavailable: %v", got)
	}
	note := unbuildable(p)
	if !strings.Contains(note, "NeverCompletes") || !strings.Contains(note, "replay-unsupported-temporal") || strings.Contains(note, "ReplayCompletedTask") || strings.Contains(note, "replay-unavailable") {
		t.Fatalf("proposal misstates the supported adapter: %s", note)
	}
	p.Statements = p.Statements[:3]
	if got := Unreplayed(p); len(got) != 0 || unbuildable(p) != "" {
		t.Fatalf("safety-only application incorrectly blocked: %v %s", got, unbuildable(p))
	}
	// A project can still fail at runtime on an unsupported binding. Admission
	// of this route promises the adapter, never a successful code verdict.
}
