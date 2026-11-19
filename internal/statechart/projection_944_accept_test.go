package statechart_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// #944's fixed policy (MiB is 1<<20 bytes; every ceiling is inclusive):
//
//  * 500 projected partitions, by label AND violated-invariant set;
//  * 100,000 concrete states, 1,000,000 dumped step occurrences, and
//    1,000,000 state/locked-invariant cells before replay;
//  * 64 MiB projection input: compact encoding/json of the envelope below,
//    including an all-false invariant matrix reservation, not a sample;
//  * 64 MiB aggregate generated replay module/config UTF-8 bytes, across all
//    batches, counted before running the first replay batch;
//  * 60 MiB for EACH complete emitted representation, including JSON
//    indentation/newline, Markdown, SVG metadata AND report, animated/still
//    posters, preview wrappers, both comparison snapshots and both deltas,
//    and comparison-plus-final receipt/reader files; 4 MiB of the existing
//    64 MiB archive-entry capacity is left unused, not granted to charts.
//
// The input envelope is {graph:tlc.GraphResult,lock:project.Lock,view:string,
// temporal:[]tlc.TraceFile,invariants:map[stateID]map[invariantName]false}.
// Use encoding/json's normal escaping, no indent, and no trailing newline.
// Source files and includes remain pinned and checked as before; exact
// source text, repeated records, escapes and wrappers count wherever emitted.
// Stream counts when practical; do not allocate the all-false reservation.
// No extra state, arrow, label or source-text threshold is authorized.
//
// Precedence: cancellation/cleanup, static view and complete graph/provenance/
// lock validation, concrete ceiling, step ceiling, invariant-cell ceiling,
// input bytes, source-pin/replay-byte validation and replay, complete runtime
// view/hiding checks, projected count, emitted bytes. Runtime errors cannot
// turn into size success. Over a work/input ceiling, no replay, lookup,
// mapping or hiding runs, and the projected count remains unknown.
// At one output stage, choose the first overflow in this order: compact JSON,
// indented JSON plus newline, Markdown, native SVG, animated poster, still
// poster; check baseline then candidate before the combined comparison, and
// the independent final outcome before the comparison-plus-final stack.
// Render errors are errors, not volume limitations. Aggregate replay size
// counts every emitted byte in every batch even when names/data are repeated.
//
// New Limitation JSON retains code/actual/limit/source and adds kind,
// concrete_states, projected_states (omitted if unknown), view_checked, and
// for evidence_volume, dimension/representation/side/unit. Kinds are exactly
// projected_states, concrete_states, evidence_volume. View_checked is explicit
// on new limitations and absent on legacy ones. Dimensions are steps,
// invariant_cells, input_bytes, replay_bytes, output_bytes; units are
// occurrences, cells, bytes respectively. Actual is the exact complete size,
// not ceiling+1, and representation names the actual encoding/file measured.
// Source retains module/config/bounds/tool/TLC identity for every kind.
// New limitations have a shared Validate() error method, used by every
// consumer; validation must also bind to the enclosing source/counts.
// Legacy absence of kind means the original concrete>500 policy, no projected
// count or checked-view claim. Never upgrade or reseal historical evidence.
//
// An output overflow never returns a partial chart/delta. A comparison volume
// record binds both exact source identities/scopes and the overflow, retains
// independently fitting candidate/baseline evidence, and has no delta. If a
// receipt's comparison-plus-final stack exceeds 60 MiB, retain the complete
// final outcome and use a limitation-only, digest-bound reference to the
// unchanged independently fitting comparison in reader-comparison.json.
// A comparison that needs to retain individually fitting snapshots separately
// uses reader-baseline.json and reader-candidate.json under its volume record.
// Only these fixed regular siblings are allowed, never caller-selected paths;
// missing, swapped or stale companions refuse the affected comparison, while
// receipt readers retain an independently valid final outcome under the
// existing comparison_evidence diagnostic and companion checks. Every entry must
// independently satisfy 60 MiB; reference chains/cycles are forbidden. Small
// ordinary/preview/comparison records and their fingerprints never gain these
// fields or companions. Rendering/loading saved bytes never normalizes them.
// Full and compact factory publication keep 60,000 comment bytes and 4 MiB
// expanded markers, and the existing configured-dashboard-only fallback.
// The builder's additional regression suite must exercise each exact byte
// ceiling and one byte above it (including replay generation), the exact step
// and invariant-cell ceilings and one occurrence/cell above, and maximum
// admitted ordinary/preview/comparison/final records with dense duplicates,
// hidden steps, long names and escaped strings. Exercise missing/tampered
// volume companions through publication, archive reading, synthesis, saved
// draft downloads and restart, retaining independently fitting candidate/final
// records, exact saved bytes and original hashes. These are required work in
// this PR, not permission to defer boundaries to another issue.

