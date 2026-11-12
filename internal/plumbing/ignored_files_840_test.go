package plumbing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkout840 is a git checkout holding files, with its ignore rules.
func checkout840(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "--quiet", "-b", "main")
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "--all", "--force", ".")
	git("-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed")
	return dir
}

// A plan that names a file the repository's ignore rules match is refused
// when it's checked, with the file and the rule (#840). git leaves such a
// file out of a commit without a word, so the build that wrote it published
// a branch that didn't compile, and nothing said why.
func TestPlanNamingAnIgnoredFileIsRefused840(t *testing.T) {
	plan := func(files []string, tests ...string) *Plan {
		p := &Plan{Files: files}
		for _, file := range tests {
			p.Tests = append(p.Tests, Test{Name: "TestIt", File: file})
		}
		return p
	}
	for _, c := range []struct {
		name  string
		files map[string]string
		plan  *Plan
		want  []string
	}{
		{"a file a rule matches", map[string]string{".gitignore": "*.out\ncoverage.*\n", "verify/verify.go": "package verify\n"},
			plan([]string{"verify/coverage.go", "verify/verify.go"}, "verify/coverage_accept_test.go"),
			[]string{"the plan names verify/coverage.go, which the repository's ignore rules leave out of every commit (.gitignore line 2: coverage.*)", "give the file a name those rules don't match"}},
		{"an acceptance test a rule matches", map[string]string{".gitignore": "*_accept_test.go\n"},
			plan([]string{"verify/verify.go"}, "verify/coverage_accept_test.go"),
			[]string{"the plan names verify/coverage_accept_test.go", ".gitignore line 1: *_accept_test.go"}},
		{"a rule in a directory of its own", map[string]string{"verify/.gitignore": "generated*\n"},
			plan([]string{"verify/generated.go"}),
			[]string{"the plan names verify/generated.go", "verify/.gitignore line 1: generated*"}},
		{"the plans' own directory", map[string]string{".gitignore": ".invariant/\n"},
			plan([]string{"verify/verify.go"}),
			[]string{"the repository's ignore rules leave .invariant/plans out of every commit (.gitignore line 1: .invariant/), so a ratified plan can't be recorded there"}},
		{"a rule another takes back", map[string]string{".gitignore": "coverage.*\n!coverage.go\n"},
			plan([]string{"verify/coverage.go"}), nil},
		{"a file the repository already tracks", map[string]string{".gitignore": "coverage.*\n", "verify/coverage.go": "package verify\n"},
			plan([]string{"verify/coverage.go"}), nil},
		{"rules that match nothing it names", map[string]string{".gitignore": "bin/\n*.out\n"},
			plan([]string{"verify/coverage.go"}, "verify/coverage_accept_test.go"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ignoredByGit(context.Background(), checkout840(t, c.files), c.plan)
			if len(c.want) == 0 && got != "" {
				t.Fatalf("refused: %s", got)
			}
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("the problem doesn't say %q:\n%s", want, got)
				}
			}
		})
	}
	// A directory that isn't a checkout has no rules to ask.
	if got := ignoredByGit(context.Background(), t.TempDir(), plan([]string{"verify/coverage.go"})); got != "" {
		t.Errorf("a plain directory refused a plan: %s", got)
	}
}

// The check a draft goes through refuses it before anything runs.
func TestCheckRefusesADraftNamingAnIgnoredFile840(t *testing.T) {
	repo := checkout840(t, map[string]string{".gitignore": "coverage.*\n", "go.mod": "module sample\n\ngo 1.24\n"})
	ws := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.json", `{"name":"Sample change","summary":"Implement the sample behavior.","files":["sample/coverage.go"],"tests":[{"name":"TestPending","file":"sample/pending_accept_test.go","says":"The sample behavior is implemented."}]}`)
	write("tests/sample/pending_accept_test.go", "package sample\n\nimport \"testing\"\n\nfunc TestPending(t *testing.T) { t.Fatal(\"pending\") }\n")
	// A sandbox that fails the test if the check gets as far as running it.
	checked, err := Check(context.Background(), ws, repo, Sandbox{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(checked.Problem, "the plan names sample/coverage.go, which the repository's ignore rules leave out of every commit (.gitignore line 1: coverage.*)") {
		t.Fatalf("the draft wasn't refused for its ignored file: %q", checked.Problem)
	}
	if checked.Plan == nil || len(checked.Run.Accepted) != 0 || checked.Run.TestResults != nil {
		t.Fatalf("the check ran the draft's tests anyway: %+v", checked.Run)
	}
}
