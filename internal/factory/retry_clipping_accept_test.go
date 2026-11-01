package factory

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// Trusted paths are detail, not instructions. A long list must be shortened
// without losing the commands or the consequences of retrying this draft:
// since #271, a retry closes it and builds the plan again, and a person
// merges what that build opens, since it changes the trusted base.
// Repetitive paths keep the marker small, so it is the visible list alone
// that needs clipping; neither the failure evidence nor the review is long.
func TestClippedTrustedDraftRetryKeepsClosingCommands(t *testing.T) {
	const sourceFile = "page/retry_accept_test.go"
	makeMarker := func(files []string) Marker {
		t.Helper()
		plan := &plumbing.Plan{
			Name:    "Repair the factory",
			Summary: "Repair the factory's handling of a failed build.",
			Files:   files,
			Tests: []plumbing.Test{{
				Name: "TestRepairedBuild", File: sourceFile,
				Says: "The repaired build handles the failure.",
			}},
			Sources: map[string]string{
				sourceFile: "package page\n\nimport \"testing\"\n\nfunc TestRepairedBuild(t *testing.T) { t.Fatal(\"fixture\") }\n",
			},
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("invalid trusted-plan fixture: %v", err)
		}
		proposal := &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name}, Plan: plan, Hash: plan.Hash()}
		return Marker{
			Kind: KindFailed, Failure: FailGate, PR: 263, Agent: "codex",
			Project: plumbing.LockPath(263), Hash: proposal.Hash,
			Branch: "invariant/issue-263", Proposal: proposal, ReplyTo: []int64{2630},
		}
	}
	pr := &github.PullRequest{Number: 263, Draft: true}
	result := &plumbing.BuildResult{
		Passed: true, Approved: false,
		Review: "Push the requested fixes before merging.\n\nVERDICT: changes needed",
	}
	checkGuidance := func(body string) {
		t.Helper()
		retry271RebuildWords(t, body, pr.Number, true)
		retry271ReviseWords(t, body, pr.Number)
		visible, _, _ := strings.Cut(body, "<!-- invariant:")
		for _, command := range []string{"`/invariant retry`", "`/invariant revise`"} {
			if strings.Count(visible, command) != 1 {
				t.Errorf("failure post must retain the complete closing command %s exactly once", command)
			}
		}
	}

	// In an ordinary post, details are still available to the person who
	// must merge. This companion stays in the same acceptance test because
	// its preservation assertions alone cannot establish the clipping fix.
	short := makeMarker([]string{"internal/factory/first.go", "internal/factory/second.go"})
	body := planFailedComment(pr, result, nil, short)
	closingCommandsCheckPost(t, body, short, false)
	checkGuidance(body)
	visible, _, _ := strings.Cut(body, "<!-- invariant:")
	if strings.Contains(visible, closingCommandsOmission) {
		t.Error("a short post should not omit trusted-file details")
	}
	for _, path := range short.Proposal.Plan.Trusted {
		if strings.Count(visible, path) != 1 {
			t.Errorf("short failure post must list trusted path %q exactly once", path)
		}
	}

	// A list too large even for GitHub's hard limit catches the regression
	// where all its paths were put into the unshortened closing guidance.
	var files []string
	for i := 0; i < 400; i++ {
		files = append(files, fmt.Sprintf("internal/factory/%03d/%srepair.go", i, strings.Repeat("long_trusted_directory/", 16)))
	}
	long := makeMarker(files)
	trusted := long.Proposal.Plan.Trusted
	if len(codeList(trusted)) <= 2*maxComment || len(long.encode()) >= maxComment/4 {
		t.Fatal("fixture must overflow the visible comment with trusted paths alone, while leaving ample room beside its marker")
	}
	body = planFailedComment(pr, result, nil, long)
	closingCommandsCheckPost(t, body, long, true)
	checkGuidance(body)
	visible, _, _ = strings.Cut(body, "<!-- invariant:")
	if strings.Contains(visible, codeList(trusted)) || strings.Contains(visible, trusted[len(trusted)-1]) {
		t.Error("oversized trusted-file detail must be clipped instead of reserved with the closing commands")
	}
	// The omission notice follows the commands, which remain before the
	// byline and unchanged marker, as for other clipped posts (D-0167).
	omission := strings.Index(visible, closingCommandsOmission)
	if omission < 0 || omission < strings.LastIndex(visible, "/invariant retry") || omission < strings.LastIndex(visible, "/invariant revise") {
		t.Error("shortening must preserve the closing commands before its omission notice")
	}
}
