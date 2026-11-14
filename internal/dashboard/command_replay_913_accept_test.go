package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestDashboardShowsCommandReplayDiagnostics913(t *testing.T) {
	const diagnosis = "ReplayCompletedTask: step 3 ReplayTask(1, job) reproduced the forbidden state: deliveries = 2, phase = 3, reused = FALSE"
	statement := project.Statement{Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"}
	r := &verify.Report{Bugs: []verify.Bug{{Name: statement.Name, Label: "replay-completed-task", Caught: true, CodeBehavior: "current", Violated: "Safe", Steps: 4,
		Replay: &verify.BugReplayEvidence{Required: true, Status: "reproduced", Message: diagnosis}}}}
	found, missed := foundFor(statement, r)
	if !missed || !strings.Contains(found, "Caught") || !strings.Contains(found, diagnosis) {
		t.Errorf("evidence page lost model catch or replay diagnosis: %s, missed=%v", found, missed)
	}
	p := projectOf(projectSource{key: "delivery", manifest: project.Manifest{Language: "typescript"}}, r)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), diagnosis) {
		t.Errorf("dashboard API omits replay diagnostic: %s", raw)
	}
	// The web card renders this diagnostic as text, including escaped names
	// and state strings; retain the existing browser test's escaping checks.
	for _, c := range []struct {
		language    string
		existing    []string
		unavailable bool
	}{
		{"typescript", nil, false}, {"typescript", []string{"src/delivery.ts"}, false}, {"python", nil, false}, {"python", []string{"delivery.py"}, true},
	} {
		proposal := &formalize.Proposal{Draft: formalize.Draft{Name: "delivery", Language: c.language, Existing: c.existing, Statements: []project.Statement{statement}}}
		if c.language == "python" && len(c.existing) > 0 {
			proposal.Target = &formalize.Target{Manifest: project.Manifest{Language: c.language, Existing: c.existing}}
		}
		w := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: proposal}, time.Now())
		if w == nil || (len(w.Unreplayed) != 0) != c.unavailable {
			t.Errorf("card gives wrong availability for %s/%v: %+v", c.language, c.existing, w)
		}
	}
}