const projection944Model = `---- MODULE Projection944 ----
EXTENDS Naturals
CONSTANT Limit
VARIABLE n
Init == n = 0
N == IF n < Limit THEN n' = n + 1 ELSE n' = 0
Next == N
Safe == n \in 0..Limit
Spec == Init /\ [][Next]_n
====
`

const projection944View = `{"version":1,"state":[{"id":"zero","when":{"eq":[{"var":"n"},"0"]},"label":"Ready"},{"id":"one","when":{"eq":[{"var":"n"},"1"]},"label":"Working"},{"id":"rest","when":true,"label":"Done"}],"step":[{"id":"poll","when":{"eq":[{"action":true},"Poll"]},"label":"Poll","hide":true},{"id":"all","when":true,"label":"Continue"}]}`
const projection944All = `{"version":1,"state":[{"id":"all","when":true,"label":"All"}],"step":[{"id":"all","when":true,"label":"Continue"}]}`

func projection944Input(t *testing.T, n int, polls bool) statechart.Input {
	t.Helper()
	lock := project.Lock{Bounds: map[string]string{"Limit": strconv.Itoa(n - 1)}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Advance and reset."},
		{Name: "Safe", Kind: project.Invariant, Says: "Stay in the bounded interval."},
	}}
	for i := range lock.Statements {
		var err error
		lock.Statements[i].SHA256, err = tla.PinHash(projection944Model, lock.Statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	in := statechart.Input{Lock: lock, Invariants: map[string]map[string]bool{}, Graph: tlc.GraphResult{
		Module: "Projection944", Config: tlc.Config{Specification: "Spec", Constants: lock.Bounds},
		Tool:  tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
		Run:   tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", Depth: n},
		Graph: tlc.Graph{Initial: []string{"0"}},
	}}
	for i := 0; i < n; i++ {
		id := strconv.Itoa(i)
		in.Graph.Graph.States = append(in.Graph.Graph.States, tlc.GraphState{ID: id, Label: "n = " + id, Values: map[string]string{"n": id}})
		in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, tlc.GraphStep{From: id, To: strconv.Itoa((i + 1) % n), Action: "N"})
		if polls {
			in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, tlc.GraphStep{From: id, To: id, Action: "Poll"})
		}
		in.Invariants[id] = map[string]bool{"Safe": true}
	}
	if polls {
		in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, tlc.GraphStep{From: "0", To: "0", Action: "Poll"})
	}
	projection944Counts(&in)
	return in
}

func projection944Counts(in *statechart.Input) {
	in.Graph.StateCount = len(in.Graph.Graph.States)
	in.Graph.StepCount = len(in.Graph.Graph.Steps)
	in.Graph.Run.DistinctStates = int64(in.Graph.StateCount)
	in.Graph.Run.StatesGenerated = int64(in.Graph.StepCount + len(in.Graph.Graph.Initial))
}

func projection944Chart(t *testing.T, in statechart.Input, view string) *statechart.Result {
	t.Helper()
	c, err := statechart.Compute(t.Context(), in, []byte(view))
	if err != nil || c == nil {
		t.Fatalf("complete %d-state projection must draw: %v", in.Graph.StateCount, err)
	}
	return c
}

