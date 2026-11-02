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
	"strings"
	"testing"
)

// Re-sign corruptions with the trusted executor's bootstrap key so these
// cases exercise Go's protocol validation after authentication succeeds.
func TestDeclaredRefusalAuthenticatedOutcomes(t *testing.T) {
	f := refusal392Fixture(t, true, refusal392Source, refusal392Driver, refusal392Model)
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
		t.Fatalf("baseline execution: %+v %v", x, err)
	}
	refusal392Assert(t, x)
	row := func(rows []map[string]any, kind, step, outcome string) map[string]any {
		for _, r := range rows {
			if r["kind"] == kind && (step == "" || r["step"] == step) && (outcome == "" || r["outcome"] == outcome) {
				return r
			}
		}
		t.Fatal("no row to corrupt")
		return nil
	}
	declaration := func(rows []map[string]any) map[string]any {
		for _, r := range rows {
			if r["type"] == "exploration" {
				return r["steps"].([]any)[0].(map[string]any)
			}
		}
		t.Fatal("no declaration to corrupt")
		return nil
	}
	for _, c := range []struct {
		name   string
		mutate func([]map[string]any)
	}{
		{"missing outcome", func(rows []map[string]any) {
			delete(row(rows, "transition", "Submit", "accepted"), "outcome")
		}},
		{"null outcome", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "accepted")["outcome"] = nil
		}},
		{"unknown outcome", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "accepted")["outcome"] = "failed"
		}},
		{"accepted with refusal", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "accepted")["refusal"] = row(rows, "transition", "Submit", "refused")["refusal"]
		}},
		{"accepted with null refusal", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "accepted")["refusal"] = nil
		}},
		{"missing exception", func(rows []map[string]any) {
			delete(row(rows, "transition", "Submit", "refused"), "refusal")
		}},
		{"null exception", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["refusal"] = nil
		}},
		{"called refusal", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["called"] = true
		}},
		{"changed refusal", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["after"] = map[string]any{"count": json.Number("1")}
		}},
		{"other exception", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["refusal"].(map[string]any)["type"] = "builtins.Exception"
		}},
		{"float instead of integer", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["refusal"].(map[string]any)["attributes"].(map[string]any)["status_code"] = json.Number("409.0")
		}},
		{"container attribute", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["refusal"].(map[string]any)["attributes"].(map[string]any)["status_code"] = []any{409}
		}},
		{"nonfinite attribute", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["refusal"].(map[string]any)["attributes"].(map[string]any)["status_code"] = json.Number("1e9999")
		}},
		{"extra exception field", func(rows []map[string]any) {
			row(rows, "transition", "Submit", "refused")["refusal"].(map[string]any)["message"] = "undeclared"
		}},
		{"legacy outcome", func(rows []map[string]any) {
			row(rows, "transition", "Legacy", "")["outcome"] = "accepted"
		}},
		{"legacy null outcome", func(rows []map[string]any) {
			row(rows, "transition", "Legacy", "")["outcome"] = nil
		}},
		{"initial outcome", func(rows []map[string]any) {
			row(rows, "initial", "", "")["outcome"] = "accepted"
		}},
		{"null declaration", func(rows []map[string]any) {
			declaration(rows)["refusal"] = nil
		}},
		{"null declaration attributes", func(rows []map[string]any) {
			declaration(rows)["refusal"].(map[string]any)["attributes"] = nil
		}},
		{"unknown declaration field", func(rows []map[string]any) {
			declaration(rows)["refusal"].(map[string]any)["extra"] = true
		}},
		{"no declared exception module", func(rows []map[string]any) {
			declaration(rows)["refusal"].(map[string]any)["type"] = "Refused"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var rows []map[string]any
			for _, line := range bytes.Split(bytes.TrimSpace(events), []byte{'\n'}) {
				var row map[string]any
				d := json.NewDecoder(bytes.NewReader(line))
				d.UseNumber()
				if err := d.Decode(&row); err != nil {
					t.Fatal(err)
				}
				if row["type"] != "authentication" {
					rows = append(rows, row)
				}
			}
			c.mutate(rows)
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
				t.Fatalf("bad stream retained evidence: %+v %v", result, err)
			}
		})
	}
}

