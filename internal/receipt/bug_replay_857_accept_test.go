package receipt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestBugReceiptSeparatesModelAndReplayEvidence857(t *testing.T) {
	const raw = `{"project":"mixed bugs","passed":false,"design":{"passed":true,"outcome":"passed"},"bugs":[{"name":"Old","label":"old","caught":true,"violated":"Safe","steps":2},{"name":"Possible","label":"possible","code_behavior":"hypothetical","caught":true,"violated":"Safe"},{"name":"Actual","label":"actual","code_behavior":"current","caught":true,"violated":"Safe","replay":{"required":true,"status":"replay-unavailable","message":"No execution adapter is available."}}],"code":{"verifier":"Gobra","passed":true,"functions":["KeepSafe"]}}`
	var r verify.Report
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	if r.Bugs[2].CodeBehavior != "current" {
		t.Fatal("receipt lost the ratified classification")
	}
	md := Markdown(&r)
	for _, s := range []string{"not replayed: not classified", "hypothetical", "replay-unavailable", "actual", "proved"} {
		if !strings.Contains(md, s) {
			t.Errorf("receipt omits %q:\n%s", s, md)
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Bugs []struct {
			Name         string
			ReplayStatus struct {
				Status, Label      string
				Required, Replayed bool
			} `json:"replay_status"`
		}
	}
	if err = json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Bugs) != 3 {
		t.Fatal(string(b))
	}
	for _, bug := range view.Bugs {
		if bug.ReplayStatus.Replayed {
			t.Errorf("%s counted as replayed without an execution", bug.Name)
		}
		if bug.Name == "Old" && (bug.ReplayStatus.Label != "not replayed: not classified" || bug.ReplayStatus.Required) {
			t.Errorf("legacy bug status: %+v", bug)
		}
		if bug.Name == "Actual" && (!bug.ReplayStatus.Required || bug.ReplayStatus.Status != "replay-unavailable") {
			t.Errorf("mandatory unmet replay hidden: %+v", bug)
		}
	}
	before := verify.Fingerprint(r)
	var round verify.Report
	if err = json.Unmarshal(b, &round); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(round) != before {
		t.Fatal("derived replay labels change evidence on JSON round trip")
	}
	if r.CodeAssurance() != "proved" {
		t.Fatal("unmet replay erases independent proof")
	}
}
