package ci

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A stand-in for gh that answers `gh api PATH --jq EXPR` from a file named
// for the path, and fails as gh does when there's none. It notes each path
// it's asked for.
const fakeGH = `#!/usr/bin/env bash
printf '%s\n' "$2" >> "$FAKE_GH/calls"
[ "$1" = api ] && [ "$3" = --jq ] || exit 2
file="$FAKE_GH/$(printf '%s' "$2" | tr -c 'A-Za-z0-9._-' '_')"
[ -f "$file" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
jq -r "$4" "$file"
`

// A push to main skips its checks and tests only when a pull request's gate
// passed on its tree (#874), which .github/gated-tree.sh reads from GitHub's
// record: an artifact named for the tree, from a run of this workflow on a
// pull request from this repository, that passed. Anything else, and
// anything it can't ask or read, says the tests run.
func TestGatedTreeCountsOnlyAPullRequestsPassedGate(t *testing.T) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the stand-in for gh needs %s: %v", tool, err)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", ".github", "gated-tree.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const (
		tree   = "85ec082d99920426af0a7624309654187210d6a1"
		sha256 = "85ec082d99920426af0a7624309654187210d6a185ec082d99920426af0a7624"
		passed = `{"id": %d, "event": "pull_request", "conclusion": "success", "path": ".github/workflows/gate.yml"}`
	)
	artifact := func(run int, expired bool, head int) string {
		return fmt.Sprintf(`{"name": "gated", "expired": %t, "workflow_run": {"id": %d, "repository_id": 1, "head_repository_id": %d}}`, expired, run, head)
	}
	listed := func(artifacts ...string) string {
		return `{"total_count": ` + fmt.Sprint(len(artifacts)) + `, "artifacts": [` + strings.Join(artifacts, ", ") + `]}`
	}
	for _, c := range []struct {
		name      string
		tree      string
		artifacts string         // GitHub's answer for the tree's record, or "" when it can't be asked
		runs      map[int]string // each run GitHub can show
		want      bool
		asked     int // how many times GitHub was asked, or -1 where it doesn't matter
	}{
		{"a pull request's gate that passed", tree, listed(artifact(7, false, 1)), map[int]string{7: fmt.Sprintf(passed, 7)}, true, 2},
		{"a tree named by SHA-256", sha256, listed(artifact(7, false, 1)), map[int]string{7: fmt.Sprintf(passed, 7)}, true, 2},
		{"a second record, when the first run failed", tree, listed(artifact(6, false, 1), artifact(7, false, 1)),
			map[int]string{6: `{"id": 6, "event": "pull_request", "conclusion": "failure", "path": ".github/workflows/gate.yml"}`, 7: fmt.Sprintf(passed, 7)}, true, 3},
		{"no record", tree, listed(), nil, false, 1},
		{"a fork's run", tree, listed(artifact(7, false, 2)), map[int]string{7: fmt.Sprintf(passed, 7)}, false, 1},
		{"a record that expired", tree, listed(artifact(7, true, 1)), map[int]string{7: fmt.Sprintf(passed, 7)}, false, 1},
		{"a run that failed", tree, listed(artifact(7, false, 1)), map[int]string{7: `{"id": 7, "event": "pull_request", "conclusion": "failure", "path": ".github/workflows/gate.yml"}`}, false, 2},
		{"a run that hasn't ended", tree, listed(artifact(7, false, 1)), map[int]string{7: `{"id": 7, "event": "pull_request", "conclusion": null, "path": ".github/workflows/gate.yml"}`}, false, 2},
		{"a push's run", tree, listed(artifact(7, false, 1)), map[int]string{7: `{"id": 7, "event": "push", "conclusion": "success", "path": ".github/workflows/gate.yml"}`}, false, 2},
		{"another workflow's run", tree, listed(artifact(7, false, 1)), map[int]string{7: `{"id": 7, "event": "pull_request", "conclusion": "success", "path": ".github/workflows/other.yml"}`}, false, 2},
		{"a record GitHub can't be asked for", tree, "", nil, false, 1},
		{"a run GitHub can't show", tree, listed(artifact(7, false, 1)), nil, false, 2},
		{"an answer that isn't GitHub's", tree, `<html>Unicorn</html>`, nil, false, 1},
		{"no tree", "", listed(artifact(7, false, 1)), map[int]string{7: fmt.Sprintf(passed, 7)}, false, 0},
		{"a branch's name", "main", listed(artifact(7, false, 1)), map[int]string{7: fmt.Sprintf(passed, 7)}, false, 0},
		{"a tree with more of a query after it", tree + "&name=gated-" + tree, listed(artifact(7, false, 1)), map[int]string{7: fmt.Sprintf(passed, 7)}, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			named := func(path string) string {
				return regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(path, "_")
			}
			write("gh", fakeGH, 0o755)
			query := "repos/owner/name/actions/artifacts?name=gated-" + tree + "&per_page=100"
			if c.tree == sha256 {
				query = strings.Replace(query, tree, sha256, 1)
			}
			if c.artifacts != "" {
				write(named(query), c.artifacts, 0o644)
			}
			for id, body := range c.runs {
				write(named(fmt.Sprintf("repos/owner/name/actions/runs/%d", id)), body, 0o644)
			}
			cmd := exec.Command("bash", script, c.tree)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_GH="+dir, "GITHUB_REPOSITORY=owner/name")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s: %v", script, err)
			}
			if want := fmt.Sprintf("tested=%t\n", c.want); string(out) != want {
				t.Errorf("it said %q, want %q", out, want)
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			asked := strings.Fields(string(calls))
			if c.asked >= 0 && len(asked) != c.asked {
				t.Errorf("it asked GitHub %d times, for %q, want %d", len(asked), asked, c.asked)
			}
			if len(asked) > 0 && asked[0] != query {
				t.Errorf("it asked for %q, want the tree's own record, %q", asked[0], query)
			}
		})
	}

	t.Run("no repository to ask", func(t *testing.T) {
		cmd := exec.Command("bash", script, tree)
		cmd.Env = append(os.Environ(), "GITHUB_REPOSITORY=")
		if out, err := cmd.Output(); err != nil || string(out) != "tested=false\n" {
			t.Errorf("it said %q, %v", out, err)
		}
	})
}

