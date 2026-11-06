package formalize

import (
	"encoding/json"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestComparisonAuthoritySurvivesQuestions(t *testing.T) {
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	p.Reader = &Reader{Chart: &statechart.Result{}}
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
}
