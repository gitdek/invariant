package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestDashboardDistinguishesBugDetectionFromReplay857(t *testing.T) {
	var r verify.Report
	if err := json.Unmarshal([]byte(`{"bugs":[{"name":"Old","caught":true,"violated":"Safe","steps":3},{"name":"Actual","code_behavior":"current","caught":true,"violated":"Safe","replay":{"required":true,"status":"replay-unavailable"}},{"name":"Possible","code_behavior":"hypothetical","caught":true,"violated":"Safe"}]}`), &r); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, label string
		missed      bool
	}{{"Old", "not replayed: not classified", false}, {"Actual", "replay-unavailable", true}, {"Possible", "hypothetical", false}} {
		found, missed := foundFor(project.Statement{Name: tc.name, Kind: project.Bug}, &r)
		if !strings.Contains(found, tc.label) || !strings.Contains(found, "Caught") || missed != tc.missed {
			t.Errorf("%s: %q missed=%v", tc.name, found, missed)
		}
	}
	p := projectOf(projectSource{}, &r)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"not replayed: not classified", "replay-unavailable", "hypothetical"} {
		if !strings.Contains(string(b), label) {
			t.Errorf("dashboard API omits %q: %s", label, b)
		}
	}
	// Classification is also visible before any receipt exists.
	var statement project.Statement
	if err = json.Unmarshal([]byte(`{"name":"Actual","kind":"bug","says":"Already present.","code_behavior":"current"}`), &statement); err != nil {
		t.Fatal(err)
	}
	if statement.CodeBehavior != "current" {
		t.Fatal("proposal lost its classification")
	}
	groups := rulesOf([]project.Statement{statement}, nil)
	b, _ = json.Marshal(groups)
	if !strings.Contains(string(b), "current") {
		t.Errorf("proposal rule hides classification: %s", b)
	}
}
