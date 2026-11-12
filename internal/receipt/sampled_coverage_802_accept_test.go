package receipt_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// Coverage is an additive, derived JSON view, not new fingerprint input.
// Counts remain integers; fraction and percent carry exact display text even
// to JavaScript consumers whose Number cannot represent every int64.
type coverage802Record struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Check       string `json:"check"`
	Invocation  string `json:"invocation"`
	Mode        string `json:"mode"`
	States      int64  `json:"states"`
	ModelStates int64  `json:"model_states"`
	Checked     bool   `json:"checked"`
	Sampled     bool   `json:"sampled"`
	Status      string `json:"status"`
	Fraction    string `json:"fraction"`
	Percent     string `json:"percent"`
}

func coverage802JSON(t *testing.T, r verify.Report) []coverage802Record {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Coverage []coverage802Record `json:"coverage"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v.Coverage
}

func coverage802Report(states int, total int64) verify.Report {
	return verify.Report{Project: "sampled counter", Passed: true, Assurance: "proved", Bounds: map[string]string{"Limit": "4"},
		Design: verify.Design{Passed: true, Outcome: "passed", DistinctStates: total}, Build: verify.Build{Passed: true},
		Conformance: &conformance.Result{Passed: true, Runs: 100, Steps: 20000, States: states, ModelStates: total, Transitions: 1}}
}

var coverage802Tags = regexp.MustCompile(`<[^>]*>`)
var coverage802NearPass = regexp.MustCompile(`(?i)(pass(?:ed)?|tested)[\s*·:()—.,-]*sampled|sampled[\s*·:()—.,-]*(pass(?:ed)?|tested)`)

func coverage802Visible(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(coverage802Tags.ReplaceAllString(s, ""))), " ")
}

func coverage802Line(t *testing.T, md, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, prefix) {
			return coverage802Visible(line)
		}
	}
	t.Fatalf("missing %q in receipt:\n%s", prefix, md)
	return ""
}

func coverage802CheckRecord(t *testing.T, got coverage802Record, states, total int64, status, fraction, percent string, checked, sampled bool) {
	t.Helper()
	if got.States != states || got.ModelStates != total || got.Status != status || got.Fraction != fraction || got.Percent != percent || got.Checked != checked || got.Sampled != sampled {
		t.Errorf("coverage %+v; want %d/%d, status=%s fraction=%q percent=%q checked=%v sampled=%v", got, states, total, status, fraction, percent, checked, sampled)
	}
}

func TestPartialSampleReceiptAndOldJSON802(t *testing.T) {
	// This pre-extension report deliberately carries an obsolete assurance.
	raw := `{"project":"sampled counter","passed":true,"assurance":"proved","bounds":{"Limit":"4"},"design":{"passed":true,"outcome":"passed","distinct_states":5},"build":{"passed":true},"conformance":{"passed":true,"runs":100,"steps":20000,"states":2,"model_states":5,"transitions":1}}`
	var r verify.Report
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	// For this fixture, the old fingerprint is precisely the raw Report's
	// JSON hash: all excluded operational fields are empty already.
	type originalReport verify.Report
	original, err := json.Marshal(originalReport(r))
	if err != nil {
		t.Fatal(err)
	}
	wantFP := fmt.Sprintf("sha256:%x", sha256.Sum256(original))
	if got := verify.Fingerprint(r); got != wantFP {
		t.Errorf("display metadata changed archived evidence identity: %s, want %s", got, wantFP)
	}
	r.Fingerprint = wantFP
	before, _ := json.Marshal(r)
	md := receipt.Markdown(&r)
	head := coverage802Line(t, md, "**✅")
	row := coverage802Line(t, md, "| Code · conformance |")
	for name, text := range map[string]string{"headline": head, "conformance row": row} {
		if !coverage802NearPass.MatchString(text) {
			t.Errorf("%s hides sampled qualification: %s", name, text)
		}
	}
	if !strings.Contains(row, "2/5") || !strings.Contains(row, "40%") {
		t.Errorf("conformance row lacks exact fraction and percentage: %s", row)
	}
	if strings.Contains(head, "**proved") {
		t.Errorf("stale assurance became a proof: %s", head)
	}
	rows := coverage802JSON(t, r)
	if len(rows) != 1 {
		t.Errorf("report has %d coverage records, want one", len(rows))
	} else {
		if rows[0].Source != "conformance" || rows[0].Mode != "sampled" {
			t.Errorf("wrong source/mode: %+v", rows[0])
		}
		coverage802CheckRecord(t, rows[0], 2, 5, "partial", "2/5", "40%", true, true)
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) || r.Fingerprint != wantFP || verify.Fingerprint(r) != wantFP {
		t.Fatal("rendering mutated the report or its fingerprint")
	}
	var round verify.Report
	if err := json.Unmarshal(after, &round); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, round) || verify.Fingerprint(round) != wantFP || receipt.Markdown(&round) != md {
		t.Fatal("JSON round trip changed evidence, identity or rendering")
	}
	// Consumers may return an obsolete or forged derived view. It is never
	// authority for either the next JSON response or the human receipt.
	forged := strings.TrimSuffix(raw, "}") + `,"coverage":[{"source":"conformance","checked":true,"sampled":false,"status":"full","states":5,"model_states":5,"fraction":"5/5","percent":"100%"}]}`
	var stale verify.Report
	if err := json.Unmarshal([]byte(forged), &stale); err != nil {
		t.Fatal(err)
	}
	stale.Fingerprint = wantFP
	if receipt.Markdown(&stale) != md || verify.Fingerprint(stale) != wantFP {
		t.Error("serialized display claims changed checked evidence or its rendering")
	}
	if cs := coverage802JSON(t, stale); len(cs) == 1 {
		coverage802CheckRecord(t, cs[0], 2, 5, "partial", "2/5", "40%", true, true)
	} else {
		t.Error("old JSON did not derive its actual coverage")
	}
	changed := round
	c := *round.Conformance
	c.States = 3
	changed.Conformance = &c
	if verify.Fingerprint(changed) == wantFP {
		t.Fatal("checked reached-state count was excluded from identity")
	}
	c.States, c.ModelStates = 2, 6
	if verify.Fingerprint(changed) == wantFP {
		t.Fatal("checked model-state count was excluded from identity")
	}
}

func TestCoverageBoundariesAndIndependentProof802(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		states                    int
		total                     int64
		status, fraction, percent string
	}{
		{"almost full", 999999, 1000000, "partial", "999999/1000000", "99.99%"},
		{"above JavaScript exact range", 9007199254740992, 9007199254740993, "partial", "9007199254740992/9007199254740993", "99.99%"},
		{"near int64 limit", 9223372036854775806, 9223372036854775807, "partial", "9223372036854775806/9223372036854775807", "99.99%"},
		{"full sampled run", 5, 5, "full", "5/5", "100%"},
		{"zero total", 2, 0, "unknown", "", ""},
		{"negative total", 2, -1, "unknown", "", ""},
		{"more visited than model", 6, 5, "unknown", "", ""},
		{"no reached-state evidence", 0, 5, "unknown", "", ""},
		{"negative reached count", -1, 5, "unknown", "", ""},
	} {
		r := coverage802Report(tc.states, tc.total)
		rows := coverage802JSON(t, r)
		if len(rows) != 1 {
			t.Errorf("%s: missing coverage record", tc.name)
		} else {
			coverage802CheckRecord(t, rows[0], int64(tc.states), tc.total, tc.status, tc.fraction, tc.percent, true, tc.status != "full")
		}
		md := receipt.Markdown(&r)
		row := coverage802Line(t, md, "| Code · conformance |")
		if tc.status == "unknown" {
			if !strings.Contains(strings.ToLower(row), "coverage unknown") || strings.Contains(row, "%") || !coverage802NearPass.MatchString(row) {
				t.Errorf("%s invents known/full coverage: %s", tc.name, row)
			}
		} else if !strings.Contains(row, tc.fraction) || !strings.Contains(row, tc.percent) {
			t.Errorf("%s hides the checked fraction: %s", tc.name, row)
		}
		if strings.Contains(row, "every reachable state") || strings.Contains(row, "every step tried") || strings.Contains(row, "proved") {
			t.Errorf("%s promoted sampling: %s", tc.name, row)
		}
	}
	// A second, conflicting positive model total cannot silently win; neither
	// can TLC's design total fill in a missing per-check denominator.
	for _, total := range []int64{0, 5} {
		r := coverage802Report(2, total)
		r.Design.DistinctStates = 7
		row := coverage802Line(t, receipt.Markdown(&r), "| Code · conformance |")
		if !strings.Contains(strings.ToLower(row), "coverage unknown") || strings.Contains(row, "%") {
			t.Errorf("conflicting/missing total fabricated a ratio: %s", row)
		}
	}
	r := coverage802Report(2, 5)
	r.Code = &verify.Code{Passed: true, Verifier: "Nagini", Functions: []string{"step"}, Contracts: []string{"step"}}
	r.Larger = &verify.Larger{Passed: true, Bounds: map[string]string{"Limit": "5"}}
	md := receipt.Markdown(&r)
	if !strings.Contains(md, "The code is **proved at every size**.") || !r.EverySize() {
		t.Error("sampled coverage erased an independent successful proof")
	}
	row := coverage802Line(t, md, "| Code · conformance |")
	if !coverage802NearPass.MatchString(row) || !strings.Contains(row, "40%") {
		t.Errorf("proof hid partial conformance: %s", row)
	}
	// Checked transitions survive a failed attempt check, while the gate and
	// every-state/every-step claims still fail.
	r = coverage802Report(2, 5)
	r.Passed, r.Conformance.Exhaustive = false, true
	r.Conformance.Tried = &conformance.Tried{Passed: false, Attempts: 2, States: 2, Message: "a step was never tried"}
	md = receipt.Markdown(&r)
	if !strings.Contains(md, "**❌ Fail.") || !strings.Contains(md, "| Every step tried | ❌") {
		t.Error("coverage changed overall or attempted-step failure")
	}
	if row = coverage802Line(t, md, "| Code · conformance |"); !coverage802NearPass.MatchString(row) || strings.Contains(row, "every reachable state") {
		t.Errorf("failed attempt check promoted state coverage: %s", row)
	}
	r.Conformance.Passed = false
	r.Conformance.BadStep = &conformance.Step{From: "n=0", To: "n=9"}
	md = receipt.Markdown(&r)
	row = coverage802Line(t, md, "| Code · conformance |")
	if !strings.Contains(row, "❌") || strings.Contains(row, "✅") || strings.Contains(row, "40%") {
		t.Errorf("failed conformance supplied successful coverage: %s", row)
	}
	rows := coverage802JSON(t, r)
	if len(rows) != 1 {
		t.Error("failed evidence lost its coverage record")
	} else {
		coverage802CheckRecord(t, rows[0], 2, 5, "unknown", "", "", false, false)
	}
}

func TestPythonAndComponentCoverageStayPerInvocation802(t *testing.T) {
	r := coverage802Report(2, 5)
	r.Conformance = nil
	r.Application = &verify.PythonApplicationReport{}
	for _, id := range []string{"seed-a", "seed-b"} {
		key := verify.PythonApplicationInvocationKey{Target: "existing", Check: "drive", Kind: "conformance", ID: id}
		r.Application.Conformance = append(r.Application.Conformance, verify.PythonApplicationConformance{Key: key,
			Invocation: project.ResolvedComponentInvocation{ID: id, Kind: "conformance", Check: "drive"}, Bounds: r.Bounds,
			Result: conformance.Result{Passed: true, Runs: 100, Steps: 20000, States: 2, ModelStates: 5}})
	}
	// The larger run has its own bounds and must not enter the sampled share.
	r.Application.Larger = []verify.PythonApplicationLarger{{Key: r.Application.Conformance[0].Key, Result: verify.Larger{Passed: true, States: 50, WantStates: 50}}}
	r.Fingerprint = verify.Fingerprint(r)
	appBefore, _ := json.Marshal(r)
	md := receipt.Markdown(&r)
	rows := coverage802JSON(t, r)
	if len(rows) != 2 {
		t.Errorf("Python invocations became %d coverage records, want two", len(rows))
	}
	for _, row := range rows {
		if row.Source != "python_application" || row.Target != "existing" || row.Check != "drive" || row.Invocation != "seed-a" && row.Invocation != "seed-b" {
			t.Errorf("Python coverage lost identity: %+v", row)
		}
		coverage802CheckRecord(t, row, 2, 5, "partial", "2/5", "40%", true, true)
	}
	for _, id := range []string{"seed-a", "seed-b"} {
		found := false
		for _, line := range strings.Split(md, "\n") {
			line = coverage802Visible(line)
			if strings.Contains(line, id) && strings.Contains(line, "2/5") && strings.Contains(line, "40%") && coverage802NearPass.MatchString(line) {
				found = true
			}
		}
		if !found {
			t.Errorf("Python invocation %q has no human-readable sampled result with its own fraction", id)
		}
	}
	if strings.Contains(md, "4/10") || strings.Contains(md, "54/60") {
		t.Error("Python runs or bounds were added together")
	}
	appAfter, _ := json.Marshal(r)
	if !bytes.Equal(appBefore, appAfter) || verify.Fingerprint(r) != r.Fingerprint {
		t.Error("rendering changed Python invocation evidence")
	}
	var appRound verify.Report
	if err := json.Unmarshal(appAfter, &appRound); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(appRound) != r.Fingerprint {
		t.Error("Python JSON round trip changed evidence identity")
	}
	appRound.Application.Conformance[0].Result.States++
	if verify.Fingerprint(appRound) == r.Fingerprint {
		t.Error("Python checked counts were excluded from evidence identity")
	}
	// Use the existing attribution fixture, including its real inventory and
	// provenance validation, then ask for different model denominators.
	f := evidence296Fixture(t)
	f.Seed.Conformance, f.Seed.Agreement, f.Seed.Code, f.Seed.Larger = nil, nil, nil, nil
	f.Seed.Passed = true
	f.Proofs = nil
	f.Conformance[0].Mode, f.Conformance[0].States, f.Conformance[0].ModelStates, f.Conformance[0].Attempts = "sampled", 2, 5, nil
	f.Conformance[1].Mode, f.Conformance[1].States, f.Conformance[1].ModelStates = "sampled", 3, 11
	r = f.report(t)
	for i := range r.Components.Conformance { // Cached flags must not win.
		r.Components.Conformance[i].ExhaustiveStates = true
		r.Components.Conformance[i].Status = "tested in every state"
	}
	r.Fingerprint = verify.Fingerprint(r)
	before, _ := json.Marshal(r)
	md = receipt.Markdown(&r)
	rows = coverage802JSON(t, r)
	if len(rows) != 2 {
		t.Errorf("component checks became %d coverage records, want two", len(rows))
	}
	for _, row := range rows {
		if row.Source != "component" || row.Invocation != "conformance-invocation" {
			t.Errorf("component identity lost: %+v", row)
		}
		var supplied *verify.ComponentConformanceResult
		for i := range r.Components.Conformance {
			c := &r.Components.Conformance[i].Result
			if c.Target == row.Target && c.Check == row.Check {
				supplied = c
			}
		}
		if supplied == nil {
			t.Errorf("coverage has no matching check: %+v", row)
			continue
		}
		percent := "40%"
		if supplied.ModelStates == 11 {
			percent = "27.27%"
		}
		coverage802CheckRecord(t, row, supplied.States, supplied.ModelStates, "partial", fmt.Sprintf("%d/%d", supplied.States, supplied.ModelStates), percent, true, true)
		cells := evidence296Record(t, evidence296Section(t, md, "conformance"), evidence296Checks, row.Target, row.Check)
		if !coverage802NearPass.MatchString(coverage802Visible(cells[2])) || !strings.Contains(coverage802Visible(cells[3]), row.Fraction) || !strings.Contains(coverage802Visible(cells[3]), percent) {
			t.Errorf("component row hides sampled coverage: %v", cells)
		}
	}
	if strings.Contains(md, "5/16") || strings.Contains(md, "State coverage: tested in every state") {
		t.Error("unrelated models or stale flags manufactured coverage")
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) || verify.Fingerprint(r) != r.Fingerprint {
		t.Fatal("rendering changed component evidence")
	}
	var round verify.Report
	if err := json.Unmarshal(after, &round); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(round) != r.Fingerprint {
		t.Fatal("component JSON round trip changed evidence identity")
	}
	round.Components.Conformance[0].Result.States++
	if verify.Fingerprint(round) == r.Fingerprint {
		t.Fatal("component coverage counts did not bind evidence identity")
	}
}
