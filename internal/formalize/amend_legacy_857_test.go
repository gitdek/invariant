package formalize

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// legacy857 is a project ratified before bugs were classified: the shared
// fixture's lock, without its bug's classification.
func legacy857(t *testing.T) *Current {
	t.Helper()
	c := current(t)
	c.Lock.Statements = append([]project.Statement(nil), c.Lock.Statements...)
	bugs := 0
	for i := range c.Lock.Statements {
		if c.Lock.Statements[i].Kind == project.Bug {
			c.Lock.Statements[i].CodeBehavior = ""
			bugs++
		}
	}
	if bugs == 0 {
		t.Fatal("the fixture has no bug")
	}
	return c
}

// seeded857 is an amendment's workspace as the factory seeds it from c,
// with the fixture's view, and each named file edited.
func seeded857(t *testing.T, c *Current, edits map[string]func(string) string) string {
	t.Helper()
	ws := t.TempDir()
	if err := seedAmendment(ws, c); err != nil {
		t.Fatal(err)
	}
	for name, edit := range edits {
		b, err := os.ReadFile(filepath.Join(ws, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, name), []byte(edit(string(b))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// changed857 is what an amendment changes, as its comparison lists it.
func changed857(c *Current, p *Proposal) []string {
	var out []string
	for _, ch := range Diff(c, p).Statements {
		if ch.How != "unchanged" {
			out = append(out, ch.Name+" "+ch.How)
		}
	}
	return out
}

// A project ratified before bugs were classified can still be amended
// (#857). An amendment keeps a bug it carries exactly as the lock has it,
// unclassified, and classifies each bug it adds or changes. Requiring every
// bug of every draft to be classified refused the factory's own seed of
// such a lock, and the prompt forbids inferring a classification, so none
// of the projects ratified before the rule could be amended at all.
func TestAnAmendmentKeepsALegacyBugAsItIs857(t *testing.T) {
	c := legacy857(t)
	lock := c.Lock.Statements

	// The seed, untouched, is exactly what's ratified, and changes nothing.
	ws := seeded857(t, c, nil)
	if _, err := Read(ws); err == nil || !strings.Contains(err.Error(), "a new proposal must classify each bug") {
		t.Fatalf("a draft that amends nothing kept an unclassified bug: %v", err)
	}
	p, err := ReadAmending(ws, lock)
	if err != nil {
		t.Fatalf("the factory's seed of a lock ratified before the rule is refused: %v", err)
	}
	if p.Hash != project.ProposalHash(c.Lock.Bounds, lock) {
		t.Error("an untouched amendment's draft isn't exactly what's ratified")
	}
	if err := p.Amend(c); err != nil {
		t.Fatal(err)
	}
	if got := changed857(c, p); len(got) != 0 {
		t.Errorf("an untouched amendment changes %v", got)
	}
	// Once it's an amendment it carries its baseline, and pins again alone.
	hash := p.Hash
	if err := p.Pin(); err != nil || p.Hash != hash {
		t.Errorf("an amendment pinned again: %v, hash %s, was %s", err, p.Hash, hash)
	}
	// The check the drafting agent runs is given the lock by its caller.
	authority := (Request{Repo: "o/r", Issue: 5, Title: "t", Current: c}).CheckContext()
	if got := authority.Amended; !reflect.DeepEqual(got, lock) {
		t.Errorf("the check is given %+v, want the lock's statements", got)
	}
	// The agent's checker is another process: it fetches the context the
	// caller captured, and holds the same draft to the same rule.
	capture, release, err := stageCheckContext(t.TempDir(), authority)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	reference, closeContext, err := serveCheckContext(t.Context(), capture)
	if err != nil {
		t.Fatal(err)
	}
	defer closeContext()
	fetched, err := LoadCheckContext(t.Context(), reference, 0)
	if err != nil || !reflect.DeepEqual(fetched.Amended, lock) {
		t.Fatalf("the agent's checker is given %+v, %v; want the lock's statements", fetched.Amended, err)
	}
	if _, err := ReadAmending(ws, fetched.Amended); err != nil {
		t.Errorf("the agent's checker refuses the factory's seed of a lock ratified before the rule: %v", err)
	}
	if got := (Request{Repo: "o/r", Issue: 5, Title: "t"}).CheckContext().Amended; got != nil {
		t.Errorf("a new project's check is given %+v", got)
	}

	second := "\nDropWhenFull == Len(buf) = Cap /\\ buf' = Tail(buf)\n\n===="
	added := func(classification string) map[string]func(string) string {
		return map[string]func(string) string{
			"BoundedBuffer.tla": func(s string) string { return strings.Replace(s, "\n====", second, 1) },
			"proposal.json": func(s string) string {
				row := `{"name": "DropWhenFull", "kind": "bug", "says": "A full buffer drops its oldest message.", "expect": "TypeOK"` + classification + `}`
				i := strings.LastIndex(s, "]")
				return s[:i] + "," + row + s[i:]
			},
		}
	}
	for _, tc := range []struct {
		name  string
		edits map[string]func(string) string
		// refused names the statement the amendment must classify.
		refused string
		changed []string
	}{
		{"a bug it adds, classified", added(`, "code_behavior": "hypothetical"`), "", []string{"DropWhenFull added"}},
		{"a bug it adds, unclassified", added(""), "DropWhenFull", nil},
		{"the carried bug, classified", map[string]func(string) string{"proposal.json": func(s string) string {
			return strings.Replace(s, `"expect": "WithinCap"`, `"expect": "WithinCap", "code_behavior": "current"`, 1)
		}}, "", []string{"PutWhenFull changed"}},
		{"the carried bug with other words", map[string]func(string) string{"proposal.json": func(s string) string {
			return strings.Replace(s, "A producer adds a message to a full buffer.", "A producer overfills the buffer.", 1)
		}}, "PutWhenFull", nil},
		{"the carried bug with another definition", map[string]func(string) string{"BoundedBuffer.tla": func(s string) string {
			return strings.Replace(s, "PutWhenFull == \\E m \\in Msgs : buf' = Append(buf, m)", "PutWhenFull == \\E m \\in Msgs : buf' = Append(Append(buf, m), m)", 1)
		}}, "PutWhenFull", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ReadAmending(seeded857(t, c, tc.edits), lock)
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), "statement "+tc.refused+": an amendment must classify each bug it adds or changes") {
					t.Fatalf("want %s refused until it's classified, got %v", tc.refused, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Amend(c); err != nil {
				t.Fatal(err)
			}
			if got := changed857(c, p); !reflect.DeepEqual(got, tc.changed) {
				t.Errorf("the amendment's changes are %v, want %v", got, tc.changed)
			}
		})
	}
	if text := Prompt(Request{Repo: "o/r", Issue: 5, Title: "t", Current: c}, 4); !strings.Contains(text, "leave such a bug exactly as the draft you were given has it") || !strings.Contains(text, "Classify every bug you add or change") {
		t.Error("the prompt doesn't say what an amendment does with a bug its lock doesn't classify")
	}
}
