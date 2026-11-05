package statechart_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

type changingReplay struct {
	base *reader586Dumper
	edit func(*tlc.GraphResult)
}

func (d changingReplay) DumpGraph(ctx context.Context, dir, module string, cfg tlc.Config) (*tlc.GraphResult, error) {
	g, err := d.base.DumpGraph(ctx, dir, module, cfg)
	if err == nil {
		d.edit(g)
	}
	return g, err
}

func TestReplayRejectsContradictoryEvidence(t *testing.T) {
	for name, edit := range map[string]func(*tlc.GraphResult){
		"duplicate record field": func(g *tlc.GraphResult) {
			for key, value := range g.Graph.States[0].Values {
				if strings.HasPrefix(value, "[") {
					g.Graph.States[0].Values[key] = `[Safe |-> FALSE, Safe |-> TRUE, Independent |-> TRUE]`
				}
			}
		},
		"foreign variable": func(g *tlc.GraphResult) {
			for i := range g.Graph.States {
				g.Graph.States[i].Values["extra"] = "TRUE"
			}
		},
		"different version": func(g *tlc.GraphResult) { g.Run.Version = "another TLC" },
		"changed bounds":    func(g *tlc.GraphResult) { g.Config.Constants["Workers"] = "9" },
		"configured check":  func(g *tlc.GraphResult) { g.Config.Invariants = []string{"Safe"} },
		"changed successor": func(g *tlc.GraphResult) { g.Graph.Steps[0].To = g.Graph.States[1].ID },
		"lost initial":      func(g *tlc.GraphResult) { g.Graph.Initial = g.Graph.Initial[1:] },
	} {
		t.Run(name, func(t *testing.T) {
			in, dir := reader586EvalFixture(t)
			before := reader586JSON(t, in)
			base := &reader586Dumper{t: t, in: in}
			matrix, err := statechart.EvaluateRules(t.Context(), changingReplay{base, edit}, dir, in.Graph, in.Lock)
			if matrix != nil || err == nil {
				t.Fatalf("contradictory evidence accepted: %v %v", matrix, err)
			}
			if !bytes.Equal(before, reader586JSON(t, in)) {
				t.Fatal("runner changed caller evidence through an alias")
			}
			reader586Clean(t, dir, base)
		})
	}
}

func TestReplayCancellationAfterRunnerReturns(t *testing.T) {
	in, dir := reader586EvalFixture(t)
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("cancel after TLC completion")
	base := &reader586Dumper{t: t, in: in}
	runner := changingReplay{base, func(*tlc.GraphResult) { cancel(cause) }}
	matrix, err := statechart.EvaluateRules(ctx, runner, dir, in.Graph, in.Lock)
	if matrix != nil || !errors.Is(err, cause) {
		t.Fatalf("canceled replay returned evidence: %v %v", matrix, err)
	}
	reader586Clean(t, dir, base)
}

func TestReplayWithoutInvariantsStillChecksPins(t *testing.T) {
	in, dir := reader586EvalFixture(t)
	in.Lock.Statements = slices.DeleteFunc(in.Lock.Statements, func(s project.Statement) bool { return s.Kind == project.Invariant })
	matrix, err := statechart.EvaluateRules(t.Context(), nil, dir, in.Graph, in.Lock)
	if err != nil || len(matrix) != len(in.Graph.Graph.States) {
		t.Fatalf("empty rule matrix: %v %v", matrix, err)
	}
	for _, row := range matrix {
		if len(row) != 0 {
			t.Fatalf("invented invariant: %v", row)
		}
	}
	in.Lock.Statements[0].SHA256 = "sha256:" + strings.Repeat("0", 64)
	if matrix, err := statechart.EvaluateRules(t.Context(), nil, dir, in.Graph, in.Lock); matrix != nil || err == nil {
		t.Fatalf("changed pin accepted without invariants: %v %v", matrix, err)
	}
}

func TestReplayPreservesStructuredStringContents(t *testing.T) {
	for _, changeString := range []bool{false, true} {
		in, dir := reader586EvalFixture(t)
		for i := range in.Graph.Graph.States {
			in.Graph.Graph.States[i].Values["worker"] = `[name |-> "a  b", sequence |-> <<1, TRUE>>, members |-> {1, 2}]`
		}
		base := &reader586Dumper{t: t, in: in}
		runner := changingReplay{base, func(g *tlc.GraphResult) {
			for i := range g.Graph.States {
				value := "[name |-> \"a  b\",\n  sequence |-> <<1, TRUE>>,\n  members |-> {1, 2}]"
				if changeString {
					value = strings.Replace(value, "a  b", "a b", 1)
				}
				g.Graph.States[i].Values["worker"] = value
			}
		}}
		matrix, err := statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock)
		if changeString {
			if matrix != nil || err == nil {
				t.Fatal("string content change was treated as layout")
			}
		} else if matrix == nil || err != nil {
			t.Fatalf("TLC layout change refused: %v", err)
		}
		reader586Clean(t, dir, base)
	}
}

func TestReplayAcceptsPrintedFiniteFunctions(t *testing.T) {
	in, dir := reader586EvalFixture(t)
	for i := range in.Graph.Graph.States {
		in.Graph.Graph.States[i].Values["worker"] = `(1 :> "a" @@ 2 :> "b")`
	}
	base := &reader586Dumper{t: t, in: in}
	runner := changingReplay{base, func(*tlc.GraphResult) {
		source, err := os.ReadFile(base.paths[0])
		if err != nil || !strings.Contains(string(source), "EXTENDS Lifecycle, TLC") {
			t.Fatalf("replay cannot evaluate TLC's printed function operators: %s %v", source, err)
		}
	}}
	matrix, err := statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock)
	if matrix == nil || err != nil {
		t.Fatalf("finite function replay: %v", err)
	}
	reader586Clean(t, dir, base)
}

