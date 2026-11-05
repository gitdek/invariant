package decisions_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/project"
)

// Run the existing repository test itself, from a test-owned checkout. This
// exercises its verdict without depending on any real project's ratification
// or changing the checkout in which the acceptance suite is running.
func TestDecisionRepositoryHandLocksAllowNone(t *testing.T) {
	for _, tc := range []struct {
		name       string
		hands      bool
		comment    bool
		untracked  bool
		damage     string
		outsideGit bool
	}{
		{name: "empty checkout"},
		{name: "only issue ratifications", comment: true},
		{name: "untracked hand lock is not repository coverage", comment: true, untracked: true},
		{name: "two exact hand ratifications", hands: true, comment: true, untracked: true},
		{name: "first hand lock changed", hands: true, comment: true, damage: "first"},
		{name: "last hand lock changed", hands: true, comment: true, damage: "last"},
		{name: "both hand locks changed", hands: true, comment: true, damage: "both"},
		{name: "ratifying decision missing", hands: true, damage: "journal"},
		{name: "tracked lock malformed", hands: true, damage: "malformed"},
		{name: "tracked lock missing", hands: true, damage: "missing"},
		{name: "outside a git checkout", outsideGit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "internal/decisions")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			env := issue525GitEnv(root)
			git := func(args ...string) {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
				cmd.Env = env
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			if !tc.outsideGit {
				git("init", "-q")
				git("remote", "add", "origin", "https://github.com/gitdek/invariant.git")
			}
			if tc.comment {
				l := issue525Lock()
				// A retained decision ID must not make an issue-ratified lock
				// part of either repository hand-lock check.
				l.Decision = "D-" + "9999"
				l.Ratified = &project.Ratification{
					By: "gitdek", At: "2026-11-03T00:00:00Z", Issue: 525,
					Comment:  "https://github.com/gitdek/invariant/issues/525#issuecomment-1",
					Proposal: project.ProposalHash(l.Bounds, l.Statements),
				}
				issue525Project(t, root, "commented", l)
				git("add", "--", "factory/commented")
			}
			if tc.untracked {
				l := issue525Lock()
				l.Decision = "D-" + "9998" // Deliberately absent from the journal.
				issue525Project(t, root, "untracked", l)
			}
			var wantProblems []string
			if tc.hands {
				s, err := decisions.Open(filepath.Join(t.TempDir(), "decisions.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.Close() })
				repo := decisions.Repo{Name: "invariant", Dir: root}
				for _, name := range []string{"first", "last"} {
					l := issue525Lock()
					l.Statements[1].Says = name + ": x is always zero."
					id, err := s.Decide(repo, decisions.NewDecision{
						Door: "two-way", Status: "proposed", Who: "agent",
						Text: "Test fixture ratification for " + name + ".",
					}, "agent")
					if err != nil {
						t.Fatal(err)
					}
					l.Decision = id
					proposal := project.ProposalHash(l.Bounds, l.Statements)
					if err := s.RatifyProposal(repo, id, "@gitdek", proposal); err != nil {
						t.Fatal(err)
					}
					lockPath := issue525Project(t, root, name, l)
					git("add", "--", "factory/"+name)
					switch {
					case tc.damage == name || tc.damage == "both":
						l.Statements[1].Says += " Or one."
						issue525JSON(t, lockPath, l)
						wantProblems = append(wantProblems, "factory/"+name)
					case name == "last" && tc.damage == "journal":
						if err := os.Remove(filepath.Join(repo.JournalDir(), id+".jsonl")); err != nil {
							t.Fatal(err)
						}
						wantProblems = append(wantProblems, id, "isn't in the repository's journal")
					case name == "last" && tc.damage == "malformed":
						issue525Write(t, lockPath, []byte("{"))
						wantProblems = append(wantProblems, "ratified.lock")
					case name == "last" && tc.damage == "missing":
						if err := os.Remove(lockPath); err != nil {
							t.Fatal(err)
						}
						wantProblems = append(wantProblems, "ratified.lock")
					}
				}
			}

			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			const target = "TestTheRepositorysHandLocksHold"
			cmd := exec.CommandContext(ctx, exe, "-test.run=^"+target+"$", "-test.v", "-test.count=1", "-test.timeout=45s")
			cmd.Dir, cmd.Env = dir, env
			out, err := cmd.CombinedOutput()
			text := string(out)
			if ctx.Err() != nil {
				t.Fatalf("repository check did not finish: %v\n%s", ctx.Err(), text)
			}
			if !strings.Contains(text, "=== RUN   "+target+"\n") {
				t.Fatalf("the repository check did not run: %v\n%s", err, text)
			}
			if tc.outsideGit {
				if err != nil || !strings.Contains(text, "--- SKIP: "+target+" ") ||
					!strings.Contains(text, "isn't a git checkout") {
					t.Fatalf("the existing non-checkout skip changed: %v\n%s", err, text)
				}
				return
			}
			if strings.Contains(text, "--- SKIP:") {
				t.Fatalf("a real Git checkout must be checked, not skipped:\n%s", text)
			}
			if tc.damage != "" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 ||
					!strings.Contains(text, "--- FAIL: "+target+" ") {
					t.Fatalf("an invalid tracked hand lock did not fail the repository check: %v\n%s", err, text)
				}
				for _, want := range wantProblems {
					if !strings.Contains(text, want) {
						t.Errorf("the failure does not identify %q:\n%s", want, text)
					}
				}
				return
			}
			if err != nil || !strings.Contains(text, "--- PASS: "+target+" ") {
				t.Fatalf("the repository check should pass: %v\n%s", err, text)
			}
			if !tc.hands {
				absence := regexp.MustCompile("(?i)(no locks? (is |are )?ratified by hand|0 (of the repository's )?locks ratified by hand)")
				if !absence.MatchString(text) {
					t.Fatalf("a passing empty check must explain that there are no hand-ratified locks:\n%s", text)
				}
			}
		})
	}
}

func issue525GitEnv(root string) []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			env = append(env, value)
		}
	}
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
}

func issue525Lock() project.Lock {
	return project.Lock{
		Bounds: map[string]string{"N": "1"},
		Statements: []project.Statement{
			{Name: "Spec", Kind: project.Spec, Says: "x starts at zero and stays."},
			{Name: "Zero", Kind: project.Invariant, Says: "x is always zero."},
		},
	}
}

func issue525Project(t *testing.T, root, name string, lock project.Lock) string {
	t.Helper()
	dir := filepath.Join(root, "factory", name, ".invariant")
	issue525JSON(t, filepath.Join(dir, "invariant.json"), project.Manifest{
		Name: name, Module: ".invariant/specs/Demo.tla", Code: "demo", Language: "go",
	})
	issue525Write(t, filepath.Join(dir, "specs", "Demo.tla"),
		[]byte("---- MODULE Demo ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\nZero == x = 0\n====\n"))
	path := filepath.Join(dir, "ratified.lock")
	issue525JSON(t, path, lock)
	return path
}

func issue525JSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	issue525Write(t, path, b)
}

func issue525Write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
