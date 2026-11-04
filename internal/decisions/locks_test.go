package decisions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
)

// A ratify line may name the exact proposal the person ratified, and only a
// ratify line may name one (D-0208).
func TestOnlyARatificationNamesAProposal(t *testing.T) {
	s, repo := fixture(t)
	for _, text := range []string{"One.", "Two."} {
		if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: text}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	proposal := "sha256:" + strings.Repeat("0f", 32)
	if err := s.RatifyProposal(repo, "D-0001", "@gitdek", "sha256:not-a-hash"); err == nil {
		t.Error("a ratification named a proposal that isn't a hash")
	}
	if err := s.RatifyProposal(repo, "D-0001", "@gitdek", proposal); err != nil {
		t.Fatal(err)
	}
	d, err := Read(repo, "D-0001")
	if err != nil {
		t.Fatal(err)
	}
	if r := d.Ratification; d.Status != "ratified" || r == nil || r.By != "@gitdek" || r.Proposal != proposal || r.At == "" {
		t.Fatalf("the ratify line isn't the decision's ratification: %+v, %+v", d.Node, d.Ratification)
	}

	// Only a person ratifies, by their GitHub login: never a bot, and never
	// anything that would split a row of decisions/log.md.
	for _, by := range []string{"@invariant-code-factory[bot]", "@x | ratified | @gitdek", "@", "@-gitdek", "@git--dek"} {
		if err := s.RatifyProposal(repo, "D-0002", by, proposal); err == nil || !strings.Contains(err.Error(), "only a person ratifies") {
			t.Errorf("a ratification by %q was taken: %v", by, err)
		}
	}

	// A line that isn't a ratification, sealed as the store seals one, can't
	// name a proposal.
	path := filepath.Join(repo.JournalDir(), "D-0002.jsonl")
	events, err := ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	e := Event{At: "2026-10-31T00:00:00Z", By: "agent", Op: OpLink, ID: "D-0002", Edges: []Edge{{Type: Cites, To: "D-0001"}}, Proposal: proposal}
	if err := e.chain(events); err != nil {
		t.Fatal(err)
	}
	if _, err := fold(repo.Name, append(events, e)); err == nil || !strings.Contains(err.Error(), "only a ratify line names a proposal") {
		t.Errorf("a link named a proposal: %v", err)
	}
	// Nor can a ratify line record what a decision does, such as who made it.
	r := Event{At: "2026-10-31T00:00:00Z", By: "@gitdek", Op: OpRatify, ID: "D-0002", Who: "@someone"}
	if err := r.chain(events); err != nil {
		t.Fatal(err)
	}
	if _, err := fold(repo.Name, append(events, r)); err == nil || !strings.Contains(err.Error(), "nothing a decision records") {
		t.Errorf("a ratify line recorded who made the decision: %v", err)
	}
}

// A decision in force can supersede another by an edge alone, with no
// supersede line in the other's journal, and a lock ratified by hand holds
// for neither form of superseded decision (D-0208).
func TestALockHoldsForNoSupersededDecision(t *testing.T) {
	proposal := "sha256:" + strings.Repeat("0f", 32)
	for _, c := range []struct {
		name   string
		status string // the later decision's
		link   bool   // supersede by an edge, not a line
		holds  bool
	}{
		{"by a supersede line", "decided", false, false},
		{"by a decided decision's edge", "decided", true, false},
		{"by a ratified decision's edge", "ratified", true, false},
		{"not by a proposed decision's edge", "proposed", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, repo := fixture(t)
			if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: "The lock."}, "agent"); err != nil {
				t.Fatal(err)
			}
			if err := s.RatifyProposal(repo, "D-0001", "@gitdek", proposal); err != nil {
				t.Fatal(err)
			}
			who := "agent"
			if c.status == "ratified" {
				who = "@gitdek"
			}
			if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: c.status, Who: who, Text: "A later call."}, "agent"); err != nil {
				t.Fatal(err)
			}
			var err error
			if c.link {
				err = s.Link(repo, "D-0002", Edge{Type: Supersedes, To: "D-0001"}, "agent")
			} else {
				err = s.Supersede(repo, "D-0001", "D-0002", "agent")
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = RatifiesLock(repo, "D-0001", proposal)
			if (err == nil) != c.holds || (!c.holds && !strings.Contains(err.Error(), "superseded by demo/D-0002")) {
				t.Fatalf("the lock holds = %v, want %v: %v", err == nil, c.holds, err)
			}
		})
	}
}

// decisions/log.md keeps a | in who made a call within its cell.
func TestTheLogEscapesWho(t *testing.T) {
	table := Table([]Node{{ID: "demo/D-0001", Date: "2026-10-31", Door: "two-way", Status: "decided", Who: "agent | x", Text: "A call."}}, "demo")
	if !strings.Contains(table, `| A call. | agent \| x |`) {
		t.Errorf("the log's row splits who made the call:\n%s", table)
	}
}

