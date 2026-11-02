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

func TestPythonApplicationHeldCallAuthenticatedOperations(t *testing.T) {
	hold393API(t)
	f := hold393Fixture(t, true, hold393Source, hold393Driver)
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
		t.Fatalf("held baseline execution: %+v %v", x, err)
	}
	hold393Assert(t, x)
	selectRow := func(rows []map[string]any, step string, replay bool, busy bool) map[string]any {
		for _, row := range rows {
			if row["kind"] == "transition" && row["step"] == step && row["replay"] == replay {
				state := row["before"].(map[string]any)
				if (state["active"] == json.Number("1")) == busy {
					return row
				}
			}
		}
		t.Fatalf("no %s replay=%v busy=%v row", step, replay, busy)
		return nil
	}
	for _, c := range []struct {
		name   string
		change func([]map[string]any)
	}{
		{"unknown operation", func(rows []map[string]any) { selectRow(rows, "Submit", false, false)["operation"] = "pause" }},
		{"null operation", func(rows []map[string]any) { selectRow(rows, "Submit", false, false)["operation"] = nil }},
		{"initial operation", func(rows []map[string]any) {
			for _, r := range rows {
				if r["kind"] == "initial" {
					r["operation"] = "hold"
					return
				}
			}
		}},
		{"release counts another selected call", func(rows []map[string]any) { selectRow(rows, "Finish", false, true)["called"] = true }},
		{"hold has no selected start", func(rows []map[string]any) { selectRow(rows, "Submit", false, false)["called"] = false }},
		{"second selected start while held", func(rows []map[string]any) { selectRow(rows, "Read", false, true)["called"] = true }},
		{"release changes state with nothing held", func(rows []map[string]any) {
			r := selectRow(rows, "Finish", false, false)
			r["after"] = map[string]any{"count": json.Number("1"), "active": json.Number("1"), "prepared": true}
		}},
		{"replay changes hold into ordinary call", func(rows []map[string]any) { delete(selectRow(rows, "Submit", true, false), "operation") }},
		{"replay changes release into ordinary step", func(rows []map[string]any) { delete(selectRow(rows, "Finish", true, true), "operation") }},
		{"ordinary call claims release target identity", func(rows []map[string]any) {
			r := selectRow(rows, "Read", false, false)
			r["entry"], r["entry_sha256"] = r["target"], r["source_sha256"]
		}},
		{"release substitutes surrounding entry", func(rows []map[string]any) {
			r := selectRow(rows, "Finish", false, true)
			r["entry"] = pa342ID(pa342Source, "submit")
		}},
		{"held refusal claims a selected start", func(rows []map[string]any) { selectRow(rows, "Submit", false, true)["called"] = true }},
		{"non-module hold", func(rows []map[string]any) { delete(selectRow(rows, "Submit", false, false), "form") }},
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
			c.change(rows)
			// Re-sign corruptions as the trusted transport, so rejection must
			// come from independent Go validation, not the HMAC check.
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
				t.Fatalf("contradictory held stream retained evidence: %+v %v", result, err)
			}
		})
	}
	// Operation metadata is evidence, not a transient transport identity.
	wire := refusal392Wire(t, x)
	changed := false
	for _, value := range wire["observations"].([]any) {
		row := value.(map[string]any)
		if row["operation"] == "hold" {
			row["operation"] = "release"
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("no operation to fingerprint")
	}
	b, _ := json.Marshal(wire)
	var altered PythonApplicationExecutionResult
	if err := json.Unmarshal(b, &altered); err != nil {
		t.Fatal(err)
	}
	if pythonApplicationExecutionFingerprint(*x) == pythonApplicationExecutionFingerprint(altered) {
		t.Fatal("operation missing from portable fingerprint")
	}
}
