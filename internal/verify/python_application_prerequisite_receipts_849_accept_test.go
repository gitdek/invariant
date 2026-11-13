package verify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func skipReasons849(t *testing.T, value any) []string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Skipped
}

func TestPythonApplicationSkipReceipts849(t *testing.T) {
	verify.RequirePrerequisiteRecords849(t)
	for _, kind := range []string{"larger", "proof", "test caller"} {
		t.Run(kind, func(t *testing.T) {
			var r *verify.Report
			if kind == "larger" {
				var calls verify.ToolCounts849
				r, calls = verify.PrerequisiteLargerReport849(t, "execution", false)
				if calls.LargerTLC != 0 || calls.Larger["blocked"] != 0 {
					t.Errorf("skipped larger receipt came from actual work: %+v", calls)
				}
			} else {
				var calls int
				if kind == "test caller" {
					r, calls = verify.PrerequisiteTestCallerReport849(t, "direct")
				} else {
					r, calls = verify.PrerequisiteProofReport849(t, "contract")
				}
				if calls != 0 {
					t.Errorf("blocked proof started Nagini %d times", calls)
				}
			}
			if r.Passed {
				t.Fatal("fixture lost original failure")
			}
			var value any
			if kind == "larger" {
				if len(r.Application.Larger) != 1 {
					t.Fatal("missing omitted rerun record")
				}
				value = r.Application.Larger[0]
			} else {
				if len(r.Application.Proofs) != 1 {
					t.Fatal("missing unrun proof record")
				}
				value = r.Application.Proofs[0]
			}
			reasons := skipReasons849(t, value)
			if len(reasons) == 0 {
				t.Error("JSON has no explicit skipped reasons")
			}
			dir := t.TempDir()
			if err := receipt.Write(dir, r); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			var decoded verify.Report
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if verify.Fingerprint(decoded) != verify.Fingerprint(*r) || decoded.Passed {
				t.Error("skip or original failure did not survive receipt JSON")
			}
			if !reflect.DeepEqual(decoded.Coverage(), r.Coverage()) {
				t.Error("skip receipt changed the derived coverage view")
			}
			md := receipt.Markdown(&decoded)
			saved, err := os.ReadFile(filepath.Join(dir, "receipt.md"))
			if err != nil {
				t.Fatal(err)
			}
			if md != string(saved) {
				t.Error("receipt rendering changed after JSON round trip")
			}
			if !strings.Contains(strings.ToLower(md), "skipped") || !strings.Contains(md, "**What failed**") {
				t.Error("Markdown hides the skip or original gate failure")
			}
			if kind == "larger" {
				l := decoded.Application.Larger[0]
				if !slices.Equal(skipReasons849(t, l), reasons) {
					t.Error("larger skip reasons changed between the Go report and JSON")
				}
				if l.Execution != nil || l.Result.Required || l.Result.Passed {
					t.Error("omitted rerun became required work or evidence")
				}
				if strings.Contains(md, "Python execution one size larger,") {
					t.Error("skip created a second execution failure")
				}
				if !strings.Contains(md, "base exploration failed 849") {
					t.Error("original exploration failure disappeared")
				}
			} else {
				p := decoded.Application.Proofs[0]
				if !slices.Equal(skipReasons849(t, p), reasons) {
					t.Error("proof skip reasons changed between the Go report and JSON")
				}
				if p.Ran || p.Result.Passed || len(p.Result.Verified) != 0 || len(p.Command) != 0 || p.Image != "" {
					t.Error("decoded skip claims proof evidence")
				}
				prerequisite := "keyword.iskeyword"
				if kind == "test caller" {
					prerequisite = "Requires(requested == 'engine' or requested == 'boat')"
					checks := verify.PythonApplicationContractChecks(decoded.Components, decoded.Application, p.Result.Target)
					if checks == nil || checks.Tests != 1 || len(checks.Findings) != 1 || checks.Findings[0].Test != "tests.test_counter.CounterTests.test_inc" {
						t.Errorf("test call's finding did not survive receipt JSON: %+v", checks)
					}
					for _, want := range []string{"1 while tests ran", "tests/test&#95;counter.py", "during tests.test&#95;counter.CounterTests.test&#95;inc", "didn't meet " + prerequisite} {
						if !strings.Contains(md, want) {
							t.Errorf("skip receipt hides test call finding %q", want)
						}
					}
				}
				if !strings.Contains(md, prerequisite) || !strings.Contains(md, p.Input.Fingerprint[:19]) || !strings.Contains(md, "Component proofs: **0 proved;") {
					t.Error("Markdown omits skipped proof identity, prerequisite or lack of assurance")
				}
				for _, falseClaim := range []string{"verified by Nagini", "Nagini checked each selected function", "Nagini ran from its pinned recipe"} {
					if strings.Contains(md, falseClaim) {
						t.Errorf("unrun proof receipt claims %q", falseClaim)
					}
				}
				inv := decoded.Components.Inventory
				if err := decoded.AttributeComponentAssumptions(&inv, nil); err != nil {
					t.Fatal(err)
				}
				for _, row := range decoded.Components.Proofs {
					if row.Target == p.Result.Target && row.Status != "unchecked" {
						t.Error("reattribution promoted unrun proof")
					}
				}
				if len(skipReasons849(t, decoded.Application.Proofs[0])) == 0 {
					t.Error("reattribution discarded skip")
				}
			}
			// The skip itself, its causes and its input identity are portable
			// evidence, not volatile diagnostics to omit from the fingerprint.
			wire := calls793ReportWire(t, r)
			app := wire["application"].(map[string]any)
			collection := "larger"
			if kind != "larger" {
				collection = "proofs"
			}
			row := app[collection].([]any)[0].(map[string]any)
			row["skipped"] = []any{"changed prerequisite 849 <script>unsafe</script> [link](https://invalid.example)"}
			changed := calls793DecodeReport(t, wire)
			if verify.Fingerprint(*changed) == verify.Fingerprint(*r) {
				t.Error("skip reasons are absent from fingerprint")
			}
			if got := receipt.Markdown(changed); !strings.Contains(got, "changed prerequisite 849") || strings.Contains(got, "<script>") || strings.Contains(got, "[link](https://invalid.example)") {
				t.Error("skip reason is hidden or not rendered literally")
			}
			wire = calls793ReportWire(t, r)
			app = wire["application"].(map[string]any)
			row = app[collection].([]any)[0].(map[string]any)
			if kind != "larger" {
				row["input"].(map[string]any)["fingerprint"] = "sha256:" + strings.Repeat("a", 64)
			} else {
				row["key"].(map[string]any)["id"] = "different-invocation"
			}
			if verify.Fingerprint(*calls793DecodeReport(t, wire)) == verify.Fingerprint(*r) {
				t.Error("skipped input identity is absent from fingerprint")
			}
			if kind != "larger" {
				// Optimistic decoded result/status fields cannot override an
				// explicit unrun proof and its blocking reasons.
				wire = calls793ReportWire(t, r)
				app = wire["application"].(map[string]any)
				p := app["proofs"].([]any)[0].(map[string]any)
				result := p["result"].(map[string]any)
				target := result["target"].(string)
				result["passed"], result["verified"] = true, []any{target}
				for _, v := range wire["components"].(map[string]any)["proofs"].([]any) {
					proof := v.(map[string]any)
					if proof["target"] == target {
						proof["status"], proof["result"] = "proved", result
					}
				}
				forged := calls793DecodeReport(t, wire)
				md = receipt.Markdown(forged)
				if !strings.Contains(md, "Component proofs: **0 proved;") || strings.Contains(md, "verified by Nagini") || !strings.Contains(strings.ToLower(md), "skipped") {
					t.Error("receipt requalification trusted optimistic fields on an unrun proof")
				}
			}
		})
	}
}
