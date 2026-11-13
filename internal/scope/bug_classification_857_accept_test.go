package scope

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func TestScopeRejectsReclassificationUnderOldRatification857(t *testing.T) {
	for _, replacement := range []string{"hypothetical", ""} {
		dir := repo(t)
		var old project.Lock
		if err := json.Unmarshal([]byte(`{"bounds":{"Cap":"2"},"statements":[{"name":"Overflow","kind":"bug","says":"Overfills.","expect":"Safe","sha256":"pin","code_behavior":"current"}]}`), &old); err != nil {
			t.Fatal(err)
		}
		if old.Statements[0].CodeBehavior != "current" {
			t.Fatal("ratification lost its current-code classification")
		}
		// The original base can remain legacy; the amendment adds these rules.
		base := project.Lock{Decision: "D-0001"}
		old.Ratified = &project.Ratification{By: "gitdek", Issue: 7, Comment: "https://example.com/c", Amends: project.ProposalHash(base.Bounds, base.Statements), Proposal: project.ProposalHash(old.Bounds, old.Statements)}
		b, _ := json.Marshal(old)
		var raw map[string]any
		if err := json.Unmarshal(b, &raw); err != nil {
			t.Fatal(err)
		}
		s := raw["statements"].([]any)[0].(map[string]any)
		delete(s, "code_behavior")
		if replacement != "" {
			s["code_behavior"] = replacement
		}
		b, _ = json.Marshal(raw)
		write(t, dir, map[string]string{"examples/01-old/.invariant/ratified.lock": string(b)})
		commit(t, dir, "classification changed after ratification")
		r, err := Check(context.Background(), dir, "main", "HEAD", 7)
		if err != nil {
			t.Fatal(err)
		}
		if r.OK() || !strings.Contains(strings.Join(r.Problems, " "), "isn't the proposal that was ratified") {
			t.Errorf("classification %q escapes scope ratification: %+v", replacement, r)
		}
	}
}