func TestReportDetachesNestedTemporalData(t *testing.T) {
	in := reader586Input()
	in.Lock.Statements = append(in.Lock.Statements, project.Statement{Name: "Eventually", Kind: project.Property, Says: "Eventually finish.", SHA256: strings.Repeat("e", 64)})
	in.Temporal = []tlc.TraceFile{{Module: in.Graph.Module, Check: "Eventually", TLC: in.Graph.Run.Version, Stutters: true,
		States: []tlc.TraceState{{Index: 1, Action: "Initial predicate", Changed: []string{"phase"}, TLA: map[string]string{"phase": `"idle"`},
			Vars: map[string]any{"nested": []any{map[string]any{"n": int64(9007199254740993)}}}}}}}
	result, err := statechart.Compute(t.Context(), in, []byte(reader586View))
	if err != nil {
		t.Fatal(err)
	}
	want := reader586JSON(t, result)
	if !bytes.Contains(want, []byte("9007199254740993")) {
		t.Fatal("temporal integer rounded")
	}
	in.Temporal[0].States[0].Vars["nested"].([]any)[0].(map[string]any)["n"] = 0
	in.Temporal[0].States[0].TLA["phase"] = `"changed"`
	in.Temporal[0].States[0].Changed[0] = "changed"
	if !bytes.Equal(want, reader586JSON(t, result)) {
		t.Fatal("report retained nested caller data")
	}
}

func TestReplayFreshNamesAndConcurrentFiles(t *testing.T) {
	in, dir := reader586EvalFixture(t)
	// Existing caller files must never be reused as replay output.
	path := filepath.Join(dir, "InvariantReader.tla")
	if err := os.WriteFile(path, []byte("caller-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	for range 3 {
		t.Run("isolated", func(t *testing.T) {
			t.Parallel()
			d := &reader586Dumper{t: t, in: in}
			matrix, err := statechart.EvaluateRules(t.Context(), d, dir, in.Graph, in.Lock)
			if matrix == nil || err != nil {
				t.Fatalf("concurrent replay: %v", err)
			}
			for _, p := range d.paths {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatalf("replay survived: %s: %v", p, err)
				}
			}
			if b, err := os.ReadFile(path); err != nil || string(b) != "caller-owned" {
				t.Fatal("caller file changed")
			}
		})
	}
}

type replayFunc func(context.Context, string, string, tlc.Config) (*tlc.GraphResult, error)

func (f replayFunc) DumpGraph(ctx context.Context, dir, module string, cfg tlc.Config) (*tlc.GraphResult, error) {
	return f(ctx, dir, module, cfg)
}

func TestReplayBindsModelValuesWithoutChangingOriginalConstants(t *testing.T) {
	in, dir := reader586EvalFixture(t)
	const value = `[node |-> r1, text |-> "r1", nodes |-> {r1, r2}]`
	for i := range in.Graph.Graph.States {
		in.Graph.Graph.States[i].Values["worker"] = value
	}
	before := reader586JSON(t, in)
	runner := replayFunc(func(ctx context.Context, dir, module string, cfg tlc.Config) (*tlc.GraphResult, error) {
		b, err := os.ReadFile(filepath.Join(dir, module+".tla"))
		if err != nil {
			t.Fatal(err)
		}
		aliases := map[string]string{}
		for name, value := range cfg.Constants {
			if name == "Workers" {
				if value != "2" {
					t.Fatal("original bound changed")
				}
				continue
			}
			if value != "r1" && value != "r2" {
				t.Fatalf("field name, boolean or literal became a model value: %s=%s", name, value)
			}
			aliases[value] = name
		}
		if len(cfg.Constants) != 3 || len(aliases) != 2 || aliases["r1"] == aliases["r2"] {
			t.Fatalf("model values not bound: %v", cfg.Constants)
		}
		want := `[node |-> ` + aliases["r1"] + `, text |-> "r1", nodes |-> {` + aliases["r1"] + `, ` + aliases["r2"] + `}]`
		if !strings.Contains(string(b), want) {
			t.Fatalf("model value substitution changed structure or text: %s", b)
		}
		var identity, rules string
		for _, name := range tla.Variables(string(b)) {
			if strings.Contains(string(b), name+` = "10"`) {
				identity = name
			} else {
				rules = name
			}
		}
		g := &tlc.GraphResult{Module: module, Config: cfg, Tool: in.Graph.Tool,
			Run: tlc.Result{Version: in.Graph.Run.Version, Outcome: tlc.Passed, Depth: 1}}
		for _, s := range in.Graph.Graph.States {
			values := maps.Clone(s.Values)
			values[identity] = strconv.Quote(s.ID)
			values[rules] = `[Independent |-> TRUE, Safe |-> TRUE]`
			g.Graph.States = append(g.Graph.States, tlc.GraphState{ID: s.ID, Label: "snapshot", Values: values})
			g.Graph.Initial = append(g.Graph.Initial, s.ID)
			g.Graph.Steps = append(g.Graph.Steps, tlc.GraphStep{From: s.ID, To: s.ID, Action: "Stay"})
		}
		g.StateCount, g.StepCount = len(g.Graph.States), len(g.Graph.Steps)
		g.Run.DistinctStates, g.Run.StatesGenerated = int64(g.StateCount), int64(g.StateCount+g.StepCount)
		return g, nil
	})
	matrix, err := statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock)
	if err != nil || len(matrix) != len(in.Graph.Graph.States) {
		t.Fatalf("model value replay: %v %v", matrix, err)
	}
	if !bytes.Equal(before, reader586JSON(t, in)) {
		t.Fatal("model value binding changed original evidence")
	}
	reader586Clean(t, dir, &reader586Dumper{})
}