// decisions check holds every lock ratified by hand that the checkout
// tracks to what its decision's ratify line names, and leaves a lock a
// comment ratified to the check on GitHub (D-0208).
func TestCheckLocksHoldsHandLocksToTheirRatification(t *testing.T) {
	s, repo := fixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo.Dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	lock := func(dir string, l project.Lock) string {
		t.Helper()
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(repo.Dir, dir, ".invariant", "ratified.lock"), string(b))
		return project.ProposalHash(l.Bounds, l.Statements)
	}
	statements := []project.Statement{{Name: "Zero", Kind: project.Invariant, Says: "x is always 0.", SHA256: "sha256:00"}}
	exact := lock("exact", project.Lock{Decision: "D-0001", Bounds: map[string]string{"N": "1"}, Statements: statements})
	lock("edited", project.Lock{Decision: "D-0002", Bounds: map[string]string{"N": "2"}, Statements: statements})
	lock("commented", project.Lock{Ratified: &project.Ratification{By: "gitdek", Issue: 1, Proposal: "sha256:aa"}, Bounds: map[string]string{"N": "3"}, Statements: statements})
	lock("untracked", project.Lock{Decision: "D-0009", Bounds: map[string]string{"N": "4"}, Statements: statements})
	for _, text := range []string{"Exact.", "Edited."} {
		if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: text}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RatifyProposal(repo, "D-0001", "@gitdek", exact); err != nil {
		t.Fatal(err)
	}
	if err := s.RatifyProposal(repo, "D-0002", "@gitdek", exact); err != nil {
		t.Fatal(err)
	}
	git("add", "exact", "edited", "commented")
	n, problems, err := CheckLocks(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(problems) != 1 || !strings.HasPrefix(problems[0], "edited/.invariant/ratified.lock: decision D-0002") ||
		!strings.Contains(problems[0], "isn't what @gitdek ratified") {
		t.Fatalf("checked %d locks, with problems %q; want 2, with one for the edited lock", n, problems)
	}
}

// Every lock the repository tracks as ratified by hand must hold, as
// decisions check finds them (D-0208). A checkout may have none.
func TestTheRepositorysHandLocksHold(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// The locks are the ones git tracks, so a copy without .git, as the
	// plumbing sandbox makes, has none to check. CI checks them (#362).
	if exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Run() != nil {
		t.Skip(root + " isn't a git checkout, so it tracks no locks to check")
	}
	n, problems, err := CheckLocks(context.Background(), Repo{Name: "invariant", Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("checked %d of the repository's locks ratified by hand, with problems:\n%s", n, strings.Join(problems, "\n"))
	}
	if n == 0 {
		t.Log("the repository has no lock ratified by hand to check")
	}
}

// Exercise the repository check in a checkout of its own, so it still
// checks a hand ratification after the last real project is amended.
func TestTheRepositorysHandLocksInAGitFixture(t *testing.T) {
	s, repo := fixture(t)
	repo.Name = "invariant"
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			env = append(env, value)
		}
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(repo.Dir))
	git := func(t *testing.T, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo.Dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(t, "init", "-q")
	id, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: "The demo's lock."}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	l := project.Lock{Decision: id, Bounds: map[string]string{"N": "1"},
		Statements: []project.Statement{{Name: "Zero", Kind: project.Invariant, Says: "x is always 0."}}}
	path := filepath.Join(repo.Dir, "factory", "demo", ".invariant", "ratified.lock")
	lock := func(t *testing.T) {
		t.Helper()
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		write(t, path, string(b))
	}
	lock(t)
	if err := s.RatifyProposal(repo, id, "@gitdek", project.ProposalHash(l.Bounds, l.Statements)); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(repo.Dir, "internal", "decisions")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, want string
		fails      bool
		change     func(t *testing.T)
	}{
		{"no tracked locks", "no lock ratified by hand", false, func(t *testing.T) {}},
		{"ratified for its proposal", "", false, func(t *testing.T) {
			git(t, "add", "--", "factory/demo")
		}},
		{"edited after ratification", "isn't what @gitdek ratified", true, func(t *testing.T) {
			l.Bounds["N"] = "2"
			lock(t)
		}},
		{"tracked lock missing", "ratified.lock", true, func(t *testing.T) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{"enumeration fails", "exit status", true, func(t *testing.T) {
			write(t, filepath.Join(repo.Dir, ".git", "index"), "invalid index")
		}},
	} {
		c.change(t)
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		const target = "TestTheRepositorysHandLocksHold"
		cmd := exec.CommandContext(ctx, exe, "-test.run=^"+target+"$", "-test.v", "-test.count=1", "-test.timeout=45s")
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.CombinedOutput()
		timedOut := ctx.Err()
		cancel()
		if timedOut != nil {
			t.Fatalf("%s: repository check did not finish: %v\n%s", c.name, timedOut, out)
		}
		if c.fails {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), "--- FAIL: "+target+" ") {
				t.Fatalf("%s: repository check should fail: %v\n%s", c.name, err, out)
			}
		} else if err != nil || !strings.Contains(string(out), "--- PASS: "+target+" ") {
			t.Fatalf("%s: repository check should pass: %v\n%s", c.name, err, out)
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("%s: repository check didn't say %q:\n%s", c.name, c.want, out)
		}
	}
}

// sealedAs is the hash a line would have, sealed as the store seals it.
func sealedAs(t *testing.T, e Event) string {
	t.Helper()
	e.Hash = ""
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// A journal line from before ratifications named proposals reads, and seals,
// exactly as it did: the proposal is left out of a line that names none.
func TestOldLinesSealAsTheyDid(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	events, err := ReadJournal(filepath.Join(root, "decisions", "journal", "D-0202.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Proposal != "" || sealedAs(t, e) != e.Hash {
			t.Fatalf("D-0202's %s line doesn't seal as it did", e.Op)
		}
	}
}
