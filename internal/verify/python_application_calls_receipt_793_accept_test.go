package verify_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func calls793ReportWire(t *testing.T, r *verify.Report) map[string]any {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&wire); err != nil {
		t.Fatal(err)
	}
	return wire
}

func calls793DecodeReport(t *testing.T, wire map[string]any) *verify.Report {
	t.Helper()
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var r verify.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestObservedDecisionProofReceipt793(t *testing.T) {
	r := verify.ObservedDecisionReport793(t)
	decoded := calls793DecodeReport(t, calls793ReportWire(t, r))
	if verify.Fingerprint(*decoded) != verify.Fingerprint(*r) {
		t.Fatal("observed proof evidence changed on report round trip")
	}
	md := receipt.Markdown(decoded)
	for _, want := range []string{"Component proofs: **1 proved;", "counter/admission.py", "arguments", "has_site", "busy", "409"} {
		if !strings.Contains(md, want) {
			t.Fatalf("receipt lacks %q: %s", want, md)
		}
	}
	if strings.Contains(md, "**What failed**") {
		t.Fatalf("observed proof receipt lists a failure: %s", md)
	}
	// Existing status fields are deliberately left optimistic. Qualification
	// must be derived again from the actual matching execution and check.
	for _, mutation := range []string{
		"no refused calls", "no accepted calls", "stale call source", "foreign call target", "no call declaration",
		"incomplete execution", "failed execution", "foreign invocation", "replays only", "sampled execution",
		"missing execution", "failed transition check", "failed attempt check", "missing state coverage", "missing component conformance",
	} {
		t.Run(mutation, func(t *testing.T) {
			wire := calls793ReportWire(t, r)
			application := wire["application"].(map[string]any)
			components := wire["components"].(map[string]any)
			var execution map[string]any
			for _, value := range application["executions"].([]any) {
				x := value.(map[string]any)
				if x["key"].(map[string]any)["kind"] == "conformance" {
					execution = x
				}
			}
			if execution == nil {
				t.Fatal("fixture has no conformance execution")
			}
			for _, value := range execution["observations"].([]any) {
				row := value.(map[string]any)
				calls, _ := row["calls"].([]any)
				if len(calls) == 0 {
					continue
				}
				switch mutation {
				case "no refused calls":
					if row["outcome"] == "refused" {
						delete(row, "calls")
					}
				case "no accepted calls":
					if row["outcome"] == "accepted" {
						delete(row, "calls")
					}
				case "stale call source":
					calls[0].(map[string]any)["source_sha256"] = "sha256:" + strings.Repeat("0", 64)
				case "foreign call target":
					calls[0].(map[string]any)["target"] = "python:counter.core#increment@counter/core.py"
				case "replays only":
					row["replay"] = true
				}
			}
			switch mutation {
			case "no call declaration":
				for _, value := range components["inventory"].(map[string]any)["checks"].([]any) {
					delete(value.(map[string]any), "observe")
				}
			case "incomplete execution":
				execution["complete"] = false
			case "failed execution":
				execution["success"], execution["exit_code"] = false, json.Number("1")
			case "foreign invocation":
				execution["key"].(map[string]any)["id"] = "another-session"
			case "sampled execution":
				execution["observation_mode"] = "sampled"
			case "missing execution":
				application["executions"] = []any{}
			case "failed transition check":
				for _, value := range application["conformance"].([]any) {
					value.(map[string]any)["result"].(map[string]any)["passed"] = false
				}
			case "failed attempt check":
				for _, value := range application["conformance"].([]any) {
					value.(map[string]any)["result"].(map[string]any)["tried"].(map[string]any)["passed"] = false
				}
			case "missing state coverage":
				for _, value := range application["conformance"].([]any) {
					value.(map[string]any)["result"].(map[string]any)["model_states"] = json.Number("99")
				}
			case "missing component conformance":
				components["conformance"] = []any{}
			}
			bad := calls793DecodeReport(t, wire)
			if verify.Fingerprint(*bad) == verify.Fingerprint(*r) {
				t.Fatal("changed evidence did not change fingerprint")
			}
			md := receipt.Markdown(bad)
			if strings.Contains(md, "Component proofs: **1 proved;") || !strings.Contains(md, "not qualified") || !strings.Contains(md, "In-place proof:") {
				t.Fatalf("%s retained a qualified proof or hid the shortfall: %s", mutation, md)
			}
		})
	}
}
