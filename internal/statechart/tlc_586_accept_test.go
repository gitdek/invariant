package statechart_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// EvaluateRules(context.Context, GraphDumper, dir string, graph tlc.GraphResult,
//               lock project.Lock) (map[string]map[string]bool, error)
// GraphDumper has the existing tlc.Runner.DumpGraph signature; tlc.Runner must
// satisfy it directly. The caller supplies the same immutable model snapshot
// used for the graph. Validate the graph, bounds and locked statement pins
// before evaluation. Generate a temporary replay module extending the source
// module, initializing every original variable from each graph state's exact
// TLA+ values, and carrying two fresh auxiliary variables: the original
// fingerprint as a string and a record of invariant-name -> invariant VALUE
// evaluated by TLC. Replay has only an explicit unchanged loop per snapshot.
// Auxiliary names must avoid source declarations; no input module is edited.
// The resulting graph is evaluation evidence only, never the reader's graph.
//
// The adapter calls DumpGraph once, retaining the original constants, using
// its own replay specification and no INVARIANT/PROPERTY checks (otherwise a
// false invariant could stop evaluation). It inherits the runner's admission,
// isolation, limits, provenance and joined cancellation cleanup unchanged.
// Match replay states to original identities and values, require a complete
// boolean record for every locked invariant and state, refuse duplicates,
// unknowns, inconsistent tool provenance and failed results, and clean up all
// generated files on every exit. Return no matrix on refusal. No invariant
// formula is translated to Go or entrusted to the view. Temporal properties
// are not evaluated as state formulas. A lock without invariants can return
// an empty row per state without a TLC replay.

const reader586Model = `---- MODULE Lifecycle ----
EXTENDS Naturals
CONSTANT Workers
VARIABLES phase, worker
vars == <<phase, worker>>
Init == /\ phase = "idle"
        /\ worker \in 0..(Workers - 1)
Next == UNCHANGED vars
Spec == Init /\ [][Next]_vars
Safe == phase # "busy" \/ worker = 1
Independent == phase # "busy" \/ worker = 0
EventuallyDone == TRUE ~> TRUE
====
`

