package plumbing

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

// The build's agent writes the plan's files. Its prompt says how its replies
// work, and still gives it its test runs. The reviewer writes no file, so
// its prompt doesn't tell it to write each one.
func TestBuildTellsTheAgentToWriteAsItGoes(t *testing.T) {
	p := &Plan{
		Name:    "the synthesis prompt",
		Summary: "Change the synthesis prompt.",
		Files:   []string{"internal/synth/prompt.go"},
		Tests:   []Test{{Name: "TestSynthesisPrompt", File: "internal/synth/prompt_accept_test.go", Says: "The prompt changed."}},
	}
	got := BuildPrompt(p, 120, 5)
	for _, want := range append([]string{"You have 5 runs"}, replyPhrases...) {
		if !strings.Contains(got, want) {
			t.Errorf("the build prompt lacks %q", want)
		}
	}
	if strings.Contains(ReviewPrompt(p, 120), "as soon as you've decided") {
		t.Error("the reviewer writes no file, but its prompt tells it to write each one as soon as it's decided")
	}
}
