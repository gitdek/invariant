package verify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

const calls793Source = "counter/admission.py"

// This is the statue service's refusal policy, separate from the worker.
// The existing hold fixture exercises admission and the busy refusal while
// preserving its independently checked reservation, release and read steps.
const calls793Admission = `from nagini_contracts.contracts import *

def admission(has_site: bool, site: str, requested: str, busy: bool) -> int:
    Ensures(Implies(has_site and site != 'statue', Result() == 403))
    Ensures(Implies((not has_site or site == 'statue') and requested != 'engine', Result() == 400))
    Ensures(Implies((not has_site or site == 'statue') and requested == 'engine' and busy, Result() == 409))
    Ensures(Implies((not has_site or site == 'statue') and requested == 'engine' and not busy, Result() == 0))
    if has_site and site != 'statue':
        return 403
    if requested != 'engine':
        return 400
    if busy:
        return 409
    return 0
`

// Check the public JSON contract before preparing Python, just as the
// existing hold acceptance tests do. This fails on the independent base;
// implementing only this round trip does not satisfy the execution tests.
func calls793API(t *testing.T) {
	t.Helper()
	var check project.ComponentCheck
	raw := `{"target":{"source":"counter/core.py","symbol":"increment"},"observe":[{"source":"counter/admission.py","symbol":"admission"}]}`
	if err := json.Unmarshal([]byte(raw), &check); err != nil {
		t.Fatal(err)
	}
	wire := refusal392Wire(t, check)
	if refs, ok := wire["observe"].([]any); !ok || len(refs) != 1 {
		t.Fatalf("component check discarded the functions exploration must observe: %v", wire)
	}
	var row PythonApplicationObservation
	raw = `{"calls":[{"target":"python:counter.admission#admission@counter/admission.py","source_sha256":"sha256:abc","arguments":{"n":9007199254740993},"result":9007199254740995}]}`
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatal(err)
	}
	wire = refusal392Wire(t, row)
	list, ok := wire["calls"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("observation discarded call evidence: %v", wire)
	}
	call := list[0].(map[string]any)
	if call["arguments"].(map[string]any)["n"] != json.Number("9007199254740993") || call["result"] != json.Number("9007199254740995") {
		t.Fatalf("call evidence rounded Python integers: %v", call)
	}
}

func calls793Fixture(t *testing.T, mode string) pa342Fixture {
	t.Helper()
	source := "from .admission import admission\n" + hold393Source
	old := "if counts['active'] != 0:\n            raise Refused(409)"
	call := "admission(True, 'statue', 'engine', counts['active'] != 0)"
	switch mode {
	case "both", "twice":
		replacement := "status = " + call + "\n        if status != 0:\n            raise Refused(status)"
		if mode == "twice" {
			replacement = call + "\n        " + replacement
		}
		source = strings.Replace(source, old, replacement, 1)
	case "accepted only":
		source = strings.Replace(source, old, old+"\n        "+call, 1)
	case "refused only":
		source = strings.Replace(source, old, "if counts['active'] != 0:\n            raise Refused("+call+")", 1)
	case "never", "outside entry":
	default:
		t.Fatalf("unknown fixture mode %q", mode)
	}
	driver := hold393Driver
	if mode == "outside entry" {
		driver = strings.Replace(driver, "    app.reset()", "    app.reset()\n    app.admission(True, 'statue', 'engine', False)\n    app.admission(True, 'statue', 'engine', True)", 1)
	}
	f := hold393Fixture(t, true, source, driver)
	pa342Write(t, f.root, calls793Source, calls793Admission)
	for _, name := range []string{"__init__.py", "contracts.py"} {
		body, err := naginiRuntime.ReadFile("naginiruntime/nagini_contracts/" + name)
		if err != nil {
			t.Fatal(err)
		}
		pa342Write(t, f.root, "nagini_contracts/"+name, string(body))
	}
	f.p.Manifest.Existing = append(f.p.Manifest.Existing, calls793Source)
	check := refusal392Wire(t, f.p.Manifest.Components.Checks[0])
	check["observe"] = []project.ComponentRef{pa342Ref(calls793Source, "admission")}
	data, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &f.p.Manifest.Components.Checks[0]); err != nil {
		t.Fatal(err)
	}
	target := pa342Ref(calls793Source, "admission")
	f.p.Manifest.Components.Checks = append(f.p.Manifest.Components.Checks, project.ComponentCheck{
		Target: target, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}},
	})
	pa342Save(t, f)
	return f
}

func calls793Proof(t *testing.T, r *Report) ComponentProof {
	t.Helper()
	if r.Components != nil {
		for _, row := range r.Components.Proofs {
			if row.Target == pa342ID(calls793Source, "admission") {
				return row
			}
		}
	}
	t.Fatalf("missing admission proof: %+v", r)
	return ComponentProof{}
}

