package setup

// CheckRepository(ctx, GitHub, repo, base string, configured bool) adds the
// configured gate preflight. Check remains the compatible modeled-only API.
// A configured gate must actually invoke repository checks, factory scope and
// guarded project/plan ratification in invariant/gate: a job name, comment,
// disabled step or invocation in an unrelated job is not this capability.
// A refusal says to manually upgrade the gate using invariant init's pinned
// template, review/merge it with any custom steps and commit it to the base.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const revision736 = "61eb0495cd8a5ff84be5bbdb4efc3bb2b7c1ae23"
const base736 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const head736 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type step736 struct{ text, script string }

// Read the literal run blocks emitted by Workflow, not a second hand-written
// copy of the workflow's shell code. No YAML interpreter is added to the gate.
func steps736(t *testing.T, w string) []step736 {
	t.Helper()
	var steps []step736
	lines := strings.Split(w, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "      - ") {
			if start >= 0 {
				steps = append(steps, parseStep736(t, lines[start:i]))
			}
			start = i
		}
	}
	if start >= 0 {
		steps = append(steps, parseStep736(t, lines[start:]))
	}
	return steps
}

func parseStep736(t *testing.T, lines []string) step736 {
	t.Helper()
	s := step736{text: strings.Join(lines, "\n")}
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "run:") && !strings.HasPrefix(trim, "- run:") {
			continue
		}
		_, value, _ := strings.Cut(trim, "run:")
		value = strings.TrimSpace(value)
		if value != "|" && value != "|-" {
			s.script = value
			return s
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		for _, body := range lines[i+1:] {
			if strings.TrimSpace(body) == "" {
				s.script += "\n"
				continue
			}
			n := len(body) - len(strings.TrimLeft(body, " "))
			if n <= indent {
				break
			}
			if n < indent+2 {
				t.Fatal("invalid generated run indentation")
			}
			s.script += body[indent+2:] + "\n"
		}
		return s
	}
	return s
}

func findStep736(t *testing.T, w, command string) step736 {
	t.Helper()
	var found []step736
	for _, s := range steps736(t, w) {
		for _, line := range strings.Split(s.script, "\n") {
			trim := strings.TrimSpace(line)
			if !strings.HasPrefix(trim, "#") && strings.Contains(line, " "+command) {
				found = append(found, s)
				break
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("generated gate needs one executable %s step; found %d\n%s", command, len(found), w)
	}
	return found[0]
}

func workflow736(t *testing.T) string {
	t.Helper()
	w, err := Workflow(revision736)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func writeGate736(t *testing.T, root, file, text string, mode os.FileMode) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func script736(t *testing.T, s step736, files []string, event, headRef, fail string) ([][]string, error, string) {
	t.Helper()
	root, bin := t.TempDir(), t.TempDir()
	for _, file := range files {
		writeGate736(t, root, file, "{}\n", 0o644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "--all"}} {
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, b)
		}
	}
	// External command fake validates the CLI contract and logs actual argv.
	// It does not supply alternate workflow discovery or branch parsing.
	writeGate736(t, bin, "invariant", `#!/bin/bash
printf '%s' "$1" >> "$GATE736_LOG"
shift
for arg in "$@"; do printf '\t%s' "$arg" >> "$GATE736_LOG"; done
printf '\n' >> "$GATE736_LOG"
command=$(tail -n 1 "$GATE736_LOG" | cut -f1)
if [[ "$command" == ratification && $# -le 2 ]]; then exit 93; fi
if [[ "$command" == scope ]]; then
  issue=
  while [[ $# -gt 0 ]]; do
    if [[ "$1" == -issue ]]; then shift; issue=$1; fi
    shift
  done
  [[ "$issue" == 736 ]] || exit 94
fi
[[ "$command" != "$GATE736_FAIL" ]]
`, 0o755)
	log := filepath.Join(bin, "calls")
	env := append(os.Environ(), "RUNNER_TEMP="+bin, "GITHUB_REPOSITORY=fixture/repository", "GITHUB_EVENT_NAME="+event, "GITHUB_HEAD_REF="+headRef, "GITHUB_BASE_REF=main", "GITHUB_SHA="+head736, "GH_TOKEN=fixture-token", "GATE736_LOG="+log, "GATE736_FAIL="+fail)
	// Render literal step env declarations exactly as Actions would for these
	// fixtures. Expressions are values; no issue or branch text enters code.
	for _, line := range strings.Split(s.text, "\n") {
		trim := strings.TrimSpace(line)
		key, value, ok := strings.Cut(trim, ":")
		if !ok || !strings.Contains(value, "${{") {
			continue
		}
		resolved := ""
		switch {
		case strings.Contains(value, "pull_request.base.sha"):
			resolved = base736
			if event == "push" {
				resolved = head736
			}
		case strings.Contains(value, "pull_request.head.sha"):
			resolved = head736
		case strings.Contains(value, "github.base_ref"):
			resolved = "main"
		case strings.Contains(value, "github.head_ref"):
			resolved = headRef
		case strings.Contains(value, "github.event_name"):
			resolved = event
		case strings.Contains(value, "github.sha"):
			resolved = head736
		case strings.Contains(value, "github.token"):
			resolved = "fixture-token"
		case strings.Contains(value, "github.repository"):
			resolved = "fixture/repository"
		default:
			continue
		}
		if !strings.ContainsAny(key, " -") {
			env = append(env, key+"="+resolved)
		}
	}
	c := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", s.script)
	c.Dir = root
	c.Env = env
	b, err := c.CombinedOutput()
	logged, readErr := os.ReadFile(log)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
		if line != "" {
			calls = append(calls, strings.Split(line, "\t"))
		}
	}
	return calls, err, string(b)
}