// The workflow skips only the checks and tests on such a push. Every project
// is still verified and invariant/gate still reports, so each commit on main
// has its gate and its receipts (D-0083). Only a push asks, and only a pull
// request's gate that passed leaves the record.
func TestAGatedTreeSkipsOnlyTheChecksAndTests(t *testing.T) {
	yml := workflow(t)
	_, body, found := strings.Cut(yml, "\njobs:\n")
	if !found {
		t.Fatal("gate.yml has no jobs")
	}
	yml, jobs := yml[:len(yml)-len(body)]+body, map[string]string{}
	starts := regexp.MustCompile(`(?m)^  ([a-z-]+):\n`).FindAllStringSubmatchIndex(body, -1)
	for i, m := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		jobs[body[m[2]:m[3]]] = body[m[0]:end]
	}
	condition := regexp.MustCompile(`(?m)^    if: (.*)$`)
	const skipped = "needs.changes.outputs.tested == 'true'"
	for _, name := range []string{"verify", "verify-tests", "integration", "python", "python-latest", "real-python", "real-tlaps"} {
		m := condition.FindStringSubmatch(jobs[name])
		if m == nil || !strings.Contains(m[1], "needs.changes.result == 'success' && (needs.changes.outputs.gated == 'false' || "+skipped) {
			t.Errorf("%s doesn't skip on a tree a pull request's gate passed on, and only when the first job passed: %q", name, m)
		}
	}
	for _, name := range []string{"changes", "projects", "project", "receipts", "gate"} {
		if m := condition.FindStringSubmatch(jobs[name]); jobs[name] == "" || m != nil && strings.Contains(m[1], "tested") {
			t.Errorf("%s must run on every gated push: %q", name, m)
		}
	}
	if len(jobs) != 12 {
		t.Errorf("gate.yml has %d jobs: say here whether each new one is a check a gated tree skips", len(jobs))
	}

	asks := regexp.MustCompile(`(?s)      - id: tested\n        if: github\.event_name == 'push'\n.*?run: \.github/gated-tree\.sh "\$TREE" \| tee -a "\$GITHUB_OUTPUT"\n`)
	if !asks.MatchString(jobs["changes"]) {
		t.Error("the first job doesn't ask about the tree on a push alone")
	}
	if !strings.Contains(jobs["changes"], "tree=$(git rev-parse 'HEAD^{tree}')") || !strings.Contains(yml, "\n  actions: read\n") {
		t.Error("the first job doesn't name the tree it checked out, or the workflow can't read artifacts")
	}
	records := regexp.MustCompile(`(?s)      - name: Upload the record\n        if: github\.event_name == 'pull_request' && needs\.changes\.outputs\.tree != ''\n        continue-on-error: true\n        uses: actions/upload-artifact@v\d+\n        with:\n          name: gated-\$\{\{ needs\.changes\.outputs\.tree \}\}\n`)
	loc := records.FindStringIndex(jobs["gate"])
	passes := strings.Index(jobs["gate"], `[ "$REAL_TLAPS" = success ] || [ "$REAL_TLAPS" = skipped ]`)
	if loc == nil || passes < 0 || loc[0] < passes {
		t.Error("the gate doesn't record the tree it passed on, after its last check, on a pull request alone")
	}
	// Where the checks were skipped they must be skipped, and where they ran
	// they must have passed.
	for _, line := range []string{
		`if [ "$TESTED" = true ]; then`,
		`[ "$result" = skipped ] || [ "$result" = success ] || exit 1`,
		`[ "$VERIFY" = success ] && [ "$VERIFY_TESTS" = success ] && [ "$PYTHON" = success ] && [ "$PYTHON_LATEST" = success ] || exit 1`,
		`[ "$PROJECTS" = success ] || exit 1`,
	} {
		if !strings.Contains(jobs["gate"], line) {
			t.Errorf("the gate's check lost %q", line)
		}
	}
}
