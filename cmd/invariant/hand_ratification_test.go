package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/project"
)

// ratificationPrints runs invariant ratification with args, and returns its
// exit code and everything it printed.
func ratificationPrints(t *testing.T, args ...string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	printed := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		printed <- string(b)
	}()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	log.SetOutput(w)
	code := func() int {
		defer func() {
			os.Stdout, os.Stderr = stdout, stderr
			log.SetOutput(stderr)
		}()
		return ratificationCmd(t.Context(), args)
	}()
	w.Close()
	return code, <-printed
}

// handProject is a checkout of the demo repository holding one project,
// whose lock names decision instead of a ratifying comment, and a decision
// store for its journal.
func handProject(t *testing.T, decision string) (dir string, repo decisions.Repo, store *decisions.Store) {
	t.Helper()
	root := repoWithOrigin(t, "https://github.com/gitdek/demo.git", "README.md", "demo\n")
	dir = filepath.Join(root, "factory", "demo")
	files := map[string]string{
		".invariant/invariant.json": `{"name": "demo", "module": ".invariant/specs/Demo.tla", "code": "demo", "language": "go"}`,
		".invariant/specs/Demo.tla": "---- MODULE Demo ----\nVARIABLE x\nInit == x = 0\nNext == x' = x\nSpec == Init /\\ [][Next]_x\nZero == x = 0\n====\n",
	}
	lock := map[string]any{
		"bounds": map[string]string{"N": "1"},
		"statements": []map[string]string{
			{"name": "Spec", "kind": "spec", "says": "x starts at 0 and stays.", "sha256": ""},
			{"name": "Zero", "kind": "invariant", "says": "x is always 0.", "sha256": ""},
		},
	}
	if decision != "" {
		lock["decision"] = decision
	}
	b, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	files[".invariant/ratified.lock"] = string(b)
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err = decisions.Open(filepath.Join(t.TempDir(), "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return dir, decisions.Repo{Name: "demo", Dir: root}, store
}

func decide(t *testing.T, store *decisions.Store, repo decisions.Repo, status, who string) string {
	t.Helper()
	return decideBy(t, store, repo, status, who, "agent")
}

// decideBy records a decision as by writes it down.
func decideBy(t *testing.T, store *decisions.Store, repo decisions.Repo, status, who, by string) string {
	t.Helper()
	id, err := store.Decide(repo, decisions.NewDecision{Door: "two-way", Status: status, Who: who,
		Text: "The demo's statements are what a person ratified."}, by)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// proposalOf is the ProposalHash of the lock in dir, as it is now.
func proposalOf(t *testing.T, dir string) string {
	t.Helper()
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return project.ProposalHash(p.Lock.Bounds, p.Lock.Statements)
}

// editLock changes what the lock in dir says its invariant means, as an
// edit after its ratification would.
func editLock(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, ".invariant", "ratified.lock")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), "x is always 0.", "x is always 0, or 1.", 1)
	if edited == string(b) {
		t.Fatal("the lock has no statement to edit")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A lock ratified by hand passes CI's ratification check only when the
// repository's journal holds the decision it names, a person ratified that
// decision on a ratify line of their own, and the line names exactly the
// lock's proposal (D-0204, D-0208).
func TestARatificationByHandNeedsADecisionAPersonRatified(t *testing.T) {
	t.Run("ratified by a person for the lock's proposal", func(t *testing.T) {
		dir, repo, store := handProject(t, "D-0001")
		decide(t, store, repo, "proposed", "agent")
		proposal := proposalOf(t, dir)
		if err := store.RatifyProposal(repo, "D-0001", "@gitdek", proposal); err != nil {
			t.Fatal(err)
		}
		code, out := ratificationPrints(t, "-repo", "gitdek/demo", dir)
		if code != 0 || !strings.Contains(out, "ratified by hand by @gitdek, as D-0001 records ("+proposal+")") {
			t.Fatalf("a decision a person ratified for the lock's proposal was refused: exit %d\n%s", code, out)
		}
	})

	t.Run("a proposed decision that would supersede it", func(t *testing.T) {
		dir, repo, store := handProject(t, "D-0001")
		decide(t, store, repo, "proposed", "agent")
		if err := store.RatifyProposal(repo, "D-0001", "@gitdek", proposalOf(t, dir)); err != nil {
			t.Fatal(err)
		}
		decide(t, store, repo, "proposed", "agent")
		if err := store.Link(repo, "D-0002", decisions.Edge{Type: decisions.Supersedes, To: "D-0001"}, "agent"); err != nil {
			t.Fatal(err)
		}
		if code, out := ratificationPrints(t, "-repo", "gitdek/demo", dir); code != 0 {
			t.Fatalf("a decision only proposed doesn't supersede anything yet, but the lock was refused: exit %d\n%s", code, out)
		}
	})

	for _, c := range []struct {
		name, lock string
		setup      func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store)
		want       []string
	}{
		{"missing from the journal", "D-0009",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
			},
			[]string{"D-0009", "isn't in the repository's journal"}},
		{"proposed", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
			},
			[]string{"D-0001", "is proposed"}},
		{"decided by an agent", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "decided", "agent")
			},
			[]string{"D-0001", "is decided"}},
		{"ratified by an agent", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				ratifyAsAgent(t, repo, "D-0001")
			},
			[]string{"D-0001", "only a person ratifies"}},
		// An agent may write down that a person ratified, and so may the
		// person, but neither is the person's ratification of a proposal.
		{"recorded as ratified by an agent", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "ratified", "@gitdek")
			},
			[]string{"D-0001", "was recorded as ratified by @gitdek", "no ratify line"}},
		{"recorded as ratified by the person", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decideBy(t, store, repo, "ratified", "@gitdek", "@gitdek")
			},
			[]string{"D-0001", "was recorded as ratified by @gitdek", "no ratify line"}},
		{"ratified by a person for no proposal", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				if err := store.Ratify(repo, "D-0001", "@gitdek"); err != nil {
					t.Fatal(err)
				}
			},
			[]string{"D-0001", "no proposal in particular"}},
		{"edited after a person ratified it", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				if err := store.RatifyProposal(repo, "D-0001", "@gitdek", proposalOf(t, dir)); err != nil {
					t.Fatal(err)
				}
				editLock(t, dir)
			},
			[]string{"D-0001", "isn't what @gitdek ratified"}},
		{"an unrelated decision", "D-0002",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				decide(t, store, repo, "proposed", "agent")
				if err := store.RatifyProposal(repo, "D-0001", "@gitdek", proposalOf(t, dir)); err != nil {
					t.Fatal(err)
				}
				other := "sha256:" + strings.Repeat("ab", 32)
				if err := store.RatifyProposal(repo, "D-0002", "@gitdek", other); err != nil {
					t.Fatal(err)
				}
			},
			[]string{"D-0002", "isn't what @gitdek ratified"}},
		{"superseded", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				if err := store.RatifyProposal(repo, "D-0001", "@gitdek", proposalOf(t, dir)); err != nil {
					t.Fatal(err)
				}
				decide(t, store, repo, "proposed", "agent")
				if err := store.Supersede(repo, "D-0001", "D-0002", "agent"); err != nil {
					t.Fatal(err)
				}
			},
			[]string{"D-0001", "is superseded by"}},
		{"superseded by a later decision's edge", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				if err := store.RatifyProposal(repo, "D-0001", "@gitdek", proposalOf(t, dir)); err != nil {
					t.Fatal(err)
				}
				decide(t, store, repo, "decided", "agent")
				if err := store.Link(repo, "D-0002", decisions.Edge{Type: decisions.Supersedes, To: "D-0001"}, "agent"); err != nil {
					t.Fatal(err)
				}
			},
			[]string{"D-0001", "is superseded by", "D-0002"}},
		{"ratified by a bot", "D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				ratifyAs(t, repo, "D-0001", "@invariant-code-factory[bot]")
			},
			[]string{"D-0001", "only a person ratifies"}},
		{"named nowhere", "",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {},
			[]string{"no ratification record"}},
		{"another project's", "copythis-ad/D-0001",
			func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "ratified", "@gitdek")
			},
			[]string{"copythis-ad/D-0001", "repository's own decision IDs"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, repo, store := handProject(t, c.lock)
			c.setup(t, dir, repo, store)
			code, out := ratificationPrints(t, "-repo", "gitdek/demo", dir)
			if code != 1 {
				t.Fatalf("exit %d, want 1, for a lock whose decision is %s:\n%s", code, c.name, out)
			}
			for _, want := range c.want {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal doesn't say %q:\n%s", want, out)
				}
			}
		})
	}
}