func option736(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=")
		}
	}
	return ""
}

func TestGeneratedGate736RunsRepositoryChecks(t *testing.T) {
	w := workflow736(t)
	s := findStep736(t, w, "check-repository")
	for _, want := range []string{"ref: " + revision736, "name: invariant/gate", "contents: read", "issues: read", "persist-credentials: false", "fetch-depth: 0", "pull_request:", "push:", "actions/upload-artifact@", "if: always()", "name: receipts", "path: out"} {
		if !strings.Contains(w, want) {
			t.Errorf("generated gate lost %q", want)
		}
	}
	for _, bad := range []string{"contents: write", "issues: write", "pull-requests: write", "{{REF}}"} {
		if strings.Contains(w, bad) {
			t.Errorf("generated gate contains %q", bad)
		}
	}
	if !strings.Contains(s.text, "pull_request.base.sha") || !strings.Contains(s.text, "github.sha") {
		t.Fatalf("repository check lacks committed CI authority:\n%s", s.text)
	}
	if strings.Contains(s.text, "continue-on-error: true") {
		t.Fatal("repository failure cannot fail invariant/gate")
	}
	// This step must run for ordinary PRs and pushes, not only factory branches.
	for _, line := range strings.Split(s.text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "if:") {
			t.Fatalf("repository checks must be unconditional for configured CI: %s", line)
		}
	}
	for _, tc := range []struct{ event, branch string }{{"pull_request", "feature"}, {"pull_request", "invariant/issue-736-fixture"}, {"push", ""}} {
		calls, err, out := script736(t, s, nil, tc.event, tc.branch, "")
		if err != nil {
			t.Fatalf("repository step %s: %v\n%s", tc.event, err, out)
		}
		var check []string
		for _, call := range calls {
			if call[0] == "check-repository" {
				if check != nil {
					t.Fatal("duplicate repository invocation")
				}
				check = call
			}
		}
		if check == nil {
			t.Fatalf("%s skipped repository checks: %v", tc.event, calls)
		}
		if tc.event == "pull_request" && option736(check, "-base") != base736 {
			t.Fatalf("PR check did not use committed base: %v", check)
		}
		if tc.branch == "invariant/issue-736-fixture" && option736(check, "-issue") != "736" {
			t.Fatalf("factory issue was not derived: %v", check)
		}
		if tc.branch != "invariant/issue-736-fixture" && option736(check, "-issue") != "" && option736(check, "-issue") != "0" {
			t.Fatal(check)
		}
		_, err, out = script736(t, s, nil, tc.event, tc.branch, "check-repository")
		if err == nil {
			t.Fatalf("repository failure did not fail generated step: %s", out)
		}
	}
	verify := findStep736(t, w, "verify")
	calls, err, out := script736(t, verify, []string{"models/one/.invariant/invariant.json"}, "pull_request", "feature", "")
	if err != nil || len(calls) != 1 || calls[0][0] != "verify" || !slices.Contains(calls[0], "-out") || !slices.Contains(calls[0], "models/one") {
		t.Fatalf("modeled gate or receipts lost: %v %v\n%s", calls, err, out)
	}
}

