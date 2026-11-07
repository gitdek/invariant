package plumbing

import (
	"strings"
	"testing"
)

// Reviews listed the pinned-TLC checks and the gate's tests against the real
// verifiers as merger work, though CI's integration shards run them on every
// plumbing pull request and the gate requires them (#729).
func TestReviewPromptLeavesIntegrationTestsToCI(t *testing.T) {
	got := strings.Join(strings.Fields(ReviewPrompt(&Plan{Name: "a review", Summary: "Change a check.", Files: []string{"internal/formalize/reader.go"}}, 729)), " ")
	for _, want := range []string{
		"the integration-tagged tests, including the gate against the real verifiers and the pinned-TLC checks, on every plumbing pull request",
		"The gate requires them all, so don't list those.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the built-in review prompt lacks %q", want)
		}
	}
}
