package statechart_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// The comparison API is explanatory, not a behavioral equivalence proof:
// Compare(ComparisonSnapshot, ComparisonSnapshot) (*Comparison, error).
// A snapshot has Kind (proposal or ratification), Name, Module, exact Model,
// Includes and View, the captured Lock, and exactly one Chart or Limitation.
// Compare detaches and canonicalizes both snapshots. Comparison has Version,
// Fingerprint, Baseline, Candidate, BaselineIdentity, CandidateIdentity,
// Visible and BeforeHiding deltas, ViewChanged, BoundsChanged, ModelChanged,
// Scope (a qualification when bounds differ) and optional Unavailable.
// Identities contain ModelSHA256, ViewSHA256, Includes (hashes), LockSHA256 and
// ProposalHash. Deltas contain AddedStates/RemovedStates []State,
// AddedArrows/RemovedArrows []Arrow, and ChangedStates/ChangedArrows records
// retaining Before and After evidence for equal identities with changed
// membership, initials, qualifications or occurrence multisets. BeforeHiding
// compares visible plus hidden buckets, ignoring visibility/hiding rule IDs.
// Chart identities are State.ID and (Arrow.From, Label, To), never positions.
// Comparison.Markdown() and Comparison.SVG() (string,error) show the complete
// candidate plus removals, both identities and bounds, and all qualifications.
// SVG groups use data-comparison-side=baseline|candidate; changed state/arrow
// groups use data-change=added|removed, retaining the ordinary data attributes.
// Removed marks are dashed; added and removed marks have distinct colors and
// a visible text legend. No scripts or external references are introduced.

const comparison648View = `{"version":1,"state":[{"id":"ready","when":{"eq":[{"var":"n"},"0"]},"label":"Ready"},{"id":"work","when":{"eq":[{"var":"n"},"1"]},"label":"Working"},{"id":"done","when":true,"label":"Done"}],"step":[{"id":"poll","when":{"eq":[{"action":true},"Poll"]},"label":"Internal poll","hide":true},{"id":"pulse","when":{"eq":[{"action":true},"Pulse"]},"label":"Internal poll","hide":true},{"id":"reset","when":{"eq":[{"action":true},"Reset"]},"label":"Reset"},{"id":"advance","when":true,"label":"Advance"}]}`

func comparison648JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func comparison648Hash(s string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(s))) }

func comparison648Snapshot(t *testing.T, n int, poll, view string) statechart.ComparisonSnapshot {
	t.Helper()
	model := fmt.Sprintf("---- MODULE Compare648 ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nAdvance == /\\ n = 0 /\\ n' = %d\nReset == /\\ n = %d /\\ n' = 0\n%s == /\\ n = %d /\\ UNCHANGED n\nNext == Advance \\/ Reset \\/ %s\nSpec == Init /\\ [][Next]_vars\n====\n", n, n, poll, n, poll)
	pin, err := tla.PinHash(model, "Spec", project.Model)
	if err != nil {
		t.Fatal(err)
	}
	lock := project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Advance, reset or poll.", SHA256: pin}}}
	id := fmt.Sprint(n)
	g := tlc.GraphResult{Module: "Compare648", Config: tlc.Config{Specification: "Spec", Constants: lock.Bounds}, Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)}, Run: tlc.Result{Outcome: tlc.Passed, Version: "TLC fixture", DistinctStates: 2, StatesGenerated: 4, Depth: 2}, StateCount: 2, StepCount: 3,
		Graph: tlc.Graph{States: []tlc.GraphState{{ID: "0", Label: "n = 0", Values: map[string]string{"n": "0"}}, {ID: id, Label: "n = " + id, Values: map[string]string{"n": id}}}, Initial: []string{"0"}, Steps: []tlc.GraphStep{{From: "0", To: id, Action: "Advance"}, {From: id, To: "0", Action: "Reset"}, {From: id, To: id, Action: poll}}}}
	c, err := statechart.Compute(t.Context(), statechart.Input{Graph: g, Lock: lock, Invariants: map[string]map[string]bool{"0": {}, id: {}}}, []byte(view))
	if err != nil {
		t.Fatal(err)
	}
	return statechart.ComparisonSnapshot{Kind: "proposal", Name: fmt.Sprintf("Proposal %d", n), Module: "Compare648", Model: model, View: view, Includes: map[string]string{}, Lock: lock, Chart: c}
}

