package factory

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// A plan's pull request quoted its build's summary whole, and #399's had
// 689,793 characters: GitHub refused the body, and the factory tried again
// on every poll (#408). The body quotes at most summaryLimit bytes of it.
func TestAPlanPullRequestFitsGitHubWhateverItsSummary(t *testing.T) {
	summary := "Everything passed: gofmt, go vet, the tests of every package the change touches, and every acceptance test.\n" +
		strings.Repeat("test example.com/big/TestSomething: passed (shard 1)\n", 14000)
	res := &plumbing.BuildResult{Passed: true, Approved: true, Summary: summary, Review: strings.Repeat("A finding.\n", 2000) + "VERDICT: approve"}
	m := Marker{Project: ".invariant/plans/issue-399.json", Hash: "sha256:4585e6938914"}
	body := planPullRequestBody(Thread{Issue: github.Issue{Number: 399}}, m, res, []string{"internal/verify/python_application_execution.go"})
	if len(body) > maxComment {
		t.Errorf("the pull request's body has %d characters, over %d", len(body), maxComment)
	}
	if !strings.Contains(body, "**The factory's own run:** Everything passed") || !strings.Contains(body, "the rest is in the build's log") {
		t.Errorf("the body doesn't keep the summary's start and say it's clipped:\n%.1500s", body)
	}
}
