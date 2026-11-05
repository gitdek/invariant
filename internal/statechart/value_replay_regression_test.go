package statechart_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// Keep the fixture at the graph runner boundary. The replay output deliberately
// uses a different printed value, as TLC can after registering fresh aliases.
func checkReplayValue(t *testing.T, original, replay string, accept bool) {
	t.Helper()
	in, dir := reader586EvalFixture(t)
	for i := range in.Graph.Graph.States {
		in.Graph.Graph.States[i].Values["worker"] = original
	}
	in.Graph.Config.Constants["RM"] = "{z, a}"
	in.Lock.Bounds["RM"] = "{z, a}"
	source := strings.Replace(reader586Model, "CONSTANT Workers", "CONSTANTS Workers, RM", 1)
	if err := os.WriteFile(filepath.Join(dir, "Lifecycle.tla"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for i := range in.Lock.Statements {
		statement := &in.Lock.Statements[i]
		pin, err := tla.PinHash(source, statement.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		statement.SHA256 = pin
	}
	before := reader586JSON(t, in)
	var path string
	calls := 0
	runner := replayFunc(func(ctx context.Context, dir, module string, cfg tlc.Config) (*tlc.GraphResult, error) {
		calls++
		path = filepath.Join(dir, module+".tla")
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for name, bound := range in.Graph.Config.Constants {
			if cfg.Constants[name] != bound {
				t.Fatalf("original constant %s changed", name)
			}
		}
		if strings.Contains(original, "z") {
			bound := map[string]bool{}
			for name, value := range cfg.Constants {
				if _, ok := in.Graph.Config.Constants[name]; !ok {
					if value != "z" && value != "a" || !strings.Contains(string(source), name) {
						t.Fatalf("unexpected model-value alias %s = %s", name, value)
					}
					bound[value] = true
				}
			}
			if !bound["z"] || !bound["a"] {
				t.Fatalf("model values lack aliases: %v", cfg.Constants)
			}
		} else if !strings.Contains(string(source), "worker = ("+original+")") {
			t.Fatalf("replay did not retain original value %q: %s", original, source)
		}
		aux := tla.Variables(string(source))
		if len(aux) != 2 {
			t.Fatalf("wrong replay variables: %v", aux)
		}
		id, rules := aux[0], aux[1]
		if !strings.Contains(string(source), id+` = "10"`) {
			id, rules = rules, id
		}
		g := &tlc.GraphResult{Module: module, Config: cfg, Tool: in.Graph.Tool,
			Run: tlc.Result{Version: in.Graph.Run.Version, Outcome: tlc.Passed, Depth: 1}}
		for _, s := range in.Graph.Graph.States {
			values := maps.Clone(s.Values)
			values["worker"] = replay
			values[id] = strconv.Quote(s.ID)
			var fields []string
			for name, holds := range in.Invariants[s.ID] {
				fields = append(fields, name+" |-> "+strings.ToUpper(strconv.FormatBool(holds)))
			}
			slices.Sort(fields)
			values[rules] = "[" + strings.Join(fields, ", ") + "]"
			g.Graph.States = append(g.Graph.States, tlc.GraphState{ID: s.ID, Label: "replay", Values: values})
			g.Graph.Initial = append(g.Graph.Initial, s.ID)
			g.Graph.Steps = append(g.Graph.Steps, tlc.GraphStep{From: s.ID, To: s.ID, Action: "Stay"})
		}
		g.StateCount, g.StepCount = len(g.Graph.States), len(g.Graph.Steps)
		g.Run.DistinctStates = int64(g.StateCount)
		g.Run.StatesGenerated = int64(g.StateCount + g.StepCount)
		return g, nil
	})
	matrix, err := statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock)
	if calls != 1 {
		t.Fatalf("valid original value refused before replay: calls=%d error=%v", calls, err)
	}
	if accept {
		if err != nil || !reflect.DeepEqual(matrix, in.Invariants) {
			t.Fatalf("equivalent value refused: %v %v", matrix, err)
		}
	} else {
		var refusal *statechart.Refusal
		if matrix != nil || !errors.As(err, &refusal) || refusal.Code != "invalid_evidence" || len(refusal.States) == 0 {
			t.Fatalf("changed or malformed value supplied evidence: %v %v", matrix, err)
		}
	}
	if !bytes.Equal(before, reader586JSON(t, in)) {
		t.Fatal("value comparison changed original graph or lock")
	}
	reader586Clean(t, dir, &reader586Dumper{paths: []string{path}})
}

func TestReplayPrintedIntervals(t *testing.T) {
	for _, tc := range []struct{ original, replay string }{
		{"1..2", "1..2"},
		{"-2..2", "-2 .. 2"},
		{"1..2", "{2, 1}"},
		{"{2, 1}", "1..2"},
		{"2..1", "{}"},
		{"1..1", "{1}"},
		{"-2147483648..2147483647", "-2147483648..2147483647"},
		{`[span |-> -2..2, nested |-> <<{1..2, 4..5}, (1..2 :> TRUE)>>]`,
			`[nested |-> <<{4..5, {2, 1}}, ({1, 2} :> TRUE)>>, span |-> {2, 1, 0, -1, -2}]`},
	} {
		t.Run(tc.original, func(t *testing.T) { checkReplayValue(t, tc.original, tc.replay, true) })
	}
}

func TestReplayUnorderedValues(t *testing.T) {
	for _, tc := range []struct{ original, replay string }{
		{`{z, a}`, `{a, z}`},
		{`[nodes |-> {z, a}, message |-> "a  b"]`, `[message |-> "a  b", nodes |-> {a, z}]`},
		{`(z :> {z, a} @@ a :> <<"a", TRUE>>)`, `(a :> <<"a", TRUE>> @@ z :> {a, z})`},
		{`({z, a} :> [b |-> 2, a |-> 1] @@ <<z, a>> :> FALSE)`,
			`(<<z, a>> :> FALSE @@ {a, z} :> [a |-> 1, b |-> 2])`},
		{`{(1 :> "a" @@ "1" :> TRUE), <<{3, 1}, "a\\b\"c">>}`,
			`{<<{1, 3}, "a\\b\"c">>, ("1" :> TRUE @@ 1 :> "a")}`},
	} {
		t.Run(tc.original, func(t *testing.T) { checkReplayValue(t, tc.original, tc.replay, true) })
	}
}

func TestReplayValueChangesStillRefuse(t *testing.T) {
	for name, tc := range map[string][2]string{
		"interval endpoint":       {"1..2", "1..3"},
		"interval and integer":    {"1..1", "1"},
		"missing endpoint":        {"1..2", "1.."},
		"noninteger endpoint":     {"1..2", "1..TRUE"},
		"interval expression":     {"1..2", "1..2 + 1"},
		"trailing input":          {"1..2", "1..2 TRUE"},
		"set and tuple":           {"{1, 2}", "<<1, 2>>"},
		"tuple order":             {"<<1, 2>>", "<<2, 1>>"},
		"boolean and model value": {"TRUE", "true"},
		"integer and string":      {"1", `"1"`},
		"model value and string":  {"{z, a}", `{"z", a}`},
		"changed string spaces":   {`[text |-> "a  b"]`, `[text |-> "a b"]`},
		"changed string escapes":  {`"a\nb"`, `"anb"`},
		"integer and string key":  {`(1 :> TRUE)`, `("1" :> TRUE)`},
		"model and string key":    {`(z :> {z, a})`, `("z" :> {a, z})`},
		"key value association":   {`(1 :> TRUE @@ 2 :> FALSE)`, `(2 :> TRUE @@ 1 :> FALSE)`},
		"duplicate function key":  {`(1 :> TRUE)`, `(1 :> FALSE @@ 1 :> TRUE)`},
		"duplicate record field":  {`[x |-> TRUE]`, `[x |-> FALSE, x |-> TRUE]`},
		"changed nested value":    {`<<[members |-> {1, 3}], (1 :> TRUE)>>`, `<<[members |-> {1, 4}], (1 :> TRUE)>>`},
	} {
		t.Run(name, func(t *testing.T) { checkReplayValue(t, tc[0], tc[1], false) })
	}
}

func TestReplayRejectsMalformedOriginalValues(t *testing.T) {
	for _, value := range []string{"1..", "1..TRUE", "1..2..3", "1..2 + 1", "{1..2", `(1 :> TRUE @@ 1 :> FALSE)`, `[x |-> TRUE, x |-> FALSE]`} {
		t.Run(value, func(t *testing.T) {
			in, dir := reader586EvalFixture(t)
			in.Graph.Graph.States[0].Values["worker"] = value
			d := &reader586Dumper{t: t, in: in}
			matrix, err := statechart.EvaluateRules(t.Context(), d, dir, in.Graph, in.Lock)
			var refusal *statechart.Refusal
			if matrix != nil || !errors.As(err, &refusal) || refusal.Code != "invalid_evidence" || d.calls != 0 {
				t.Fatalf("malformed original reached replay: %v %v calls=%d", matrix, err, d.calls)
			}
			reader586Clean(t, dir, d)
		})
	}
}
