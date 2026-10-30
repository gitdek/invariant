package scope

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

// A regression test may extend a plan's coverage without extending its
// production scope or weakening the tests a person ratified.
func TestReviewRegressionTestsStayInTheirPlanPackages(t *testing.T) {
	const addedTest = "package page\n\nimport \"testing\"\n\nfunc TestReviewFinding(t *testing.T) {}\n"
	for _, tc := range []struct {
		name       string
		planFile   string
		acceptFile string
		file       string
		text       string
		remove     bool
		link       bool
		allowed    bool
		trusted    bool
	}{
		{name: "new regression in touched package", file: "page/review_test.go", text: addedTest, allowed: true},
		{name: "external test package", file: "page/external_review_test.go", text: "package page_test\n\nimport \"testing\"\n\nfunc TestReviewFinding(t *testing.T) {}\n", allowed: true},
		{name: "embedded asset touches its owning package", planFile: "page/web/app.js", acceptFile: "other/plan_accept_test.go", file: "page/review_test.go", text: addedTest, allowed: true},
		{name: "acceptance test touches its package", acceptFile: "other/plan_accept_test.go", file: "other/review_test.go", text: "package other\n", allowed: true},
		{name: "new package named by the plan", planFile: "newpage/page.go", acceptFile: "newpage/plan_accept_test.go", file: "newpage/review_test.go", text: "package newpage\n", allowed: true},
		{name: "new test in trusted package stays trusted", planFile: "internal/factory/hook.go", file: "internal/factory/review_test.go", text: "package factory\n", allowed: true, trusted: true},
		{name: "existing unnamed test cannot change", file: "page/existing_test.go", text: addedTest},
		{name: "existing unnamed test cannot disappear", file: "page/existing_test.go", remove: true},
		{name: "adjacent package stays outside", file: "other/review_test.go", text: "package other\n"},
		{name: "nested package stays outside", file: "page/child/review_test.go", text: "package child\n"},
		{name: "new nested package stays outside", file: "page/newchild/review_test.go", text: "package newchild\n"},
		{name: "ancestor package stays outside", file: "review_test.go", text: "package root\n"},
		{name: "production addition stays outside", file: "page/review.go", text: "package page\n"},
		{name: "acceptance source cannot change", file: "page/plan_accept_test.go", text: addedTest},
		{name: "acceptance source cannot disappear", file: "page/plan_accept_test.go", remove: true},
		{name: "a regression path cannot be a symbolic link", file: "page/review_test.go", link: true},
	} {
		func() {
			dir := repo(t)
			write(t, dir, map[string]string{
				"page/page.go":             "package page\n",
				"page/existing_test.go":    "package page\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) {}\n",
				"page/web/app.js":          "export const value = 1;\n",
				"page/child/child.go":      "package child\n",
				"other/other.go":           "package other\n",
				"internal/factory/hook.go": "package factory\n",
			})
			commit(t, dir, "existing packages and tests")
			gitRun(t, dir, "branch", "review-base")
			planFile, acceptFile := tc.planFile, tc.acceptFile
			if planFile == "" {
				planFile = "page/page.go"
			}
			if acceptFile == "" {
				acceptFile = "page/plan_accept_test.go"
			}
			pkg := filepath.Base(filepath.Dir(acceptFile))
			accept := "package " + pkg + "\n\nimport \"testing\"\n\nfunc TestPlanBehavior(t *testing.T) {}\n"
			plan := plumbing.Plan{
				Name: "reviewed change", Summary: "Implement the planned behavior and repair review findings within its scope.",
				Files:   []string{planFile},
				Tests:   []plumbing.Test{{Name: "TestPlanBehavior", File: acceptFile, Says: "The planned behavior holds."}},
				Sources: map[string]string{acceptFile: accept},
			}
			if err := plan.Validate(); err != nil {
				t.Fatal(err)
			}
			hash := plan.Hash()
			lock := plumbing.Lock{Plan: plan, Ratified: plumbing.Ratification{By: "gitdek", Issue: 7, Comment: "https://example.com/ratification", Proposal: hash}}
			if err := lock.Write(dir); err != nil {
				t.Fatal(err)
			}
			if planFile == "newpage/page.go" {
				write(t, dir, map[string]string{planFile: "package newpage\n"})
			}
			switch {
			case tc.remove:
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(tc.file))); err != nil {
					t.Fatal(err)
				}
			case tc.link:
				if err := os.Symlink("existing_test.go", filepath.Join(dir, filepath.FromSlash(tc.file))); err != nil {
					t.Fatal(err)
				}
			default:
				write(t, dir, map[string]string{tc.file: tc.text})
			}
			commit(t, dir, "plan and reviewed build")
			result, err := Check(context.Background(), dir, "review-base", "HEAD", 7)
			if err != nil {
				t.Fatal(err)
			}
			if result.OK() != tc.allowed {
				t.Errorf("%s: scope accepted = %v, want %v: %+v", tc.name, result.OK(), tc.allowed, result)
			}
			if !result.Plumbing || result.Project != plumbing.LockPath(7) {
				t.Errorf("the change lost its ratified plan identity: %+v", result)
			}
			if tc.trusted && !slices.Contains(result.Trusted, tc.file) {
				t.Errorf("new regression test in the trusted base was not marked trusted: %+v", result.Trusted)
			}
			var recorded plumbing.Lock
			record, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(plumbing.LockPath(7))))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(record, &recorded); err != nil {
				t.Fatal(err)
			}
			if recorded.Plan.Hash() != hash || recorded.Ratified.Proposal != hash {
				t.Fatal("scope checking changed the plan or its ratification")
			}
		}()
	}
}