func projection944Accounting(t *testing.T, in statechart.Input, c *statechart.Result) {
	t.Helper()
	owners, initials := map[string]int{}, map[string]int{}
	steps := map[tlc.GraphStep]int{}
	for _, s := range c.States {
		for _, id := range s.Members {
			owners[id]++
		}
		for _, id := range s.InitialMembers {
			initials[id]++
		}
	}
	for _, s := range in.Graph.Graph.States {
		if owners[s.ID] != 1 {
			t.Fatalf("member %s: %d owners", s.ID, owners[s.ID])
		}
	}
	if len(owners) != in.Graph.StateCount || len(initials) != len(in.Graph.Graph.Initial) {
		t.Fatal("lost or invented membership")
	}
	for _, id := range in.Graph.Graph.Initial {
		if initials[id] != 1 {
			t.Fatal("lost initial", id)
		}
	}
	for _, a := range append(append([]statechart.Arrow{}, c.Arrows...), c.Report.Hidden...) {
		if a.Count != len(a.Members) {
			t.Fatal("wrong occurrence count")
		}
		for _, s := range a.Members {
			steps[s]++
		}
	}
	want := map[tlc.GraphStep]int{}
	for _, s := range in.Graph.Graph.Steps {
		want[s]++
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatal("lost duplicate/hidden step or invented shortcut")
	}
	if c.Report.Counts["tlc_states"] != int64(in.Graph.StateCount) || c.Report.Counts["tlc_steps"] != int64(in.Graph.StepCount) {
		t.Fatal("wrong complete counts")
	}
}

func TestProjection944CompleteLargeChart(t *testing.T) {
	in := projection944Input(t, 24760, true)
	c := projection944Chart(t, in, projection944View)
	if len(c.States) != 3 || c.Report.Counts["shown_steps"] != 24760 || c.Report.Counts["hidden_steps"] != 24761 {
		t.Fatal("projection did not retain all visible and hidden occurrences")
	}
	projection944Accounting(t, in, c)
	if _, err := c.SVG(); err != nil {
		t.Fatal("concrete membership still blocks SVG:", err)
	}
	for _, animated := range []bool{false, true} {
		if _, err := c.Poster(statechart.PosterOptions{Title: "Complete exploration", Animated: animated}); err != nil {
			t.Fatal(err)
		}
	}
	// Equal labels must not merge a safe state with a violating one.
	in.Invariants["24759"]["Safe"] = false
	c = projection944Chart(t, in, projection944All)
	if len(c.States) != 2 {
		t.Fatal("violated invariant set was omitted from partition")
	}
	projection944Accounting(t, in, c)
}

func TestProjection944RuntimeFailuresPrecedeSize(t *testing.T) {
	in := projection944Input(t, 601, true)
	cases := []struct{ name, view, code string }{
		{"lookup", strings.ReplaceAll(projection944View, `"var":"n"`, `"var":"absent"`), "evaluation"},
		{"state totality", strings.Replace(projection944View, `"id":"rest","when":true`, `"id":"rest","when":false`, 1), "unmapped_state"},
		{"step totality", strings.Replace(projection944View, `"id":"all","when":true`, `"id":"all","when":false`, 1), "unmapped_step"},
		{"partial", strings.ReplaceAll(projection944View, `"label":"Poll"`, `"label":"Continue"`), "partial_arrow"},
		{"disconnect", strings.Replace(projection944View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1), "unreachable_chart"},
		{"last outgoing", strings.Replace(projection944All, `"label":"Continue"`, `"label":"Continue","hide":true`, 1), "empty_outgoing"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, err := statechart.Compute(t.Context(), in, []byte(tt.view))
			var r *statechart.Refusal
			if c != nil || !errors.As(err, &r) || r.Code != tt.code {
				t.Fatalf("want %s, no chart; got %v", tt.code, err)
			}
		})
	}
	broken := projection944Input(t, 601, false)
	broken.Graph.Graph.Initial = []string{"1"}
	broken.Graph.Graph.Steps[600].To = "1"
	projection944Counts(&broken)
	c, err := statechart.Compute(t.Context(), broken, []byte(projection944All))
	var r *statechart.Refusal
	if c != nil || !errors.As(err, &r) || r.Code != "invalid_graph" {
		t.Fatalf("disconnected concrete graph became size success: %v", err)
	}
}

func projection944Partition(n int) string {
	var b strings.Builder
	b.WriteString(`{"version":1,"state":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"s%d","when":{"eq":[{"var":"n"},"%d"]},"label":"State %d"}`, i, i, i)
	}
	b.WriteString(`,{"id":"rest","when":true,"label":"State 0"}],"step":[{"id":"all","when":true,"label":"Continue"}]}`)
	return b.String()
}

