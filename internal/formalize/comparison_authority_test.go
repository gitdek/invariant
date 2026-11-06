package formalize

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestComparisonAuthoritySurvivesQuestions(t *testing.T) {
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	// A draft whose reader doesn't check out failed, and is never a selected
	// baseline: its revision starts afresh (#648).
	p.Reader = &Reader{Chart: &statechart.Result{}}
	if (Request{Previous: p}).CheckContext().Baseline != nil {
		t.Fatal("an unchecked draft became a comparison baseline")
	}
	s := p.comparisonSnapshot()
	s.Chart = &statechart.Result{}
	p.Reader, p.Baseline = nil, &s
	authority := (Request{Previous: p}).CheckContext()
	if authority.Baseline.Chart != nil || authority.Baseline.Limitation != nil {
		t.Fatal("check process authority carried a saved graph instead of just its sources")
	}
	p.Bounds["Cap"] = "99"
	p.Includes["Extra.tla"] = "replacement"
	if authority.Baseline.Lock.Bounds["Cap"] != "2" || len(authority.Baseline.Includes) != 0 {
		t.Fatal("caller baseline aliases previous proposal")
	}
	question := &Proposal{Draft: Draft{Forks: []Fork{{ID: "F1"}}}, Baseline: authority.Baseline}
	next := (Request{Previous: question}).CheckContext()
	if next.Baseline == nil || next.Baseline.Name != authority.Baseline.Name || next.Baseline.Model != authority.Baseline.Model {
		t.Fatal("question replaced the preceding selected source")
	}
	firstQuestion := (Request{Previous: &Proposal{Draft: question.Draft}}).CheckContext()
	if firstQuestion.Baseline != nil {
		t.Fatal("first question invented an empty baseline")
	}

	cur := current(t)
	cur.Reader = &Reader{Comparison: &statechart.Comparison{}}
	q, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Amend(cur); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var saved Proposal
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	cur.ModuleText += "changed after capture"
	captured := (Request{Previous: &saved, Current: cur}).CapturedCurrent()
	if captured.Reader != nil || captured.ModuleText == cur.ModuleText {
		t.Fatal("amendment copied history or moved its baseline")
	}
	// Without a current project the request changes none, whatever an
	// earlier draft amended.
	if (Request{Previous: &saved}).CapturedCurrent() != nil {
		t.Fatal("a request naming no project amended the earlier target")
	}
}

// An answered question passes on its baseline and target, never a draft to
// revise: nobody asked for a revision (#648).
func TestAnswersCarryTheBaselineWithoutARevision(t *testing.T) {
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	s := p.comparisonSnapshot()
	question := &Proposal{Draft: Draft{Forks: []Fork{{ID: "F1"}}}, Baseline: &s}
	carried := question.Carrier()
	if carried == nil || carried.drafted() {
		t.Fatal("the answer carried a draft")
	}
	req := Request{Previous: carried}
	if b := req.CheckContext().Baseline; b == nil || b.Name != s.Name {
		t.Fatal("the answer lost the selected baseline")
	}
	if strings.Contains(Prompt(req, 4), "People asked for a revision") {
		t.Fatal("an answer was drafted as a revision")
	}
	if !strings.Contains(Prompt(Request{Previous: question}, 4), "People asked for a revision") {
		t.Fatal("a revision of a question lost its revision prompt")
	}
	if (&Proposal{Draft: question.Draft}).Carrier() != nil {
		t.Fatal("a first question invented something to carry")
	}
}
