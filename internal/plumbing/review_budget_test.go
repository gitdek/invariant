package plumbing

import (
	"strings"
	"testing"
)

// Every plumbing review used to list the sandbox's real-image budget check
// under Merger work, though the check measures only the sandbox and what
// internal/verify costs to build and test (#725). The built-in prompt names
// the check with that condition, and a configured repository's never names it.
func TestReviewPromptListsTheBudgetCheckOnlyWhenItApplies(t *testing.T) {
	builtIn := ReviewPrompt(&Plan{Name: "a review", Summary: "Change a page.", Files: []string{"internal/dashboard/web/app.js"}}, 725)
	for _, want := range []string{
		"List the plumbing sandbox's real-image budget check only when the change can affect what it measures",
		"the sandbox's container, image, resource limits, deadlines or check runner",
		"the cost of building and testing internal/verify",
		"Don't list it for any other change.",
	} {
		if !strings.Contains(builtIn, want) {
			t.Errorf("the built-in review prompt lacks %q", want)
		}
	}
	if strings.Contains(builtIn, "budget check is still the merger's") {
		t.Error("the built-in review prompt still makes the budget check every merger's")
	}
	configured := ReviewPrompt(&Plan{Name: "a review", Summary: "Change a widget.", Files: []string{"web/widget.ts"},
		Tests: []Test{{Name: "widget case", Part: "web", Framework: "vitest", File: "web/widget.test.ts", Selector: "widget case", Says: "Widget values change."}}}, 725)
	if strings.Contains(configured, "budget check") {
		t.Error("a configured repository's review prompt names Invariant's budget check")
	}
}