func calls793Assert(t *testing.T, x *PythonApplicationExecutionResult, perEntry int) {
	t.Helper()
	hold393Assert(t, x)
	accepted, refused := 0, 0
	for _, row := range x.Observations {
		wire := refusal392Wire(t, row)
		calls, _ := wire["calls"].([]any)
		if row.Kind != "transition" || row.Step != "Submit" {
			if len(calls) != 0 {
				t.Fatalf("calls escaped their application entry: %v", wire)
			}
			continue
		}
		if len(calls) != perEntry {
			t.Fatalf("entry calls missing, merged or duplicated: %v", wire)
		}
		busy := row.Before["active"] == json.Number("1")
		result := json.Number("0")
		if busy {
			result = json.Number("409")
		}
		for _, value := range calls {
			call := value.(map[string]any)
			wantArgs := map[string]any{"has_site": true, "site": "statue", "requested": "engine", "busy": busy}
			if call["target"] != pa342ID(calls793Source, "admission") || call["source_sha256"] != pythonApplicationHash([]byte(calls793Admission)) ||
				!reflect.DeepEqual(call["arguments"], wantArgs) || call["result"] != result {
				t.Fatalf("wrong staged call, arguments or return value: %v", call)
			}
		}
		if !*row.Replay {
			if row.Outcome == "refused" {
				refused++
				if *row.Called {
					t.Fatal("observed decision counted as starting the selected worker")
				}
			} else if row.Outcome == "accepted" {
				accepted++
			}
		}
	}
	if accepted == 0 || refused == 0 {
		t.Fatal("decision evidence must include accepted and refused attempts")
	}
}

func TestPythonApplicationGateObservedDecision793(t *testing.T) {
	calls793API(t)
	run := func(t *testing.T, tc toolchain.Toolchain, real bool) {
		gp359Verifier(t, real)
		f := calls793Fixture(t, "both")
		if err := Check(f.p.Dir); err != nil {
			t.Fatalf("observed proof selection must be admitted: %v", err)
		}
		r := pa342Unchanged(t, f, tc)
		proof := calls793Proof(t, r)
		if !r.Passed || proof.Status != "proved" || proof.Result == nil || !proof.Result.Passed {
			t.Fatalf("observed admission proof did not qualify: %s", gp359Dump(t, r))
		}
		x, c := pa342Explored(t, r)
		calls793Assert(t, x, 1)
		if !c.passed() || !c.Result.Exhaustive || c.Result.Tried == nil || !c.Result.Tried.Passed {
			t.Fatalf("observed proof weakened conformance: %+v", c)
		}
		for _, row := range r.Components.Conformance {
			if row.Target == proof.Target {
				t.Fatal("observed call invented selected-function conformance")
			}
		}
		// Selection must not silently enable this route for sampled checks.
		f.p.Manifest.Exhaustive = false
		pa342Save(t, f)
		if err := Check(f.p.Dir); err == nil {
			t.Fatal("sampled check admitted observed-call proof qualification")
		}
	}
	t.Run("actual Python and verifier doubles", func(t *testing.T) {
		tc, _ := hold393GateFake(t)
		run(t, tc, false)
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("pinned Python TLC and Nagini", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			run(t, tc, true)
		})
	}
	t.Run("each call is retained", func(t *testing.T) {
		f := calls793Fixture(t, "twice")
		s := pa315Prepare(t, f.p, pa318Options(t, false, new(int)))
		x, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
		if err != nil || x == nil || !x.Success {
			t.Fatalf("repeated decision calls failed: %+v %v", x, err)
		}
		calls793Assert(t, x, 2)
	})
}

func TestPythonApplicationGateObservedDecisionCoverage793(t *testing.T) {
	calls793API(t)
	for _, mode := range []string{"never", "accepted only", "refused only", "outside entry"} {
		t.Run(mode, func(t *testing.T) {
			tc, _ := hold393GateFake(t)
			calls := gp359Verifier(t, false)
			f := calls793Fixture(t, mode)
			r := pa342Unchanged(t, f, tc)
			x, c := pa342Explored(t, r)
			hold393Assert(t, x)
			proof := calls793Proof(t, r)
			if !c.passed() || proof.Result == nil || proof.Result.Passed || *calls != 0 || r.Application.Proofs[0].Ran || len(r.Application.Proofs[0].Skipped) == 0 {
				t.Fatalf("fixture must have passing worker conformance and skip Nagini: %s", gp359Dump(t, r))
			}
			if r.Passed || proof.Status != "unchecked" {
				t.Fatalf("%s calls incorrectly qualified the proof: %s", mode, gp359Dump(t, r))
			}
			// Other attribution cannot turn a skipped proof into a proof.
			inv := r.Components.Inventory
			if err := r.AttributeComponentAssumptions(&inv, nil); err != nil || calls793Proof(t, r).Status != "unchecked" {
				t.Fatalf("later attribution lost the missing-call qualification: %v", err)
			}
		})
	}
}

