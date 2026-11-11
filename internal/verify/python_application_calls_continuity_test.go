package verify_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestObservedCallsReceiptRequiresReplayContinuity(t *testing.T) {
	r := verify.ObservedDecisionReport793(t)
	r = calls793DecodeReport(t, calls793ReportWire(t, r))
	if md := receipt.Markdown(r); !strings.Contains(md, "Component proofs: **1 proved;") {
		t.Fatalf("complete receipt lost its qualified proof: %s", md)
	}
	for _, mutation := range []string{"missing replay", "missing replay with contiguous sequence", "changed sequence"} {
		t.Run(mutation, func(t *testing.T) {
			bad := calls793DecodeReport(t, calls793ReportWire(t, r))
			var execution *verify.PythonApplicationExecutionResult
			for i := range bad.Application.Executions {
				x := &bad.Application.Executions[i]
				if x.Key.Kind == "conformance" {
					execution = x
					break
				}
			}
			if execution == nil || execution.Exploration == nil {
				t.Fatal("fixture has no explored conformance execution")
			}
			stats, err := json.Marshal(execution.Exploration)
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			for i, row := range execution.Observations {
				if i == 0 || row.Step != "Submit" || row.Outcome != "refused" || row.Replay == nil || *row.Replay {
					continue
				}
				previous := execution.Observations[i-1]
				if previous.Step != "Submit" || previous.Replay == nil || !*previous.Replay || previous.Run != row.Run {
					t.Fatal("busy refusal is not preceded by the replayed Submit")
				}
				if mutation == "changed sequence" {
					execution.Observations[i].Sequence++
				} else {
					if mutation == "missing replay with contiguous sequence" {
						execution.Observations[i].Sequence--
					}
					execution.Observations = append(execution.Observations[:i-1], execution.Observations[i:]...)
				}
				changed = true
				break
			}
			if !changed {
				t.Fatal("fixture has no non-replay busy refusal")
			}
			accepted, refused := 0, 0
			for _, row := range execution.Observations {
				if row.Replay == nil || *row.Replay || len(row.Calls) == 0 {
					continue
				}
				if row.Outcome == "accepted" {
					accepted++
				} else if row.Outcome == "refused" {
					refused++
				}
			}
			if accepted == 0 || refused == 0 {
				t.Fatal("mutation removed qualifying call outcomes")
			}
			after, err := json.Marshal(execution.Exploration)
			if err != nil || string(after) != string(stats) {
				t.Fatal("mutation changed saved exploration counts")
			}
			// Keep every saved success claim optimistic: the observations
			// themselves must prevent receipt requalification.
			if !bad.Passed || !execution.Complete || !execution.Success || execution.ExitCode != 0 ||
				!reflect.DeepEqual(bad.Components.Proofs, r.Components.Proofs) ||
				!reflect.DeepEqual(bad.Components.Conformance, r.Components.Conformance) ||
				!reflect.DeepEqual(bad.Application.Conformance, r.Application.Conformance) {
				t.Fatal("mutation changed saved proof, execution or conformance status")
			}
			bad = calls793DecodeReport(t, calls793ReportWire(t, bad))
			if verify.Fingerprint(*bad) == verify.Fingerprint(*r) {
				t.Fatal("broken observation continuity did not change the fingerprint")
			}
			md := receipt.Markdown(bad)
			if strings.Contains(md, "Component proofs: **1 proved;") || !strings.Contains(md, "not qualified") || !strings.Contains(md, "In-place proof:") {
				t.Fatalf("%s left the proof qualified or hid the shortfall: %s", mutation, md)
			}
		})
	}
}