// This double is only for the no-replay branches. Full replay is exercised by
// the existing protocol fakes in formalize/verify and by the real-tool test.
type projection944Dumper struct {
	graph tlc.GraphResult
	calls int
}

func (d *projection944Dumper) DumpGraph(_ context.Context, _, module string, cfg tlc.Config) (*tlc.GraphResult, error) {
	d.calls++
	if d.calls != 1 || module != d.graph.Module {
		return nil, fmt.Errorf("unexpected invariant replay above work ceiling")
	}
	d.graph.Config = cfg
	return &d.graph, nil
}

func projection944Wire(t *testing.T, l *statechart.Limitation) map[string]any {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestProjection944ProjectedAndConcreteBoundaries(t *testing.T) {
	in := projection944Input(t, 1000, false)
	c := projection944Chart(t, in, projection944Partition(500))
	if len(c.States) != 500 || !c.Report.OverArrowTarget {
		t.Fatal("inclusive projected capacity or arrow advice changed")
	}
	projection944Accounting(t, in, c)
	c, err := statechart.Compute(t.Context(), in, []byte(projection944Partition(501)))
	var r *statechart.Refusal
	if c != nil || !errors.As(err, &r) || r.Code != "too_large" || r.Actual != 501 || r.Limit != 500 {
		t.Fatalf("want exact projected 501 of 1000, got %v", err)
	}
	projection944Chart(t, projection944Input(t, 100000, false), projection944All)
	large := projection944Input(t, 100001, false)
	d := &projection944Dumper{graph: large.Graph}
	c, l, err := statechart.Read(t.Context(), d, t.TempDir(), large.Graph.Module, large.Lock, []byte(strings.ReplaceAll(projection944View, `"var":"n"`, `"var":"absent"`)), 100001)
	if err != nil || c != nil || l == nil || d.calls != 1 {
		t.Fatalf("over-work ceiling: chart=%v limitation=%v calls=%d error=%v", c != nil, l, d.calls, err)
	}
	m := projection944Wire(t, l)
	if m["kind"] != "concrete_states" || m["actual"] != float64(100001) || m["limit"] != float64(100000) || m["concrete_states"] != float64(100001) || m["view_checked"] != false || m["projected_states"] != nil {
		t.Fatalf("guessed projection or wrong ceiling: %v", m)
	}
	if !reflect.DeepEqual(l.Source.Config.Constants, large.Lock.Bounds) || l.Source.Config.Specification != "Spec" || !reflect.DeepEqual(l.Source.Config.Invariants, []string{"Safe"}) || l.Source.Tool != large.Graph.Tool {
		t.Fatal("limitation lost provenance")
	}
	for _, damage := range []string{"counts", "tool", "static"} {
		g := large.Graph
		view := projection944View
		switch damage {
		case "counts":
			g.Run.DistinctStates--
		case "tool":
			g.Tool.JarSHA256 = ""
		case "static":
			view = `{"version":1,"state":[],"step":[]}`
		}
		d := &projection944Dumper{graph: g}
		c, l, err := statechart.Read(t.Context(), d, t.TempDir(), g.Module, large.Lock, []byte(view), 100001)
		if err == nil || c != nil || l != nil {
			t.Fatal("invalid evidence hidden by ceiling:", damage)
		}
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("cancel projection")
	cancel(cause)
	c, l, err = statechart.Read(ctx, &projection944Dumper{graph: large.Graph}, t.TempDir(), large.Graph.Module, large.Lock, []byte(projection944All), 100001)
	if c != nil || l != nil || !errors.Is(err, cause) {
		t.Fatal("cancellation became size success")
	}
}

func TestProjection944LargeComparison(t *testing.T) {
	a := projection944Input(t, 7296, true)
	b := projection944Input(t, 17792, true)
	base := statechart.ComparisonSnapshot{Kind: "proposal", Name: "captured baseline", Module: a.Graph.Module, Model: projection944Model, Includes: map[string]string{}, View: projection944View, Lock: a.Lock, Chart: projection944Chart(t, a, projection944View)}
	view := strings.Replace(projection944View, `"hide":true`, `"hide":false`, 1)
	candidate := statechart.ComparisonSnapshot{Kind: "proposal", Name: "candidate", Module: b.Graph.Module, Model: projection944Model, Includes: map[string]string{}, View: view, Lock: b.Lock, Chart: projection944Chart(t, b, view)}
	c, err := statechart.Compare(base, candidate)
	if err != nil || c == nil || c.Unavailable != nil {
		t.Fatalf("large fitting comparison: %v", err)
	}
	if !c.BoundsChanged || !c.ViewChanged || c.Scope == "" || len(c.Visible.AddedArrows) != 3 || len(c.BeforeHiding.AddedArrows) != 0 || len(c.BeforeHiding.ChangedArrows) != 2 || len(c.Visible.ChangedStates) != 1 {
		t.Fatalf("incorrect visible/hidden differences: visible=%+v before hiding=%+v", c.Visible, c.BeforeHiding)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SVG(); err != nil {
		t.Fatal(err)
	}
	var round statechart.Comparison
	data, _ := json.Marshal(c)
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	if err := round.Validate(); err != nil || round.Fingerprint != c.Fingerprint {
		t.Fatalf("round trip: %v", err)
	}
	round.Candidate.Chart.Report.Counts["tlc_states"]--
	if round.Validate() == nil {
		t.Fatal("tampered large comparison validated")
	}
}

func TestProjection944VolumeAndLegacyValidation(t *testing.T) {
	// Dense occurrences count even if all are hidden or duplicate the same edge.
	in := projection944Input(t, 1, false)
	in.Graph.Graph.Steps = make([]tlc.GraphStep, 1000001)
	for i := range in.Graph.Graph.Steps {
		in.Graph.Graph.Steps[i] = tlc.GraphStep{From: "0", To: "0", Action: `long <&> "escaped" action`}
	}
	projection944Counts(&in)
	d := &projection944Dumper{graph: in.Graph}
	c, l, err := statechart.Read(t.Context(), d, t.TempDir(), in.Graph.Module, in.Lock, []byte(projection944All), 1)
	if err != nil || c != nil || l == nil || d.calls != 1 {
		t.Fatalf("step overflow must skip replay successfully: %v", err)
	}
	m := projection944Wire(t, l)
	for k, want := range map[string]any{"kind": "evidence_volume", "dimension": "steps", "actual": float64(1000001), "limit": float64(1000000), "unit": "occurrences", "view_checked": false, "concrete_states": float64(1)} {
		if m[k] != want {
			t.Fatalf("%s=%v, want %v", k, m[k], want)
		}
	}
	if m["projected_states"] != nil {
		t.Fatal("work overflow guessed projected count")
	}
	// Validate must be the shared public boundary, not just a fingerprint test.
	v, ok := any(l).(interface{ Validate() error })
	if !ok || v.Validate() != nil {
		t.Fatal("new volume limitation needs shared validation")
	}
	for _, edit := range []func(map[string]any){
		func(m map[string]any) { m["kind"] = "unknown" },
		func(m map[string]any) { m["limit"] = 1000001 },
		func(m map[string]any) { m["actual"] = 1 },
		func(m map[string]any) { m["projected_states"] = 1 },
		func(m map[string]any) { m["view_checked"] = true },
	} {
		copy := projection944Wire(t, l)
		edit(copy)
		data, _ := json.Marshal(copy)
		var bad statechart.Limitation
		if err := json.Unmarshal(data, &bad); err != nil {
			continue
		}
		if any(&bad).(interface{ Validate() error }).Validate() == nil {
			t.Fatalf("accepted contradictory limitation: %s", data)
		}
	}
	legacy := &statechart.Limitation{Code: "too_large", Actual: 501, Limit: 500, Source: l.Source}
	before, _ := json.Marshal(legacy)
	if any(legacy).(interface{ Validate() error }).Validate() != nil {
		t.Fatal("legacy limitation lost original semantics")
	}
	after, _ := json.Marshal(legacy)
	if string(before) != string(after) || projection944Wire(t, legacy)["kind"] != nil {
		t.Fatal("legacy evidence was upgraded")
	}
	// A new projected limitation may have Actual != concrete states.
	data, _ := json.Marshal(map[string]any{"code": "too_large", "kind": "projected_states", "actual": 501, "limit": 500, "concrete_states": 1000, "projected_states": 501, "view_checked": true, "source": l.Source})
	var projected statechart.Limitation
	if err := json.Unmarshal(data, &projected); err != nil {
		t.Fatal(err)
	}
	if any(&projected).(interface{ Validate() error }).Validate() != nil {
		t.Fatal("projected and concrete dimensions were conflated")
	}
}

// Keep the complete replay source available to tests using the public reader.
func projection944WriteModel(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Projection944.tla"), []byte(projection944Model), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func projection944InputBytes(t *testing.T, in statechart.Input, view string) int {
	t.Helper()
	reservation := map[string]map[string]bool{}
	for _, s := range in.Graph.Graph.States {
		row := map[string]bool{}
		for _, st := range in.Lock.Statements {
			if st.Kind == project.Invariant {
				row[st.Name] = false
			}
		}
		reservation[s.ID] = row
	}
	b, err := json.Marshal(struct {
		Graph      tlc.GraphResult            `json:"graph"`
		Lock       project.Lock               `json:"lock"`
		View       string                     `json:"view"`
		Temporal   []tlc.TraceFile            `json:"temporal"`
		Invariants map[string]map[string]bool `json:"invariants"`
	}{in.Graph, in.Lock, view, in.Temporal, reservation})
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

func TestProjection944InputByteBoundary(t *testing.T) {
	in := projection944Input(t, 1, false)
	// Printed text is retained graph provenance but does not become a chart
	// label: this isolates input admission from output-volume admission.
	in.Graph.Run.Printed = []string{""}
	const ceiling = 64 << 20
	in.Graph.Run.Printed[0] = strings.Repeat("x", ceiling-projection944InputBytes(t, in, projection944All))
	if got := projection944InputBytes(t, in, projection944All); got != ceiling {
		t.Fatalf("fixture has %d bytes", got)
	}
	projection944Chart(t, in, projection944All)
	in.Graph.Run.Printed[0] += "x"
	c, err := statechart.Compute(t.Context(), in, []byte(projection944All))
	var r *statechart.Refusal
	if c != nil || !errors.As(err, &r) || r.Code != "too_large" || r.Actual != ceiling+1 || r.Limit != ceiling {
		t.Fatalf("input ceiling+1 was not an exact successful size outcome: %v", err)
	}
	// JSON escaping, not source string length, determines input volume.
	in.Graph.Run.Printed[0] = strings.Repeat("<", (ceiling-projection944InputBytes(t, projection944Input(t, 1, false), projection944All))/6+2)
	want := projection944InputBytes(t, in, projection944All)
	c, err = statechart.Compute(t.Context(), in, []byte(projection944All))
	if want <= ceiling || c != nil || !errors.As(err, &r) || r.Code != "too_large" || r.Actual != want {
		t.Fatalf("escaped input was undercounted: actual bytes=%d error=%v", want, err)
	}
}

func TestProjection944EscapedOutputVolume(t *testing.T) {
	in := projection944Input(t, 2, false)
	// Exact input fits, but repetitions in metadata, visible occurrence report
	// and escaping make the complete SVG exceed the emitted-evidence ceiling.
	in.Graph.Graph.Steps[0].Action = strings.Repeat(`<>&"`, 2<<20)
	if n := projection944InputBytes(t, in, projection944All); n > 64<<20 {
		t.Fatalf("output fixture exceeds input ceiling: %d", n)
	}
	c, err := statechart.Compute(t.Context(), in, []byte(projection944All))
	var r *statechart.Refusal
	if c != nil || !errors.As(err, &r) || r.Code != "too_large" || r.Limit != 60<<20 || r.Actual <= r.Limit {
		t.Fatalf("full serialized evidence escaped admission: result=%v error=%v", c != nil, err)
	}
	// Complete runtime validation still has precedence over output size.
	bad := strings.Replace(projection944All, `"id":"all","when":true,"label":"Continue"`, `"id":"all","when":false,"label":"Continue"`, 1)
	c, err = statechart.Compute(t.Context(), in, []byte(bad))
	if c != nil || !errors.As(err, &r) || r.Code != "unmapped_step" {
		t.Fatalf("output volume hid a runtime view failure: %v", err)
	}
}
