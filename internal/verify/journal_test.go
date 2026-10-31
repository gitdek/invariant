package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/project"
)

// journalCheckout is a git checkout keeping a decision journal, with a
// decision store for it, and a project in it whose lock names D-0001.
func journalCheckout(t *testing.T) (dir string, lock project.Lock, repo decisions.Repo, store *decisions.Store) {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	repo = decisions.Repo{Name: "demo", Dir: root}
	store, err := decisions.Open(filepath.Join(t.TempDir(), "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.Decide(repo, decisions.NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: "The demo's statements."}, "agent"); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock = project.Lock{Decision: "D-0001", Bounds: map[string]string{"N": "1"},
		Statements: []project.Statement{{Name: "Zero", Kind: project.Invariant, Says: "x is always 0.", SHA256: "sha256:00"}}}
	return dir, lock, repo, store
}

// The gate holds a lock ratified by hand to the proposal a person's ratify
// line names in the repository's journal, and says why when it isn't
// (D-0208).
func TestTheGateReadsAHandLocksRatificationInTheJournal(t *testing.T) {
	t.Run("ratified for exactly the lock's proposal", func(t *testing.T) {
		dir, lock, repo, store := journalCheckout(t)
		if err := store.RatifyProposal(repo, "D-0001", "@gitdek", project.ProposalHash(lock.Bounds, lock.Statements)); err != nil {
			t.Fatal(err)
		}
		var r Report
		ratification(t.Context(), &r, dir, lock)
		if r.Journal == nil || r.Journal.By != "@gitdek" || r.Journal.Problem != "" || !r.RatificationMatches() {
			t.Fatalf("a lock a person ratified exactly doesn't match: %+v", r.Journal)
		}
	})
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, lock *project.Lock, repo decisions.Repo, store *decisions.Store)
		want  string
	}{
		{"edited after its ratification", func(t *testing.T, lock *project.Lock, repo decisions.Repo, store *decisions.Store) {
			if err := store.RatifyProposal(repo, "D-0001", "@gitdek", project.ProposalHash(lock.Bounds, lock.Statements)); err != nil {
				t.Fatal(err)
			}
			lock.Bounds = map[string]string{"N": "2"}
		}, "isn't what @gitdek ratified"},
		{"ratified for no proposal", func(t *testing.T, lock *project.Lock, repo decisions.Repo, store *decisions.Store) {
			if err := store.Ratify(repo, "D-0001", "@gitdek"); err != nil {
				t.Fatal(err)
			}
		}, "no proposal in particular"},
		{"not ratified", func(t *testing.T, lock *project.Lock, repo decisions.Repo, store *decisions.Store) {}, "is proposed"},
		{"a decision the journal doesn't hold", func(t *testing.T, lock *project.Lock, repo decisions.Repo, store *decisions.Store) {
			lock.Decision = "D-0009"
		}, "isn't in the repository's journal"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, lock, repo, store := journalCheckout(t)
			c.setup(t, &lock, repo, store)
			var r Report
			ratification(t.Context(), &r, dir, lock)
			if r.Journal == nil || r.RatificationMatches() || !strings.Contains(r.Journal.Problem, c.want) {
				t.Fatalf("the lock matches, or the problem doesn't say %q: %+v", c.want, r.Journal)
			}
			if !strings.Contains(Feedback(&r), c.want) {
				t.Errorf("the feedback doesn't say %q:\n%s", c.want, Feedback(&r))
			}
		})
	}
	t.Run("outside a repository that keeps a journal", func(t *testing.T) {
		lock := project.Lock{Decision: "D-0001", Bounds: map[string]string{"N": "1"}}
		var r Report
		ratification(t.Context(), &r, t.TempDir(), lock)
		if r.Journal != nil || r.Proposal != "" || !r.RatificationMatches() {
			t.Fatalf("a lock ratified by hand outside a journal was checked against one: %+v", r)
		}
	})
}
