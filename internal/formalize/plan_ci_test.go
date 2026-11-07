package formalize

import (
	"strings"
	"testing"
)

// Plans left the pinned-TLC checks to the merger whenever the builder lacked
// Docker, though CI's integration shards run them on every plumbing pull
// request (#729).
func TestPlanPromptLeavesIntegrationTestsToCI(t *testing.T) {
	got := strings.Join(strings.Fields(PlanPrompt(Request{Repo: "gitdek/invariant", Issue: 729, Title: "Change a check", Author: "gitdek"}, 4)), " ")
	for _, want := range []string{
		"the integration-tagged tests, including the gate against the real verifiers and the pinned-TLC checks, on every plumbing pull request",
		"so they aren't Merger work, and a plan needn't leave them to the merger",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the built-in plan prompt lacks %q", want)
		}
	}
}