// The repository's own locks ratified by hand, factory/protocol's and
// factory/recovery's since #271, pass the check as they are: each names a
// decision @gitdek ratified for exactly its proposal (D-0208).
func TestTheRepositorysLocksRatifiedByHandHold(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"factory/protocol", "factory/recovery"} {
		p, err := project.Load(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		if p.Lock.Ratified != nil || p.Lock.Decision == "" {
			t.Fatalf("%s's lock isn't ratified by hand", dir)
		}
		code, out := ratificationPrints(t, "-repo", "gitdek/invariant", filepath.Join(root, dir))
		want := "ratified by hand by @gitdek, as " + p.Lock.Decision + " records (" + project.ProposalHash(p.Lock.Bounds, p.Lock.Statements) + ")"
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%s: exit %d, want 0 and %q:\n%s", dir, code, want, out)
		}
	}
}

// ratifyAsAgent appends a ratification by a coding agent to a decision's
// journal, sealed and chained as the store seals a line, which the store
// itself refuses to write.
func ratifyAsAgent(t *testing.T, repo decisions.Repo, id string) {
	t.Helper()
	ratifyAs(t, repo, id, "agent")
}

// ratifyAs appends a ratification by by to a decision's journal, sealed and
// chained as the store seals a line, whatever the store would refuse.
func ratifyAs(t *testing.T, repo decisions.Repo, id, by string) {
	t.Helper()
	path := filepath.Join(repo.JournalDir(), id+".jsonl")
	events, err := decisions.ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	e := decisions.Event{At: time.Now().UTC().Format(time.RFC3339), By: by, Op: decisions.OpRatify, ID: id,
		Prev: events[len(events)-1].Hash}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	e.Hash = "sha256:" + hex.EncodeToString(sum[:])
	if b, err = json.Marshal(e); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	if _, err := decisions.ReadJournal(path); err != nil {
		t.Fatalf("the agent's ratification isn't sealed as the store seals a line: %v", err)
	}
}

