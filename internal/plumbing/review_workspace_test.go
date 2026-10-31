package plumbing

import (
	"strings"
	"testing"
)

// A reviewer that read its workspace's real path, under the temporary
// directory its profile denies, used to stop without reading a file, and the
// build spent a repair round on a review with no findings (#324). Its prompt
// says the workspace is readable, and that only a failed read is a problem.
func TestReviewPromptSaysTheWorkspaceIsReadable(t *testing.T) {
	p := &Plan{Name: "a review", Summary: "Change the review prompt.", Files: []string{"internal/plumbing/prompt.go"}}
	got := ReviewPrompt(p, 324)
	for _, want := range []string{
		"This directory is readable, although it sits in the temporary directory your sandbox otherwise denies",
		"its real path may begin with /private on macOS",
		"Read its files by the relative paths below",
		"Report an access problem only if reading one of them actually fails",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the review prompt lacks %q", want)
		}
	}
}