func reader586EvalFixture(t *testing.T) (statechart.Input, string) {
	t.Helper()
	in := reader586Input()
	in.Lock.Statements = append(in.Lock.Statements, project.Statement{Name: "Independent", Kind: project.Invariant, Says: "A different rule."}, project.Statement{Name: "EventuallyDone", Kind: project.Property, Says: "An eventual fact."})
	for i := range in.Lock.Statements {
		s := &in.Lock.Statements[i]
		hash, err := tla.PinHash(reader586Model, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		s.SHA256 = hash
	}
	for _, row := range in.Invariants {
		row["Independent"] = true
	}
	in.Invariants["20"]["Safe"] = false
	in.Invariants["21"]["Independent"] = false
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Lifecycle.tla"), []byte(reader586Model), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "caller.txt"), []byte("caller-owned\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return in, dir
}

// This fake is at the existing graph API boundary, so no Docker executable,
// command flags, global environment or existing TLC fake needs changing.
// It validates the request's meaning and returns the shape that real TLC's
// replay dump has. The real integration test checks the generated TLA+ with
// the pinned tool as well; these checks are not a substitute for that case.
type reader586Dumper struct {
	t      *testing.T
	in     statechart.Input
	mode   string
	calls  int
	paths  []string
	result *tlc.GraphResult
}

func (d *reader586Dumper) DumpGraph(ctx context.Context, dir, module string, cfg tlc.Config) (*tlc.GraphResult, error) {
	d.t.Helper()
	d.calls++
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, module+".tla")
	d.paths = append(d.paths, path)
	src, err := os.ReadFile(path)
	if err != nil {
		d.t.Fatalf("replay not available to TLC: %v", err)
	}
	if module == d.in.Graph.Module || cfg.Specification == d.in.Graph.Config.Specification || cfg.Specification == "" || !reflect.DeepEqual(cfg.Constants, d.in.Graph.Config.Constants) || len(cfg.Invariants) != 0 || len(cfg.Properties) != 0 {
		d.t.Fatalf("wrong replay configuration: module=%s cfg=%+v", module, cfg)
	}
	if !regexp.MustCompile(`(?m)^EXTENDS\s+[^\n]*\bLifecycle\b`).Match(src) {
		d.t.Fatalf("replay does not use the original definitions: %s", src)
	}
	if _, err := tla.Definition(string(src), cfg.Specification); err != nil {
		d.t.Fatal(err)
	}
	aux := tla.Variables(string(src))
	if len(aux) != 2 {
		d.t.Fatalf("replay must carry identity and evaluated rules: %v", aux)
	}
	var idVar, rulesVar string
	for _, name := range aux {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*=\s*"10"`).Match(src) {
			idVar = name
		} else {
			rulesVar = name
		}
		if slices.Contains(tla.Variables(reader586Model), name) || slices.Contains(tla.Definitions(reader586Model), name) {
			d.t.Fatalf("replay shadows source declaration %s", name)
		}
	}
	if idVar == "" || rulesVar == "" {
		d.t.Fatalf("replay identity is missing: %s", src)
	}
	for _, s := range d.in.Graph.Graph.States {
		if !strings.Contains(string(src), strconv.Quote(s.ID)) {
			d.t.Errorf("replay omitted concrete ID %s", s.ID)
		}
		for name, value := range s.Values {
			if !strings.Contains(string(src), name) || !strings.Contains(string(src), value) {
				d.t.Errorf("replay omitted %s = %s", name, value)
			}
		}
	}
	for _, s := range d.in.Lock.Statements {
		if s.Kind == project.Invariant && !regexp.MustCompile(`\b`+regexp.QuoteMeta(s.Name)+`\s*\|->\s*`+regexp.QuoteMeta(s.Name)+`\b`).Match(src) {
			d.t.Errorf("rule %s is not evaluated by TLC: %s", s.Name, src)
		}
	}
	if strings.Contains(string(src), "EventuallyDone") {
		d.t.Fatal("temporal property treated as a state formula")
	}
	if !strings.Contains(string(src), "UNCHANGED") {
		d.t.Fatal("snapshot replay has no unchanged successor")
	}
	if d.mode == "tool error" {
		return nil, errors.New("TLC evaluation failed")
	}
	g := &tlc.GraphResult{Module: module, Config: cfg, Tool: d.in.Graph.Tool, Run: tlc.Result{Version: d.in.Graph.Run.Version, Outcome: tlc.Passed, Depth: 1}}
	for i, s := range d.in.Graph.Graph.States {
		values := map[string]string{}
		for k, v := range s.Values {
			values[k] = v
		}
		values[idVar] = strconv.Quote(s.ID)
		var fields []string
		for name, holds := range d.in.Invariants[s.ID] {
			value := "FALSE"
			if holds {
				value = "TRUE"
			}
			fields = append(fields, name+" |-> "+value)
		}
		slices.Sort(fields)
		values[rulesVar] = "[" + strings.Join(fields, ", ") + "]"
		id := fmt.Sprint(1000 + i)
		g.Graph.States = append(g.Graph.States, tlc.GraphState{ID: id, Label: "snapshot", Values: values})
		g.Graph.Initial = append(g.Graph.Initial, id)
		g.Graph.Steps = append(g.Graph.Steps, tlc.GraphStep{From: id, Action: "ReplayStay", To: id})
	}
	switch d.mode {
	case "missing state":
		g.Graph.States = g.Graph.States[:len(g.Graph.States)-1]
		g.Graph.Initial = g.Graph.Initial[:len(g.Graph.Initial)-1]
		g.Graph.Steps = g.Graph.Steps[:len(g.Graph.Steps)-1]
	case "duplicate identity":
		g.Graph.States[1].Values[idVar] = g.Graph.States[0].Values[idVar]
	case "unknown identity":
		g.Graph.States[0].Values[idVar] = `"999"`
	case "mismatched variables":
		g.Graph.States[0].Values["worker"] = "99"
	case "missing rule":
		g.Graph.States[0].Values[rulesVar] = `[Safe |-> TRUE]`
	case "extra rule":
		g.Graph.States[0].Values[rulesVar] = `[Safe |-> TRUE, Independent |-> TRUE, Invented |-> FALSE]`
	case "nonboolean rule":
		g.Graph.States[0].Values[rulesVar] = `[Safe |-> "TRUE", Independent |-> TRUE]`
	case "malformed rules":
		g.Graph.States[0].Values[rulesVar] = `[Safe |-> TRUE`
	case "failed result":
		g.Run.Outcome = tlc.Failed
	case "different tool":
		g.Tool.JarSHA256 = strings.Repeat("f", 64)
	}
	g.StateCount = len(g.Graph.States)
	g.StepCount = len(g.Graph.Steps)
	g.Run.DistinctStates = int64(g.StateCount)
	g.Run.StatesGenerated = int64(g.StateCount + g.StepCount)
	d.result = g
	return g, nil
}

func reader586Clean(t *testing.T, dir string, d *reader586Dumper) {
	t.Helper()
	for _, p := range d.paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("replay artifact survived: %s: %v", p, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"Lifecycle.tla", "caller.txt"}) {
		t.Fatalf("evaluation changed caller directory: %v", names)
	}
	b, err := os.ReadFile(filepath.Join(dir, "caller.txt"))
	if err != nil || string(b) != "caller-owned\n" {
		t.Fatalf("caller data changed: %q %v", b, err)
	}
}

func TestReader586TLCRuleAdapter(t *testing.T) {
	in, dir := reader586EvalFixture(t)
	before := reader586JSON(t, in)
	d := &reader586Dumper{t: t, in: in}
	matrix, err := statechart.EvaluateRules(t.Context(), d, dir, in.Graph, in.Lock)
	if err != nil || !reflect.DeepEqual(matrix, in.Invariants) || d.calls != 1 {
		t.Fatalf("TLC rule evidence: %v, %v, calls=%d", matrix, err, d.calls)
	}
	if !bytes.Equal(before, reader586JSON(t, in)) {
		t.Fatal("evaluation mutated source evidence")
	}
	b, err := os.ReadFile(filepath.Join(dir, "Lifecycle.tla"))
	if err != nil || string(b) != reader586Model {
		t.Fatalf("source module edited: %v", err)
	}
	reader586Clean(t, dir, d)
	in.Invariants = matrix
	out := reader586Compute(t, in, reader586View)
	if len(out.States) != 4 {
		t.Fatalf("TLC-evaluated rule distinctions disappeared: %+v", out.States)
	}
	// Matrix results are independent of the runner's returned mutable graph.
	for key := range d.result.Graph.States[0].Values {
		d.result.Graph.States[0].Values[key] = "corrupted"
	}
	if !matrix["10"]["Safe"] || matrix["20"]["Safe"] || matrix["21"]["Independent"] {
		t.Fatalf("matrix aliases runner evidence: %v", matrix)
	}
}

func TestReader586TLCRuleAdapterRefusals(t *testing.T) {
	for _, mode := range []string{"tool error", "missing state", "duplicate identity", "unknown identity", "mismatched variables", "missing rule", "extra rule", "nonboolean rule", "malformed rules", "failed result", "different tool"} {
		t.Run(mode, func(t *testing.T) {
			in, dir := reader586EvalFixture(t)
			d := &reader586Dumper{t: t, in: in, mode: mode}
			matrix, err := statechart.EvaluateRules(t.Context(), d, dir, in.Graph, in.Lock)
			if matrix != nil || err == nil {
				t.Fatalf("bad evaluation supplied rule facts: %v %v", matrix, err)
			}
			reader586Clean(t, dir, d)
		})
	}
	t.Run("changed statement pin", func(t *testing.T) {
		in, dir := reader586EvalFixture(t)
		d := &reader586Dumper{t: t, in: in}
		changed := strings.Replace(reader586Model, `Safe == phase # "busy" \/ worker = 1`, `Safe == TRUE`, 1)
		if changed == reader586Model {
			t.Fatal("fixture failed to change the rule")
		}
		if err := os.WriteFile(filepath.Join(dir, "Lifecycle.tla"), []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		matrix, err := statechart.EvaluateRules(t.Context(), d, dir, in.Graph, in.Lock)
		if matrix != nil || err == nil || d.calls != 0 {
			t.Fatalf("changed pin reached evaluation: %v %v calls=%d", matrix, err, d.calls)
		}
		reader586Clean(t, dir, d)
	})
	t.Run("canceled", func(t *testing.T) {
		in, dir := reader586EvalFixture(t)
		d := &reader586Dumper{t: t, in: in}
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("stop rule evaluation")
		cancel(cause)
		matrix, err := statechart.EvaluateRules(ctx, d, dir, in.Graph, in.Lock)
		if matrix != nil || !errors.Is(err, cause) || d.calls != 0 {
			t.Fatalf("canceled replay: %v %v calls=%d", matrix, err, d.calls)
		}
		reader586Clean(t, dir, d)
	})
}
