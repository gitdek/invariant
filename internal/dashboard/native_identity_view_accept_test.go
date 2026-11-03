package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
)

func TestNativeProposalViewRetainsFullIdentityAndSource(t *testing.T) {
	var p plumbing.Plan
	if err := json.Unmarshal([]byte(`{"name":"native view","summary":"Show the test being ratified.","files":["web/main.ts"],"tests":[{"name":"<img src=x>","part":"web","framework":"jest","file":"web/ready.test.ts","selector":"outer exact title [case one]","says":"It is ready."}],"sources":{"web/ready.test.ts":"it('exact title [case one]', () => {});\n"}}`), &p); err != nil {
		t.Fatal(err)
	}
	view := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: p.Name}, Plan: &p, Hash: p.Hash()}}, time.Now())
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	// JSON remains data for the page's existing text escaping. The full
	// tuple and source must survive this projection, not just Name/Says.
	var object any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	decoded, _ := json.Marshal(object)
	for _, want := range []string{"web", "jest", "web/ready.test.ts", "outer exact title [case one]", "it('exact title [case one]'"} {
		if !strings.Contains(string(decoded), want) {
			t.Fatalf("proposal view lost %q: %s", want, decoded)
		}
	}
}