func TestDeclaredRefusalReplayOutcome(t *testing.T) {
	var x pythonApplicationExplorer
	if err := x.declare([]byte(`{"type":"exploration","steps":[{"name":"Read","args":[[]],"refusal":{"type":"app.Refused","attributes":{"status":null}}}]}`)); err != nil {
		t.Fatal(err)
	}
	start, replay, called := 0, false, true
	state := map[string]any{"count": json.Number("1")}
	initial := PythonApplicationObservation{Kind: "initial", Run: 1, State: state, Start: &start}
	if err := x.observe(initial); err != nil {
		t.Fatal(err)
	}
	accepted := PythonApplicationObservation{Kind: "transition", Run: 1, Step: "Read", Args: []any{}, Before: state, After: state, Replay: &replay, Called: &called, Outcome: "accepted"}
	if err := x.observe(accepted); err != nil {
		t.Fatal(err)
	}
	stats, err := x.finish()
	if err != nil || stats.Refusals != 0 || stats.DeclaredRefusals == nil || *stats.DeclaredRefusals != 0 {
		t.Fatalf("read-only normal return counted as refusal: %+v %v", stats, err)
	}
	initial.Run = 2
	if err := x.observe(initial); err != nil {
		t.Fatal(err)
	}
	accepted.Run, accepted.Outcome, accepted.Refusal = 2, "refused", x.refusals["Read"]
	replay, called = true, false
	if err := x.observe(accepted); err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Fatalf("replay changed acceptance into refusal: %v", err)
	}
}

func TestDeclaredRefusalScalarAttributes(t *testing.T) {
	source := strings.Replace(refusal392Source, "self.status_code = status_code", "self.status_code = status_code\n        self.detail = None\n        self.retry = False\n        self.ratio = 0.5\n        self.note = 'busy'\n        self.token = 9007199254740993", 1)
	driver := strings.Replace(refusal392Driver, "status_code=409", "status_code=409, detail=None, retry=False, ratio=0.5, note='busy', token=9007199254740993", 1)
	refusal392Modes(t, func(t *testing.T, real bool) {
		x := refusal392Execution(t, real, source, driver, false)
		if !x.Success || x.Exploration == nil || x.Exploration.Refusals != 8 {
			t.Fatalf("valid scalar attributes rejected: %+v", x)
		}
		found := false
		for i := range x.Observations {
			row := &x.Observations[i]
			if row.Step != "Submit" || row.Outcome != "refused" {
				continue
			}
			found = true
			if got := pythonApplicationKey(row.Refusal.Attributes); got != `{"detail":null,"note":"busy","ratio":0.5,"retry":false,"status_code":409,"token":9007199254740993}` {
				t.Fatalf("scalar attributes changed: %s", got)
			}
			row.Outcome = ""
			if _, err := pythonApplicationAttempts(*x); err == nil {
				t.Fatal("conversion silently downgraded a declared outcome")
			}
			row.Outcome = "refused"
		}
		if !found {
			t.Fatal("no declared refusal recorded")
		}
	})
}

func TestDeclaredRefusalFingerprints(t *testing.T) {
	const raw = `{"application":{"executions":[{"exploration":{"declared_refusals":1,"steps":[{"name":"Read","args":[[]],"refusal":{"type":"app.Refused","attributes":{"token":9007199254740993}}}]},"observations":[{"outcome":"refused","refusal":{"type":"app.Refused","attributes":{"token":9007199254740993}}}]}]}}`
	var original Report
	if err := json.Unmarshal([]byte(raw), &original); err != nil {
		t.Fatal(err)
	}
	fingerprint := Fingerprint(original)
	b, err := json.Marshal(original)
	if err != nil || !bytes.Contains(b, []byte(`9007199254740993`)) {
		t.Fatalf("exact attributes lost: %s %v", b, err)
	}
	for name, change := range map[string]func(*PythonApplicationExecutionResult){
		"declaration": func(x *PythonApplicationExecutionResult) {
			x.Exploration.Steps[0].Refusal.Type = "app.Other"
		},
		"declared count": func(x *PythonApplicationExecutionResult) {
			*x.Exploration.DeclaredRefusals = 2
		},
		"outcome": func(x *PythonApplicationExecutionResult) {
			x.Observations[0].Outcome = "accepted"
		},
		"exception": func(x *PythonApplicationExecutionResult) {
			x.Observations[0].Refusal.Type = "app.Other"
		},
		"attribute": func(x *PythonApplicationExecutionResult) {
			x.Observations[0].Refusal.Attributes["token"] = json.Number("9007199254740992")
		},
	} {
		t.Run(name, func(t *testing.T) {
			var r Report
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatal(err)
			}
			if Fingerprint(r) != fingerprint {
				t.Fatal("JSON round trip changed fingerprint")
			}
			change(&r.Application.Executions[0])
			if Fingerprint(r) == fingerprint {
				t.Fatal("changed strict evidence kept its fingerprint")
			}
		})
	}
}