func TestComparison648CanonicalChanges(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", comparison648View)
	before := comparison648JSON(t, []statechart.ComparisonSnapshot{a, b})
	d, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 || d.Unavailable != nil || !d.ModelChanged || d.ViewChanged || d.BoundsChanged || !strings.HasPrefix(d.Fingerprint, "sha256:") {
		t.Fatalf("bad comparison: %+v", d)
	}
	if len(d.Visible.AddedStates) != 1 || d.Visible.AddedStates[0].Label != "Done" || len(d.Visible.RemovedStates) != 1 || d.Visible.RemovedStates[0].Label != "Working" || len(d.Visible.AddedArrows) != 2 || len(d.Visible.RemovedArrows) != 2 {
		t.Fatalf("lost state or arrow differences: %+v", d.Visible)
	}
	if !reflect.DeepEqual(d.Visible.AddedArrows, b.Chart.Arrows) || !reflect.DeepEqual(d.Visible.RemovedArrows, a.Chart.Arrows) {
		t.Fatal("delta changed labeled endpoints or concrete occurrences")
	}
	if len(d.BeforeHiding.AddedArrows) != 3 || len(d.BeforeHiding.RemovedArrows) != 3 {
		t.Fatalf("hidden differences disappeared: %+v", d.BeforeHiding)
	}
	for _, pair := range []struct {
		s                     statechart.ComparisonSnapshot
		model, view, proposal string
	}{{a, d.BaselineIdentity.ModelSHA256, d.BaselineIdentity.ViewSHA256, d.BaselineIdentity.ProposalHash}, {b, d.CandidateIdentity.ModelSHA256, d.CandidateIdentity.ViewSHA256, d.CandidateIdentity.ProposalHash}} {
		if pair.model != comparison648Hash(pair.s.Model) || pair.view != comparison648Hash(pair.s.View) || pair.proposal != project.ProposalHash(pair.s.Lock.Bounds, pair.s.Lock.Statements) {
			t.Fatal("comparison identifies different source bytes or rules")
		}
	}
	if !bytes.Equal(before, comparison648JSON(t, []statechart.ComparisonSnapshot{a, b})) {
		t.Fatal("comparison mutated source snapshots")
	}
	// Reorder every collection as another complete DOT dump could do.
	slices.Reverse(b.Chart.States)
	slices.Reverse(b.Chart.Arrows)
	slices.Reverse(b.Chart.Report.Hidden)
	for i := range b.Chart.States {
		slices.Reverse(b.Chart.States[i].Members)
	}
	for i := range b.Chart.Arrows {
		slices.Reverse(b.Chart.Arrows[i].Members)
	}
	again, err := statechart.Compare(a, b)
	if err != nil || !bytes.Equal(comparison648JSON(t, d), comparison648JSON(t, again)) {
		t.Fatalf("dump order changed the comparison: %v", err)
	}
	unchanged, err := statechart.Compare(b, b)
	if err != nil || len(unchanged.Visible.AddedStates)+len(unchanged.Visible.RemovedStates)+len(unchanged.Visible.AddedArrows)+len(unchanged.Visible.RemovedArrows)+len(unchanged.Visible.ChangedStates)+len(unchanged.Visible.ChangedArrows) != 0 {
		t.Fatalf("unchanged graph invented differences: %+v %v", unchanged, err)
	}
	var restored statechart.Comparison
	if err := json.Unmarshal(comparison648JSON(t, d), &restored); err != nil || !reflect.DeepEqual(d, &restored) {
		t.Fatalf("comparison did not round trip: %v", err)
	}
	b.Chart.States[0].Label = "caller changed it"
	if !bytes.Equal(before, comparison648JSON(t, []statechart.ComparisonSnapshot{d.Baseline, d.Candidate})) {
		t.Fatal("saved comparison aliases its caller")
	}
}