func TestGeneratedGate736RatifiesPlansIndependently(t *testing.T) {
	w := workflow736(t)
	s := findStep736(t, w, "ratification")
	for _, tc := range []struct {
		name        string
		files, want []string
	}{
		{"plan only", []string{".invariant/plans/issue-736.json"}, []string{".invariant/plans/issue-736.json"}},
		{"mixed", []string{".invariant/plans/issue-736.json", ".invariant/plans/issue-10.json", "models/one/.invariant/invariant.json", "models/two/.invariant/invariant.json"}, []string{".invariant/plans/issue-736.json", ".invariant/plans/issue-10.json", "models/one", "models/two"}},
		{"empty", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, err, out := script736(t, s, tc.files, "pull_request", "feature", "")
			if err != nil {
				t.Fatalf("ratification script: %v\n%s", err, out)
			}
			var got []string
			for _, call := range calls {
				if call[0] != "ratification" {
					continue
				}
				if option736(call, "-repo") != "fixture/repository" {
					t.Fatal(call)
				}
				for i := 1; i < len(call); i++ {
					if call[i] == "-repo" {
						i++
						continue
					}
					got = append(got, call[i])
				}
			}
			slices.Sort(got)
			slices.Sort(tc.want)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("independent discovery: got %v want %v", got, tc.want)
			}
			if len(tc.want) > 0 {
				_, err, out = script736(t, s, tc.files, "pull_request", "feature", "ratification")
				if err == nil {
					t.Fatalf("ratification failure hidden: %s", out)
				}
			}
		})
	}
	scopeStep := findStep736(t, w, "scope")
	if !strings.Contains(scopeStep.text, "pull_request") || !strings.Contains(scopeStep.text, "invariant/") {
		t.Fatal("scope lost factory PR condition")
	}
	calls, err, out := script736(t, scopeStep, nil, "pull_request", "invariant/issue-736-a-slug", "")
	if err != nil || len(calls) != 1 || option736(calls[0], "-issue") != "736" || option736(calls[0], "-base") == "" {
		t.Fatalf("factory scope issue lost: %v %v\n%s", calls, err, out)
	}
	_, err, out = script736(t, scopeStep, nil, "pull_request", "invariant/issue-736-a-slug", "scope")
	if err == nil {
		t.Fatalf("scope failure hidden: %s", out)
	}
}

type capabilityGitHub736 struct {
	workflows map[string]string
	base      string
}

func (g *capabilityGitHub736) Workflows(_ context.Context, base string) (map[string]string, error) {
	g.base = base
	return g.workflows, nil
}
func (*capabilityGitHub736) MergeCommits(context.Context) (bool, error) { return true, nil }

func TestConfiguredGate736RequiresCapabilities(t *testing.T) {
	w := workflow736(t)
	checks := findStep736(t, w, "check-repository")
	disabled := strings.Replace(w, checks.text, strings.Replace(checks.text, "        run:", "        if: false\n        run:", 1), 1)
	ignored := strings.Replace(w, checks.text, strings.Replace(checks.text, "        run:", "        continue-on-error: true\n        run:", 1), 1)
	old := "name: gate\non: [push, pull_request]\njobs:\n  gate:\n    name: invariant/gate\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo old\n"
	for _, tc := range []struct {
		name, workflow   string
		configured, pass bool
	}{
		{"legacy modeled", old, false, true},
		{"legacy configured", old, true, false},
		{"current configured", w, true, true},
		{"current modeled", w, false, true},
		{"missing checks", strings.ReplaceAll(w, "check-repository", "echo removed-check"), true, false},
		{"missing ratification", strings.ReplaceAll(w, " ratification", " echo removed-ratification"), true, false},
		{"missing scope", strings.ReplaceAll(w, " scope", " echo removed-scope"), true, false},
		{"disabled checks", disabled, true, false},
		{"ignored failure", ignored, true, false},
		{"comment only", old + "# check-repository scope ratification .invariant/plans/issue-N.json\n", true, false},
		{"other job", old + "  unrelated:\n    runs-on: ubuntu-latest\n    steps:\n      - run: invariant check-repository\n      - run: invariant scope\n      - run: invariant ratification -repo fixture/repository .invariant/plans/issue-736.json\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &capabilityGitHub736{workflows: map[string]string{WorkflowPath: tc.workflow}}
			err := CheckRepository(t.Context(), g, "fixture/repository", "trunk", tc.configured)
			if (err == nil) != tc.pass {
				t.Fatalf("capability result: %v", err)
			}
			if g.base != "trunk" {
				t.Fatal("setup checked the wrong branch")
			}
			if !tc.pass {
				for _, word := range []string{"upgrade", "invariant init", "review", "commit", "trunk"} {
					if !strings.Contains(strings.ToLower(err.Error()), word) {
						t.Errorf("upgrade advice lacks %q: %v", word, err)
					}
				}
			}
		})
	}
}
