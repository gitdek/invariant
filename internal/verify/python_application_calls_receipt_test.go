package verify_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestObservedCallsReceiptRechecksComponentCoverage(t *testing.T) {
	r := verify.ObservedDecisionReport793(t)
	decoded := calls793DecodeReport(t, calls793ReportWire(t, r))
	md := receipt.Markdown(decoded)
	if !strings.Contains(md, "Component proofs: **1 proved;") || !strings.Contains(md, "has_site") || strings.Contains(md, "**What failed**") {
		t.Fatal("the decoded receipt lost its qualified proof or readable call arguments")
	}
	for _, mutation := range []string{"transitions", "states", "attempts", "invocation", "replay arguments", "entry source"} {
		t.Run(mutation, func(t *testing.T) {
			wire := calls793ReportWire(t, r)
			components := wire["components"].(map[string]any)
			for _, value := range components["conformance"].([]any) {
				result := value.(map[string]any)["result"].(map[string]any)
				switch mutation {
				case "transitions":
					result["transitions"] = json.Number("0")
				case "states":
					result["model_states"] = json.Number("999")
				case "attempts":
					result["attempts"].(map[string]any)["outcome"] = "failed"
				case "invocation":
					result["invocation"].(map[string]any)["id"] = "another-invocation"
				}
			}
			for _, value := range wire["application"].(map[string]any)["executions"].([]any) {
				x := value.(map[string]any)
				rows, _ := x["observations"].([]any)
				for _, value := range rows {
					row := value.(map[string]any)
					calls, _ := row["calls"].([]any)
					if len(calls) == 0 {
						continue
					}
					if mutation == "entry source" {
						row["entry_sha256"] = "sha256:" + strings.Repeat("0", 64)
					}
					if mutation == "replay arguments" && row["replay"] == true {
						calls[0].(map[string]any)["arguments"].(map[string]any)["site"] = "another-site"
					}
				}
			}
			bad := calls793DecodeReport(t, wire)
			md := receipt.Markdown(bad)
			if strings.Contains(md, "Component proofs: **1 proved;") || !strings.Contains(md, "not qualified") || !strings.Contains(md, "In-place proof:") {
				t.Fatalf("%s left the proof qualified: %s", mutation, md)
			}
		})
	}
}