func TestComparison648ViewHidingAndScope(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 1, "Poll", strings.ReplaceAll(comparison648View, "Working", "Work <&>"))
	d, err := statechart.Compare(a, b)
	if err != nil || !d.ViewChanged || d.ModelChanged || len(d.Visible.AddedStates) != 1 || len(d.Visible.RemovedStates) != 1 {
		t.Fatalf("state relabeling was not a view change: %+v %v", d, err)
	}
	b = comparison648Snapshot(t, 1, "Poll", strings.ReplaceAll(comparison648View, `"label":"Advance"`, `"label":"Proceed"`))
	d, err = statechart.Compare(a, b)
	if err != nil || !d.ViewChanged || len(d.Visible.AddedArrows) != 1 || len(d.Visible.RemovedArrows) != 1 {
		t.Fatalf("arrow relabeling disappeared: %+v %v", d, err)
	}
	b = comparison648Snapshot(t, 1, "Poll", strings.ReplaceAll(comparison648View, `"hide":true`, `"hide":false`))
	d, err = statechart.Compare(a, b)
	if err != nil || !d.ViewChanged || d.ModelChanged || len(d.Visible.AddedArrows) != 1 || len(d.BeforeHiding.AddedArrows)+len(d.BeforeHiding.RemovedArrows)+len(d.BeforeHiding.ChangedArrows) != 0 {
		t.Fatalf("unhiding was mistaken for new model behavior: %+v %v", d, err)
	}
	// A different hidden model action maps to the SAME reader arrow. Its
	// occurrence change must survive even though visible topology is equal.
	b = comparison648Snapshot(t, 1, "Pulse", comparison648View)
	d, err = statechart.Compare(a, b)
	if err != nil || !d.ModelChanged || len(d.BeforeHiding.ChangedArrows) != 1 {
		t.Fatalf("hiding erased a model step change: %+v %v", d, err)
	}
	if !strings.Contains(string(comparison648JSON(t, d.BeforeHiding.ChangedArrows)), "Poll") || !strings.Contains(string(comparison648JSON(t, d.BeforeHiding.ChangedArrows)), "Pulse") {
		t.Fatal("changed occurrence lost its old or new action")
	}
	b = comparison648Snapshot(t, 1, "Poll", comparison648View)
	b.Lock.Bounds = map[string]string{"Limit": "3"}
	b.Chart.Report.Source.Config.Constants = map[string]string{"Limit": "3"}
	d, err = statechart.Compare(a, b)
	if err != nil || !d.BoundsChanged || !strings.Contains(strings.ToLower(d.Scope), "scope") {
		t.Fatalf("bound change has no scope qualification: %+v %v", d, err)
	}
	if d.Baseline.Lock.Bounds["Limit"] != "2" || d.Candidate.Lock.Bounds["Limit"] != "3" {
		t.Fatal("comparison rewrote a checking bound")
	}
	missing := a
	missing.Chart = nil
	if got, err := statechart.Compare(missing, b); err == nil || got != nil {
		t.Fatal("missing comparison source became an empty graph")
	}
	large := a
	large.Chart = nil
	large.Limitation = &statechart.Limitation{Code: "too_large", Actual: 501, Limit: 500, Source: a.Chart.Report.Source}
	d, err = statechart.Compare(large, b)
	if err != nil || d.Unavailable == nil || len(d.Visible.AddedStates)+len(d.Visible.RemovedStates)+len(d.Visible.AddedArrows)+len(d.Visible.RemovedArrows) != 0 || d.Candidate.Chart == nil {
		t.Fatalf("size limitation invented graph differences or lost the complete candidate: %+v %v", d, err)
	}
}

