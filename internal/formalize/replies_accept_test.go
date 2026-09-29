package formalize

import (
	"strings"
	"testing"
)

// replyPhrases is what a prompt tells an agent that writes files about its
// replies (#120): a reply holds about 128,000 tokens, thinking included, and
// one that runs out of room loses what it didn't write, so the agent writes
// each file as soon as it's decided, which costs none of its checks.
var replyPhrases = []string{
	"128,000 tokens of output, thinking included",
	"reply that runs out of room loses what it didn't write",
	"each file as soon as you've decided what it holds",
	"files early costs none of your",
}

// The formalization agent writes the module and proposal.json. Its prompt
// says how its replies work, and still gives it its checks.
func TestFormalizationTellsTheAgentToWriteAsItGoes(t *testing.T) {
	got := Prompt(Request{Repo: "gitdek/invariant", Issue: 7, Title: "Add a bounded buffer", Body: "Producers put lines in a buffer, and a shipper takes them out.", Author: "gitdek"}, 3)
	for _, want := range append([]string{"You have 3 checks"}, replyPhrases...) {
		if !strings.Contains(got, want) {
			t.Errorf("the formalization prompt lacks %q", want)
		}
	}
}

// A plumbing issue's planning agent writes plan.json and its acceptance
// tests. Its prompt says the same, and still gives it its checks.
func TestPlanningTellsTheAgentToWriteAsItGoes(t *testing.T) {
	got := PlanPrompt(Request{Repo: "gitdek/invariant", Issue: 120, Title: "Tell the factory's agents to write as they go", Author: "gitdek"}, 4)
	for _, want := range append([]string{"You have 4 checks"}, replyPhrases...) {
		if !strings.Contains(got, want) {
			t.Errorf("the planning prompt lacks %q", want)
		}
	}
}
