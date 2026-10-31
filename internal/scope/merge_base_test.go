package scope

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

// A plumbing pull request's changed paths come from its merge base with
// base, so a test is a new regression file only if that merge base doesn't
// hold it, however base has moved since the branch diverged (#298).
func TestRegressionTestsAreNewOnlyAgainstTheMergeBase(t *testing.T) {
	const existing = "package page\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) {}\n"
	const weakened = "package page\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) { t.Skip(\"weakened\") }\n"
	const added = "package page\n\nimport \"testing\"\n\nfunc TestReviewFinding(t *testing.T) {}\n"
	for _, tc := range []struct {
		name    string
		gone    string            // what main deletes after the branch diverged
		later   map[string]string // what main writes after the branch diverged
		branch  map[string]string // what the branch writes besides its plan
		allowed bool
		problem string
	}{
		{name: "a test main deleted since is still one the branch changes", gone: "page/existing_test.go",
			branch: map[string]string{"page/existing_test.go": weakened}, problem: "it changes page/existing_test.go, which its plan doesn't name"},
		{name: "a new test is admitted however main has moved", gone: "page/existing_test.go", later: map[string]string{"README.md": "moved on\n"},
			branch: map[string]string{"page/review_test.go": added}, allowed: true},
		{name: "a test main added since is new to the branch", later: map[string]string{"page/later_test.go": existing},
			branch: map[string]string{"page/later_test.go": added}, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := repo(t)
			write(t, dir, map[string]string{"page/page.go": "package page\n", "page/existing_test.go": existing})
			commit(t, dir, "an existing package and test")
			// The branch diverges here, and main moves on without it.
			gitRun(t, dir, "branch", "-f", "main")
			gitRun(t, dir, "checkout", "-q", "main")
			if tc.gone != "" {
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(tc.gone))); err != nil {
					t.Fatal(err)
				}
			}
			write(t, dir, tc.later)
			commit(t, dir, "main moves on")
			gitRun(t, dir, "checkout", "-q", "invariant/issue-7-new")

			const accept = "package page\n\nimport \"testing\"\n\nfunc TestPlanBehavior(t *testing.T) {}\n"
			plan := plumbing.Plan{
				Name: "reviewed change", Summary: "Implement the planned behavior and repair review findings within its scope.",
				Files:   []string{"page/page.go"},
				Tests:   []plumbing.Test{{Name: "TestPlanBehavior", File: "page/plan_accept_test.go", Says: "The planned behavior holds."}},
				Sources: map[string]string{"page/plan_accept_test.go": accept},
			}
			if err := plan.Validate(); err != nil {
				t.Fatal(err)
			}
			lock := plumbing.Lock{Plan: plan, Ratified: plumbing.Ratification{By: "gitdek", Issue: 7, Comment: "https://example.com/ratification", Proposal: plan.Hash()}}
			if err := lock.Write(dir); err != nil {
				t.Fatal(err)
			}
			write(t, dir, map[string]string{"page/page.go": "package page\n\n// Built.\n"})
			write(t, dir, tc.branch)
			commit(t, dir, "the plan and its build")

			r, err := Check(context.Background(), dir, "main", "HEAD", 7)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK() != tc.allowed || (tc.problem != "" && !contains(r.Problems, tc.problem)) {
				t.Errorf("in scope = %v, with problems %q; want %v, with %q", r.OK(), r.Problems, tc.allowed, tc.problem)
			}
			if !r.Plumbing || r.Project != plumbing.LockPath(7) {
				t.Errorf("the change lost its ratified plan identity: %+v", r)
			}
		})
	}
}