// pin says what a lock ratified by hand needs once its statements are
// pinned: nothing, when its decision ratifies exactly its proposal; that
// decision's ratification, when it waits for one; or a new decision, when
// it names none, or the one it names is ratified already, since a decision
// is ratified once (D-0208).
func TestPinSaysWhatAHandLockNeeds(t *testing.T) {
	for _, c := range []struct {
		name, lock string
		setup      func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store)
		want, not  string
	}{
		{"ratified for its proposal", "D-0001", func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
			decide(t, store, repo, "proposed", "agent")
			if err := store.RatifyProposal(repo, "D-0001", "@gitdek", proposalOf(t, dir)); err != nil {
				t.Fatal(err)
			}
		}, "which D-0001 ratifies", "invariant decisions ratify"},
		{"waiting for its ratification", "D-0001", func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
			decide(t, store, repo, "proposed", "agent")
		}, "-proposal " + "PROPOSAL D-0001`", "new decision"},
		{"ratified for another proposal", "D-0001", func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {
			decide(t, store, repo, "proposed", "agent")
			if err := store.RatifyProposal(repo, "D-0001", "@gitdek", "sha256:"+strings.Repeat("ab", 32)); err != nil {
				t.Fatal(err)
			}
		}, "D-0001 is ratified already", "PROPOSAL D-0001`"},
		{"naming no decision", "", func(t *testing.T, dir string, repo decisions.Repo, store *decisions.Store) {}, "names no decision", "PROPOSAL D-0001`"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, repo, store := handProject(t, c.lock)
			c.setup(t, dir, repo, store)
			proposal := proposalOf(t, dir)
			got := pinHint(t.Context(), dir, c.lock, proposal)
			want, not := strings.ReplaceAll(c.want, "PROPOSAL", proposal), strings.ReplaceAll(c.not, "PROPOSAL", proposal)
			if !strings.Contains(got, proposal) || !strings.Contains(got, want) || strings.Contains(got, not) {
				t.Errorf("pin says %q; want it to say %q, and not %q", got, want, not)
			}
		})
	}
}
