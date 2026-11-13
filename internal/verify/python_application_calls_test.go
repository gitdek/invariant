package verify

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func observedCallsFixture(t *testing.T, helper, call string) pa342Fixture {
	t.Helper()
	source := "from .decision import decision\n" + hold393Source
	source = strings.Replace(source, "        if counts['active'] != 0:", call+"\n        if counts['active'] != 0:", 1)
	f := hold393Fixture(t, true, source, hold393Driver)
	pa342Write(t, f.root, "counter/decision.py", helper)
	f.p.Manifest.Existing = append(f.p.Manifest.Existing, "counter/decision.py")
	f.p.Manifest.Components.Checks[0].Observe = []project.ComponentRef{pa342Ref("counter/decision.py", "decision")}
	pa342Save(t, f)
	return f
}

func TestPythonApplicationObservedCallCopies(t *testing.T) {
	t.Parallel()
	f := observedCallsFixture(t, `def decision(payload, /, requested='engine', *, busy=False):
    payload['items'].append(9007199254740995)
    return {'items': payload['items'], 'busy': busy, 'empty': None}
`, `        payload = {'items': [9007199254740993]}
        result = decision(payload, busy=counts['active'] != 0)
        payload['items'].append(1)
        result['busy'] = None
        decision({'items': [2]}, 'second', busy=False)`)
	s := pa315Prepare(t, f.p, pa318Options(t, false, new(int)))
	x, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
	if err != nil || x == nil || !x.Success {
		t.Fatalf("execution: %+v %v", x, err)
	}
	hold393Assert(t, x)
	raw, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PythonApplicationExecutionResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, result := range []PythonApplicationExecutionResult{*x, decoded} {
		if accepted, refused, valid := pythonApplicationCallCoverage(result, s.observedDeclarations(x.Key.Target), s.declarations, []string{pa342Source}, pa342ID("counter/decision.py", "decision")); !accepted || !refused || !valid {
			t.Fatal("complete call coverage did not survive JSON")
		}
	}
	seen := 0
	for _, row := range x.Observations {
		if row.Kind != "transition" || row.Step != "Submit" {
			if len(row.Calls) != 0 {
				t.Fatalf("call outside Submit: %+v", row)
			}
			continue
		}
		seen++
		if len(row.Calls) != 2 {
			t.Fatalf("lost calls: %+v", row.Calls)
		}
		first, second := row.Calls[0], row.Calls[1]
		busy := row.Before["active"] == json.Number("1")
		wantArgs := map[string]any{"payload": map[string]any{"items": []any{json.Number("9007199254740993")}}, "requested": "engine", "busy": busy}
		wantResult := map[string]any{"items": []any{json.Number("9007199254740993"), json.Number("9007199254740995")}, "busy": busy, "empty": nil}
		if !reflect.DeepEqual(first.Arguments, wantArgs) || !reflect.DeepEqual(first.Result, wantResult) || second.Arguments["requested"] != "second" {
			t.Fatalf("arguments, result or call order changed: %+v", row.Calls)
		}
	}
	if seen == 0 {
		t.Fatal("no entry calls")
	}
}

func TestPythonApplicationObservedCallRefusesSubstitutionAndValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, helper, setup string }{
		{"replaced function", "def decision(value=1):\n    return value\n", "from types import FunctionType\ndecision = FunctionType(decision.__code__, decision.__globals__, argdefs=(1,))\n"},
		{"replaced default", "def decision(value=1):\n    return value\n", "decision.__defaults__ = (2,)\n"},
		{"infinite result", "def decision():\n    return float('inf')\n", ""},
		{"tuple result", "def decision():\n    return (1, 2)\n", ""},
		{"cyclic result", "def decision():\n    values = []\n    values.append(values)\n    return values\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := observedCallsFixture(t, tc.helper+tc.setup, "        decision()")
			s := pa315Prepare(t, f.p, pa318Options(t, false, new(int)))
			x, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			if err == nil || x == nil || x.Success || x.Complete || len(x.Observations) != 0 || x.Exploration != nil {
				t.Fatalf("invalid call survived: %+v %v", x, err)
			}
		})
	}
}

func TestPythonApplicationObservedCallJSON(t *testing.T) {
	t.Parallel()
	const good = `{"target":"python:app#decision@app.py","source_sha256":"sha256:abc","arguments":{"n":9007199254740993},"result":null}`
	var call PythonApplicationCall
	if err := json.Unmarshal([]byte(good), &call); err != nil || call.Arguments["n"] != json.Number("9007199254740993") || call.Result != nil {
		t.Fatalf("exact arguments and null return: %+v %v", call, err)
	}
	for _, raw := range []string{
		strings.Replace(good, `,"result":null`, "", 1),
		strings.Replace(good, `"result":null`, `"result":null,"result":1`, 1),
		strings.Replace(good, `"result":null`, `"result":1e999`, 1),
		strings.Replace(good, `{"n":9007199254740993}`, `null`, 1),
		strings.Replace(good, `"n":`, `"not a parameter":`, 1),
	} {
		if err := json.Unmarshal([]byte(raw), &call); err == nil {
			t.Fatalf("invalid call accepted: %s", raw)
		}
	}
}

func TestPythonApplicationObservedDeclarationAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*project.Project)
	}{
		{"sampled", func(p *project.Project) { p.Manifest.Exhaustive = false }},
		{"no conformance", func(p *project.Project) { p.Manifest.Components.Checks[0].Conformance = nil }},
		{"duplicate", func(p *project.Project) {
			c := &p.Manifest.Components.Checks[0]
			c.Observe = append(c.Observe, c.Observe[0])
		}},
		{"unknown symbol", func(p *project.Project) { p.Manifest.Components.Checks[0].Observe[0].Symbol = "missing" }},
		{"outside existing", func(p *project.Project) { p.Manifest.Existing = p.Manifest.Existing[:len(p.Manifest.Existing)-1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := observedCallsFixture(t, "def decision():\n    return 0\n", "        decision()")
			tc.edit(f.p)
			pa342Save(t, f)
			if err := Check(f.p.Dir); err == nil {
				t.Fatal("invalid observe declaration admitted")
			}
		})
	}
}
