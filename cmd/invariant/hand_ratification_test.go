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
	id, err := store.Decide(repo, decisions.NewDecision{Door: "two-way", Status: status, Who: who,
		Text: "The demo's statements are what a person ratified."}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A lock ratified by hand passes CI's ratification check only when the
// repository's journal holds the decision it names and a person ratified
// that decision (D-0204).
func TestARatificationByHandNeedsADecisionAPersonRatified(t *testing.T) {
	t.Run("ratified by a person", func(t *testing.T) {
		dir, repo, store := handProject(t, "D-0001")
		decide(t, store, repo, "proposed", "agent")
		if err := store.Ratify(repo, "D-0001", "@gitdek"); err != nil {
			t.Fatal(err)
		}
		code, out := ratificationPrints(t, "-repo", "gitdek/demo", dir)
		if code != 0 || !strings.Contains(out, "ratified by hand, recorded in D-0001") {
			t.Fatalf("a decision a person ratified was refused: exit %d\n%s", code, out)
		}
	})
	t.Run("ratified by a person when it was recorded", func(t *testing.T) {
		dir, repo, store := handProject(t, "D-0001")
		decide(t, store, repo, "ratified", "@gitdek")
		if code, out := ratificationPrints(t, "-repo", "gitdek/demo", dir); code != 0 {
			t.Fatalf("a decision recorded as ratified by a person was refused: exit %d\n%s", code, out)
		}
	})

	for _, c := range []struct {
		name, lock string
		setup      func(t *testing.T, repo decisions.Repo, store *decisions.Store)
		want       []string
	}{
		{"missing from the journal", "D-0009",
			func(t *testing.T, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
			},
			[]string{"D-0009", "isn't in the repository's journal"}},
		{"proposed", "D-0001",
			func(t *testing.T, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
			},
			[]string{"D-0001", "is proposed"}},
		{"decided by an agent", "D-0001",
			func(t *testing.T, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "decided", "agent")
			},
			[]string{"D-0001", "is decided"}},
		{"ratified by an agent", "D-0001",
			func(t *testing.T, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "proposed", "agent")
				ratifyAsAgent(t, repo, "D-0001")
			},
			[]string{"D-0001", "only a person ratifies"}},
		{"named nowhere", "",
			func(t *testing.T, repo decisions.Repo, store *decisions.Store) {},
			[]string{"no ratification record"}},
		{"another project's", "copythis-ad/D-0001",
			func(t *testing.T, repo decisions.Repo, store *decisions.Store) {
				decide(t, store, repo, "ratified", "@gitdek")
			},
			[]string{"copythis-ad/D-0001", "repository's own decision IDs"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, repo, store := handProject(t, c.lock)
			c.setup(t, repo, store)
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

// ratifyAsAgent appends a ratification by a coding agent to a decision's
// journal, sealed and chained as the store seals a line, which the store
// itself refuses to write.
func ratifyAsAgent(t *testing.T, repo decisions.Repo, id string) {
	t.Helper()
	path := filepath.Join(repo.JournalDir(), id+".jsonl")
	events, err := decisions.ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	e := decisions.Event{At: time.Now().UTC().Format(time.RFC3339), By: "agent", Op: decisions.OpRatify, ID: id,
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