func TestComparison648MermaidSVGAnnotations(t *testing.T) {
	a := comparison648Snapshot(t, 1, "Poll", comparison648View)
	b := comparison648Snapshot(t, 2, "Poll", strings.ReplaceAll(comparison648View, "Done", "Done <&>"))
	b.Lock.Bounds = map[string]string{"Limit": "3"}
	b.Chart.Report.Source.Config.Constants = map[string]string{"Limit": "3"}
	d, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	before := comparison648JSON(t, d)
	md := d.Markdown()
	svg, err := d.SVG()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{a.Name, b.Name, d.BaselineIdentity.ModelSHA256, d.CandidateIdentity.ModelSHA256, d.BaselineIdentity.ViewSHA256, d.CandidateIdentity.ViewSHA256, "Working", "Ready", "Limit"} {
		if !strings.Contains(html.UnescapeString(md), s) || !strings.Contains(html.UnescapeString(svg), s) {
			t.Errorf("comparison presentation lacks %q", s)
		}
	}
	for _, s := range []string{"view change", "scope", "added", "removed"} {
		if !strings.Contains(strings.ToLower(md), s) || !strings.Contains(strings.ToLower(svg), s) {
			t.Errorf("formats disagree about %s", s)
		}
	}
	if !strings.Contains(md, b.Chart.Markdown()) {
		t.Fatal("comparison no longer includes the complete candidate Mermaid and report")
	}
	dec := xml.NewDecoder(strings.NewReader(svg))
	addedStates, removedStates, addedArrows, removedArrows := 0, 0, 0, 0
	var dash bool
	var visibleSVG strings.Builder
	var changes []string
	textDepth := 0
	colors := map[string]map[string]bool{"added": {}, "removed": {}}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if end, ok := tok.(xml.EndElement); ok {
			if end.Name.Local == "text" {
				textDepth--
			}
			changes = changes[:len(changes)-1]
			continue
		}
		if txt, ok := tok.(xml.CharData); ok && textDepth > 0 {
			visibleSVG.Write(txt)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if el.Name.Local == "script" || el.Name.Local == "foreignObject" {
			t.Fatal("active SVG content")
		}
		attrs := map[string]string{}
		for _, a := range el.Attr {
			attrs[a.Name.Local] = a.Value
			if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") || ((a.Name.Local == "href" || a.Name.Local == "src") && !strings.HasPrefix(a.Value, "#")) {
				t.Fatal("active or external SVG reference")
			}
		}
		change := ""
		if len(changes) > 0 {
			change = changes[len(changes)-1]
		}
		if attrs["data-change"] != "" {
			change = attrs["data-change"]
		}
		changes = append(changes, change)
		if el.Name.Local == "text" {
			textDepth++
		}
		if change == "added" || change == "removed" {
			for _, key := range []string{"fill", "stroke"} {
				if value := attrs[key]; value != "" && value != "none" {
					colors[change][value] = true
				}
			}
		}
		if change == "removed" && attrs["stroke-dasharray"] != "" {
			dash = true
		}
		if attrs["data-state-id"] != "" {
			if attrs["data-change"] == "added" {
				addedStates++
			}
			if attrs["data-change"] == "removed" {
				removedStates++
			}
		}
		if attrs["data-from"] != "" {
			if attrs["data-change"] == "added" {
				addedArrows++
			}
			if attrs["data-change"] == "removed" {
				removedArrows++
			}
		}
	}
	if addedStates != 1 || removedStates != 1 || addedArrows != 2 || removedArrows != 2 || !dash {
		t.Fatalf("annotations do not describe the real delta: states +%d -%d arrows +%d -%d dashed=%v", addedStates, removedStates, addedArrows, removedArrows, dash)
	}
	if len(colors["added"]) == 0 || len(colors["removed"]) == 0 || reflect.DeepEqual(colors["added"], colors["removed"]) {
		t.Fatal("added and removed annotations lack distinct visible colors")
	}
	for _, word := range []string{"added", "removed", "view change", "scope", a.Name, b.Name} {
		if !strings.Contains(strings.ToLower(visibleSVG.String()), strings.ToLower(word)) {
			t.Errorf("visible SVG legend/report lacks %q", word)
		}
	}
	if !bytes.Equal(before, comparison648JSON(t, d)) {
		t.Fatal("rendering mutated saved evidence")
	}
}
