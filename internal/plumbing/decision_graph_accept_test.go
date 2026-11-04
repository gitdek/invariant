package plumbing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// The journal and graph checker are real. Only the Docker process is replaced,
// using the sandbox's existing structured result protocol. The fixture sources
// either fail unconditionally or pass unconditionally, as this stand-in reports.
const decisionDocker529 = `#!/bin/sh
[ "$1" = run ] || exit 0
src=
for arg in "$@"; do
  case "$arg" in *:/src) src=${arg%:/src} ;; esac
done
[ -f "$src/sample/pending_accept_test.go" ] || exit 71
echo '@@invariant vet=0'
echo '@@invariant test=1'
for pair in pending:TestPending other:TestOther; do
  file="sample/${pair%:*}_accept_test.go"
  name=${pair#*:}
  [ -f "$src/$file" ] || continue
  status=passed
  if grep -q 't.Fatal' "$src/$file"; then status=failed; fi
  printf '@@result {"kind":"test","package":"example.com/fixture/sample","name":"%s","file":"%s","phase":"accept","status":"%s"}\n' "$name" "$file" "$status"
done
echo '@@result {"kind":"done"}'
`

func write529(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sandbox529(t *testing.T) plumbing.Sandbox {
	t.Helper()
	bin := t.TempDir()
	write529(t, bin, "docker", decisionDocker529)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return plumbing.Sandbox{Image: "decision-fixture", ModCache: t.TempDir(), Timeout: time.Minute}
}

func repo529(t *testing.T, journal bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	write529(t, root, "go.mod", "module example.com/fixture\n\ngo 1.27.1\n")
	write529(t, root, "sample/sample.go", "package sample\n")
	if !journal {
		return root, ""
	}
	s, err := decisions.Open(filepath.Join(t.TempDir(), "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	repo := decisions.Repo{Name: "fixture", Dir: root}
	id, err := s.Decide(repo, decisions.NewDecision{
		Door: "two-way", Status: "decided", Who: "agent", Text: "Keep the sample behavior.",
	}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	write529(t, root, "decisions/log.md", "# Decisions\n\n"+decisions.TableStart+"\n"+decisions.TableEnd+"\n")
	if _, err := decisions.WriteLog(repo); err != nil {
		t.Fatal(err)
	}
	return root, id
}

func source529(t *testing.T, name, expression, comment string, passes bool) string {
	t.Helper()
	statement := "t.Fatal(fmt.Sprint(id))"
	if passes {
		statement = "_ = fmt.Sprint(id)"
	}
	data, err := format.Source([]byte(fmt.Sprintf(`package sample
import ("fmt"; "testing")
// %s
func %s(t *testing.T) {
    id := %s
    %s
}
`, comment, name, expression, statement)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func draft529(t *testing.T, ws string, sources map[string]string) *plumbing.Plan {
	t.Helper()
	tests := []plumbing.Test{{Name: "TestPending", File: "sample/pending_accept_test.go", Says: "The sample behavior is implemented."}}
	if _, ok := sources["sample/other_accept_test.go"]; ok {
		tests = append(tests, plumbing.Test{Name: "TestOther", File: "sample/other_accept_test.go", Says: "The other behavior is implemented."})
	}
	data, err := json.Marshal(struct {
		Name    string          `json:"name"`
		Summary string          `json:"summary"`
		Files   []string        `json:"files"`
		Tests   []plumbing.Test `json:"tests"`
	}{"Sample change", "Implement the sample behavior.", []string{"sample/sample.go"}, tests})
	if err != nil {
		t.Fatal(err)
	}
	write529(t, ws, "plan.json", string(data))
	for file, source := range sources {
		write529(t, ws, "tests/"+file, source)
	}
	p, err := plumbing.ReadDraft(ws)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func snapshot529(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		files[rel] = string(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Project naming is not part of the plan-check API. Compare the graph's own
// diagnostics without its project namespace, retaining file, line, edge and ID.
func graphText529(text string) string {
	re := regexp.MustCompile(`[A-Za-z0-9_.-]+/(sample/|SPEC\.md|decisions/log\.md|D-[0-9]{4,})`)
	return re.ReplaceAllString(text, "$1")
}

func graphProblems529(t *testing.T, root string, sources map[string]string) []string {
	t.Helper()
	staged := t.TempDir()
	if err := plumbing.CopyCheckout(context.Background(), root, staged); err != nil {
		t.Fatal(err)
	}
	for file, source := range sources {
		write529(t, staged, file, source)
	}
	s, err := decisions.Open(filepath.Join(t.TempDir(), "oracle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	repos := []decisions.Repo{{Name: "fixture", Dir: staged}}
	if err := s.Rebuild(repos); err != nil {
		t.Fatal(err)
	}
	problems, err := s.Check(repos)
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

func check529(t *testing.T, ws, root string, sandbox plumbing.Sandbox) *plumbing.Checked {
	t.Helper()
	baseBefore, draftBefore := snapshot529(t, root), snapshot529(t, ws)
	c, err := plumbing.Check(context.Background(), ws, root, sandbox)
	if err != nil || c == nil || c.Plan == nil {
		t.Fatalf("Check should return draft feedback: checked=%+v error=%v", c, err)
	}
	if !reflect.DeepEqual(baseBefore, snapshot529(t, root)) || !reflect.DeepEqual(draftBefore, snapshot529(t, ws)) {
		t.Fatal("Check changed the original checkout or draft")
	}
	return c
}

func TestPlanCheckReportsAcceptanceDecisionGraphProblems(t *testing.T) {
	sandbox := sandbox529(t)
	root, recorded := repo529(t, true)
	if problems := graphProblems529(t, root, nil); len(problems) != 0 {
		t.Fatalf("base fixture has graph problems: %v", problems)
	}
	// Construct nonexistent IDs here, so this acceptance file itself does not
	// introduce the very unknown citations it is meant to catch.
	unknown, other := fmt.Sprintf("D-%04d", 9999), fmt.Sprintf("D-%04d", 9998)
	sources := map[string]string{
		"sample/pending_accept_test.go": source529(t, "TestPending", fmt.Sprintf("%q", unknown), "Fixture literal.", false),
		"sample/other_accept_test.go":   source529(t, "TestOther", fmt.Sprintf("%q", recorded), "Fixture comment: "+other, false),
	}
	ws := t.TempDir()
	draft529(t, ws, sources)
	want := graphProblems529(t, root, sources)
	if len(want) != 2 {
		t.Fatalf("fixture should introduce exactly two graph problems: %v", want)
	}
	c := check529(t, ws, root, sandbox)
	for _, problem := range want {
		for _, got := range []string{c.Problem, c.Feedback()} {
			if !strings.Contains(graphText529(got), graphText529(problem)) {
				t.Errorf("draft feedback lost graph diagnostic %q: %q", problem, got)
			}
		}
	}
	// A correction in the same draft must be checked afresh, with no stale
	// citation edges retained from the rejected attempt.
	for _, expression := range []string{fmt.Sprintf("%q", recorded), `fmt.Sprintf("D-%04d", 9999)`} {
		for file := range sources {
			name := "TestPending"
			if strings.Contains(file, "other_") {
				name = "TestOther"
			}
			sources[file] = source529(t, name, expression, "Corrected fixture.", false)
		}
		draft529(t, ws, sources)
		if c := check529(t, ws, root, sandbox); c.Problem != "" {
			t.Errorf("corrected draft was refused: %s", c.Feedback())
		}
	}
	sources["sample/pending_accept_test.go"] = source529(t, "TestPending", fmt.Sprintf("%q", recorded), "Already passes.", true)
	draft529(t, ws, sources)
	if c := check529(t, ws, root, sandbox); !strings.Contains(c.Problem, "already pass") || !strings.Contains(c.Problem, "TestPending") {
		t.Errorf("graph success bypassed the existing acceptance rule: %s", c.Feedback())
	}
	plain, _ := repo529(t, false)
	sources["sample/pending_accept_test.go"] = source529(t, "TestPending", fmt.Sprintf("%q", unknown), "No journal here.", false)
	draft529(t, ws, sources)
	if c := check529(t, ws, plain, sandbox); c.Problem != "" {
		t.Errorf("a repository without a journal changed behavior: %s", c.Feedback())
	}
}

func TestPlanCheckUsesTheExistingJournalAndGraphValidation(t *testing.T) {
	sandbox := sandbox529(t)
	for _, kind := range []string{"SPEC and log", "damaged journal"} {
		t.Run(kind, func(t *testing.T) {
			root, recorded := repo529(t, true)
			sources := map[string]string{"sample/pending_accept_test.go": source529(t, "TestPending", fmt.Sprintf("%q", recorded), "Recorded citation.", false)}
			ws := t.TempDir()
			draft529(t, ws, sources)
			var want []string
			if kind == "SPEC and log" {
				write529(t, root, "SPEC.md", "# Sample\n\n- A rule with no decision.\n")
				write529(t, root, "decisions/log.md", "# Stale log\n\n"+decisions.TableStart+"\n"+decisions.TableEnd+"\n")
				want = graphProblems529(t, root, sources)
				if len(want) != 2 {
					t.Fatalf("expected both existing graph rules to fail: %v", want)
				}
			} else {
				path := "decisions/journal/" + recorded + ".jsonl"
				body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if err != nil {
					t.Fatal(err)
				}
				write529(t, root, path, strings.Replace(string(body), "Keep the sample behavior.", "An unsealed edit.", 1))
				want = []string{recorded + ".jsonl:1: the line's hash isn't its own, so it was edited"}
			}
			c := check529(t, ws, root, sandbox)
			for _, problem := range want {
				for _, got := range []string{c.Problem, c.Feedback()} {
					if !strings.Contains(graphText529(got), graphText529(problem)) {
						t.Errorf("draft feedback lost graph/journal diagnostic %q: %q", problem, got)
					}
				}
			}
		})
	}
}

type drafter529 struct {
	t       *testing.T
	sources map[string]string
	plan    *plumbing.Plan
}

func (*drafter529) Name() string { return "decision graph fixture" }

func (a *drafter529) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	// Deliberately leave an unchecked draft: final proposal creation must
	// enforce the rule regardless of what the planner claims to have checked.
	a.plan = draft529(a.t, job.Workspace, a.sources)
	return synth.Usage{}, nil
}

func TestPlanProposalRequiresValidAcceptanceDecisionGraph(t *testing.T) {
	sandbox := sandbox529(t)
	for _, kind := range []string{"unknown", "recorded", "runtime fixture"} {
		t.Run(kind, func(t *testing.T) {
			root, recorded := repo529(t, true)
			expression := fmt.Sprintf("%q", recorded)
			if kind == "unknown" {
				expression = fmt.Sprintf("%q", fmt.Sprintf("D-%04d", 9999))
			} else if kind == "runtime fixture" {
				expression = `fmt.Sprintf("D-%04d", 9999)`
			}
			sources := map[string]string{"sample/pending_accept_test.go": source529(t, "TestPending", expression, "Acceptance fixture.", false)}
			agent := &drafter529{t: t, sources: sources}
			f := formalize.Formalizer{Backend: agent, Binary: "unused", CheckRuns: 4, Timeout: time.Minute, Sandbox: sandbox}
			before := snapshot529(t, root)
			out := t.TempDir()
			result, err := f.Plan(context.Background(), formalize.Request{Repo: "gitdek/invariant", Issue: 529, Title: "Sample", Body: "Kind: plumbing", Author: "gitdek"}, root, out)
			if err != nil || result == nil || agent.plan == nil {
				t.Fatalf("Plan: result=%+v error=%v", result, err)
			}
			if !reflect.DeepEqual(before, snapshot529(t, root)) {
				t.Fatal("final planning check changed the base")
			}
			data, err := os.ReadFile(filepath.Join(out, "formalization.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved formalize.Result
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			for _, got := range []*formalize.Result{result, &saved} {
				if kind == "unknown" {
					if got.Proposal != nil || !strings.Contains(got.Problem, "sample/pending_accept_test.go:") || !strings.Contains(got.Problem, "which isn't a decision") {
						t.Errorf("factory proposed an invalid graph or hid its diagnostic: %+v", got)
					}
				} else if got.Problem != "" || got.Proposal == nil || got.Proposal.Plan == nil {
					t.Errorf("valid decision references could not be proposed: %+v", got)
				} else if got.Proposal.Hash != agent.plan.Hash() || !reflect.DeepEqual(got.Proposal.Plan.Sources, sources) {
					t.Errorf("proposal changed the checked plan or acceptance sources: %+v", got.Proposal)
				}
			}
		})
	}
}