func TestPythonApplicationObservedCallTransport793(t *testing.T) {
	calls793API(t)
	f := calls793Fixture(t, "both")
	opts := pa318Options(t, false, new(int))
	execute := opts.Execute
	var command PythonApplicationCommand
	var request pythonApplicationRequest
	var secret, events []byte
	opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		command = c
		var err error
		secret, err = os.ReadFile(filepath.Join(c.OutputDir, "authentication.key"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(c.RuntimeDir, "request.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		p, err := execute(ctx, c)
		events = bytes.Clone(p.Events)
		return p, err
	}
	s := pa315Prepare(t, f.p, opts)
	x, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
	if err != nil || x == nil || !x.Success {
		t.Fatalf("execution: %+v %v", x, err)
	}
	calls793Assert(t, x, 1)
	for _, mutation := range []string{"foreign target", "stale source", "no arguments", "no result", "initial call", "release call", "changed replay"} {
		t.Run(mutation, func(t *testing.T) {
			var rows []map[string]any
			var selected, initial, release, replay map[string]any
			for _, line := range bytes.Split(bytes.TrimSpace(events), []byte{'\n'}) {
				var row map[string]any
				d := json.NewDecoder(bytes.NewReader(line))
				d.UseNumber()
				if err := d.Decode(&row); err != nil {
					t.Fatal(err)
				}
				if row["type"] == "authentication" {
					continue
				}
				rows = append(rows, row)
				if row["kind"] == "initial" {
					initial = row
				}
				if row["operation"] == "release" {
					release = row
				}
				if row["step"] == "Submit" && row["kind"] == "transition" {
					if row["replay"] == true {
						replay = row
					} else {
						selected = row
					}
				}
			}
			if selected == nil || initial == nil || release == nil || replay == nil {
				t.Fatal("fixture lost its attempts")
			}
			call := selected["calls"].([]any)[0].(map[string]any)
			switch mutation {
			case "foreign target":
				call["target"] = pa342ID(pa342Source, "increment")
			case "stale source":
				call["source_sha256"] = "sha256:" + strings.Repeat("0", 64)
			case "no arguments":
				delete(call, "arguments")
			case "no result":
				delete(call, "result")
			case "initial call":
				initial["calls"] = selected["calls"]
			case "release call":
				release["calls"] = selected["calls"]
			case "changed replay":
				replay["calls"].([]any)[0].(map[string]any)["result"] = json.Number("999")
			}
			// Re-sign as the trusted test transport: Go must validate the
			// record, not just the seal. No prefix may survive a bad record.
			var body bytes.Buffer
			encoder := json.NewEncoder(&body)
			for _, row := range rows {
				if err := encoder.Encode(row); err != nil {
					t.Fatal(err)
				}
			}
			mac := hmac.New(sha256.New, secret)
			mac.Write(body.Bytes())
			if err := encoder.Encode(map[string]string{"type": "authentication", "hmac_sha256": hex.EncodeToString(mac.Sum(nil))}); err != nil {
				t.Fatal(err)
			}
			var result PythonApplicationExecutionResult
			err := s.observeTransitions(command, request.Execution, secret, body.Bytes(), &result, s.declarations[command.Key.Check], s.drivers, true)
			if err == nil || strings.Contains(err.Error(), "authentication") || result.Complete || result.Exploration != nil || len(result.Observations) != 0 {
				t.Fatalf("invalid call evidence survived: %+v %v", result, err)
			}
		})
	}
	// Call data is portable evidence, including integers outside float64's
	// exact range; it must survive report decoding and affect fingerprints.
	wire := refusal392Wire(t, x)
	for _, value := range wire["observations"].([]any) {
		row := value.(map[string]any)
		if calls, ok := row["calls"].([]any); ok && len(calls) > 0 {
			calls[0].(map[string]any)["result"] = json.Number("9007199254740993")
			break
		}
	}
	raw, _ := json.Marshal(wire)
	var changed PythonApplicationExecutionResult
	if err := json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	if pythonApplicationExecutionFingerprint(*x) == pythonApplicationExecutionFingerprint(changed) {
		t.Fatal("call result is absent from fingerprint")
	}
	raw, _ = json.Marshal(changed)
	var decoded PythonApplicationExecutionResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if pythonApplicationExecutionFingerprint(changed) != pythonApplicationExecutionFingerprint(decoded) {
		t.Fatal("call fingerprint changed on JSON round trip")
	}
}

// ObservedDecisionReport793 shares the actual gate fixture with external
// receipt tests without requiring a new production API for making reports.
func ObservedDecisionReport793(t *testing.T) *Report {
	t.Helper()
	calls793API(t)
	tc, _ := hold393GateFake(t)
	gp359Verifier(t, false)
	r := pa342Run(t, calls793Fixture(t, "both"), tc)
	if !r.Passed || calls793Proof(t, r).Status != "proved" {
		t.Fatalf("positive observed-call gate: %s", gp359Dump(t, r))
	}
	return r
}
