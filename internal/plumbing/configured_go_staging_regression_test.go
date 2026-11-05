package plumbing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestConfiguredGoPlanningRefusesCommittedLinks610(t *testing.T) {
	cache := t.TempDir()
	for _, kind := range []string{"symlink", "gitlink"} {
		t.Run(kind, func(t *testing.T) {
			selected := repositoryControl582()
			if kind == "symlink" {
				// Removing this valid fixture turns a passing test into a failure.
				selected.source = repositorySource582(`data,err:=os.ReadFile("testdata/linked.txt");if err!=nil{t.Fatal(err)};if string(data)!="fixture\n"{t.Fatal("changed fixture")}`, "")
			}
			f := newRepositoryFixture582(t, cache, []repositoryCase582{selected}, false,
				map[string]string{"api/nested/testdata/fixture.txt": "fixture\n"}, nil)
			f.sandbox.Repository.Adapters = nil
			name := "api/nested/testdata/linked.txt"
			if kind == "symlink" {
				if err := os.Symlink("fixture.txt", filepath.Join(f.root, name)); err != nil {
					t.Fatal(err)
				}
				if read484(t, f.root, name) != "fixture\n" {
					t.Fatal("fixture link does not resolve")
				}
				git468(t, f.root, "add", "--", name)
			} else {
				name = "vendor/submodule"
				commit := git468(t, f.root, "rev-parse", "HEAD")
				git468(t, f.root, "update-index", "--add", "--cacheinfo", "160000,"+commit+","+name)
			}
			git468(t, f.root, "commit", "-q", "--no-verify", "-m", "committed link fixture")
			snapshot, err := repository.Load(t.Context(), f.root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			f.sandbox.Repository.Snapshot = snapshot
			f.plan = n503Read(t, f.plan, snapshot)
			checked, err := check610(t, f)
			if err != nil || checked == nil || !strings.Contains(checked.Problem, name) || !strings.Contains(checked.Problem, "not a regular file") {
				t.Fatalf("committed %s did not refuse public planning: %v %+v", kind, err, checked)
			}
			if len(f.runner.observed) != 0 {
				t.Fatal("nonregular base reached preparation or execution")
			}
			repositoryResult582(t, checked.Run, f.plan.Tests[0], "not_run", name)
		})
	}
}

func TestConfiguredGoPlanningCopiesLiteralCommittedNames610(t *testing.T) {
	selected := repositoryControl582()
	selected.source = repositorySource582(`
for _,name:=range []string{"../../docs/example[1].txt","../../docs/star*.txt","../../docs/question?.txt","../../docs/one\ttwo.txt"}{
 data,err:=os.ReadFile(name)
 if err!=nil||string(data)!="captured fixture\n"{t.Skipf("lost captured fixture %q: %q %v",name,data,err)}
}
if Value!=7{t.Fatal("needs seven",Value)}
`, "")
	files := map[string]string{
		"docs/example[1].txt": "captured fixture\n",
		"docs/star*.txt":      "captured fixture\n",
		"docs/question?.txt":  "captured fixture\n",
		"docs/one\ttwo.txt":   "captured fixture\n",
	}
	f := newRepositoryFixture582(t, t.TempDir(), []repositoryCase582{selected}, false, files, nil)
	f.sandbox.Repository.Adapters = nil
	base := f.sandbox.Repository.Snapshot.Commit()
	original := git468(t, f.root, "rev-parse", "HEAD:docs/example[1].txt")
	// Neither a moved HEAD, dirty checkout nor a replacement blob may supply
	// the bytes copied for literal names from the retained commit.
	for name := range files {
		write468(t, f.root, name, "later commit\n")
	}
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-q", "--no-verify", "-m", "later fixture bytes")
	replacement := git468(t, f.root, "rev-parse", "HEAD:docs/example[1].txt")
	git468(t, f.root, "replace", original, replacement)
	for name := range files {
		write468(t, f.root, name, "working tree\n")
	}
	checked := eligible610(t, f)
	if checked.Plan.Execution.Base != base {
		t.Fatal("public planning replaced the captured base")
	}
	repositoryResult582(t, checked.Run, f.plan.Tests[0], "failed", "needs seven")
}
