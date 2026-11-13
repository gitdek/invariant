package formalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func draft857(t *testing.T, classification string) string {
	t.Helper()
	ws := workspace(t, nil)
	b, err := os.ReadFile(filepath.Join(ws, "proposal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	for _, v := range d["statements"].([]any) {
		s := v.(map[string]any)
		if s["kind"] == "bug" {
			delete(s, "code_behavior")
			if classification != "" {
				s["code_behavior"] = classification
			}
		}
	}
	b, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(ws, "proposal.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestNewProposalsMustClassifyBugs857(t *testing.T) {
	for _, classification := range []string{"", "current", "hypothetical", "plausible"} {
		p, err := Read(draft857(t, classification))
		valid := classification == "current" || classification == "hypothetical"
		if valid && err == nil {
			for _, s := range p.Statements {
				if s.Kind == project.Bug && s.CodeBehavior != classification {
					t.Fatalf("draft lost classification: %+v", s)
				}
			}
		}
		if (err == nil) != valid {
			t.Errorf("new proposal classification %q: proposal=%v err=%v", classification, p, err)
		}
		if !valid && err != nil && !strings.Contains(err.Error(), "code_behavior") {
			t.Errorf("missing named classification diagnostic: %v", err)
		}
	}
	prompt := Prompt(Request{Repo: "owner/repo", Issue: 857, Title: "Fix an existing overflow", Body: "The code overfills the buffer."}, 4)
	for _, word := range []string{"code_behavior", "current", "hypothetical", "unchanged code", "tests"} {
		if !strings.Contains(prompt, word) {
			t.Errorf("formalization prompt omits %q", word)
		}
	}
}

func TestClassificationChangesAreVisibleStatementAmendments857(t *testing.T) {
	p, err := Read(draft857(t, "current"))
	if err != nil {
		t.Fatal(err)
	}
	c := &Current{ModuleText: p.ModuleText, Includes: p.Includes, Lock: project.Lock{Bounds: p.Bounds, Statements: append([]project.Statement(nil), p.Statements...)}}
	for _, classification := range []string{"hypothetical", ""} {
		// Removal is diffable even though a new unclassified draft is refused.
		q := *p
		b, _ := json.Marshal(p.Statements)
		var rows []map[string]any
		if err = json.Unmarshal(b, &rows); err != nil {
			t.Fatal(err)
		}
		for _, s := range rows {
			if s["kind"] == "bug" {
				delete(s, "code_behavior")
				if classification != "" {
					s["code_behavior"] = classification
				}
			}
		}
		b, _ = json.Marshal(rows)
		q.Statements = nil
		if err = json.Unmarshal(b, &q.Statements); err != nil {
			t.Fatal(err)
		}
		for _, s := range q.Statements {
			if s.Kind == project.Bug && s.CodeBehavior != classification {
				t.Fatalf("amendment did not retain classification %q: %+v", classification, s)
			}
		}
		d := Diff(c, &q)
		changed := d.Of(Changed)
		if len(changed) != 1 || changed[0].Name != "PutWhenFull" || changed[0].OwnTextSame() {
			t.Errorf("classification %q is hidden as an unchanged/helper-only statement: %+v", classification, d)
		}
		if project.ProposalHash(q.Bounds, q.Statements) == p.Hash {
			t.Errorf("classification %q keeps old ratification", classification)
		}
	}
}
