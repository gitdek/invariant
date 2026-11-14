package factory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
)

func TestFactoryReplayAvailabilityMatchesAdapters913(t *testing.T) {
	statements := []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Delivery recovers."},
		{Name: "Safe", Kind: project.Invariant, Says: "No duplicate delivery."},
		{Name: "ReplayCompletedTask", Kind: project.Bug, Says: "A completed task is delivered twice.", Expect: "Safe", CodeBehavior: "current"},
	}
	for _, c := range []struct {
		language string
		existing []string
		blocked  bool
	}{
		{"typescript", nil, false},
		{"typescript", []string{"src/delivery.ts"}, false},
		{"python", nil, false},
		{"python", []string{"delivery.py"}, true},
		{"go", nil, false},
	} {
		for _, amendment := range []bool{false, true} {
			// New Code: proposals currently select TypeScript. Existing Python
			// is represented by its already captured target manifest.
			if c.language == "python" && len(c.existing) > 0 && !amendment {
				continue
			}
			p := &formalize.Proposal{Draft: formalize.Draft{Name: "delivery", Language: c.language, Existing: c.existing, Statements: statements}}
			if amendment {
				p.Target = &formalize.Target{Manifest: project.Manifest{Language: c.language, Existing: c.existing}, Dir: "verification/delivery", Previous: "#789"}
			}
			var want []string
			if c.blocked {
				want = []string{"ReplayCompletedTask"}
			}
			if got := Unreplayed(p); !reflect.DeepEqual(got, want) {
				t.Errorf("language=%s existing=%v amendment=%v: unavailable=%v want=%v", c.language, c.existing, amendment, got, want)
			}
			if message := unbuildable(p); c.blocked && (!strings.Contains(message, "ReplayCompletedTask") || !strings.Contains(message, "replay-unavailable")) || !c.blocked && message != "" {
				t.Errorf("availability notice does not describe this adapter: %s", message)
			}
		}
	}
	// An implemented finite replay adapter must not promise it can execute an
	// infinite counterexample; neither classification nor pins are rewritten.
	p := &formalize.Proposal{Draft: formalize.Draft{Name: "delivery", Language: "typescript", Statements: append(append([]project.Statement(nil), statements...),
		project.Statement{Name: "EventuallyDone", Kind: project.Property, Says: "Delivery finishes."},
		project.Statement{Name: "NeverFinishes", Kind: project.Bug, Says: "Delivery never finishes.", Expect: "EventuallyDone", CodeBehavior: "current"})}}
	if got := Unreplayed(p); !reflect.DeepEqual(got, []string{"NeverFinishes"}) {
		t.Errorf("temporal replay availability: %v", got)
	}
	if message := unbuildable(p); !strings.Contains(message, "NeverFinishes") || !strings.Contains(message, "counterexample") || strings.Contains(message, "Ratify it once replays run") {
		t.Errorf("temporal limitation hidden or falsely temporary: %s", message)
	}
}
