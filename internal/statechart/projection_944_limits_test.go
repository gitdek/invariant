package statechart_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

func TestProjection944ExactWorkCeilings(t *testing.T) {
	t.Run("steps", func(t *testing.T) {
		in := projection944Input(t, 1, false)
		in.Graph.Graph.Steps = make([]tlc.GraphStep, 1000000)
		for i := range in.Graph.Graph.Steps {
			in.Graph.Graph.Steps[i] = tlc.GraphStep{From: "0", To: "0", Action: "N"}
		}
		projection944Counts(&in)
		view := strings.Replace(projection944All, `"label":"Continue"`, `"label":"Continue","hide":true`, 1)
		_, err := statechart.Compute(t.Context(), in, []byte(view))
		var refusal *statechart.Refusal
		if !errors.As(err, &refusal) || refusal.Code != "empty_outgoing" {
			t.Fatalf("exact step ceiling must reach runtime validation: %v", err)
		}
		in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, in.Graph.Graph.Steps[0])
		projection944Counts(&in)
		_, err = statechart.Compute(t.Context(), in, []byte(view))
		if !errors.As(err, &refusal) || refusal.Limitation == nil || refusal.Limitation.Dimension != "steps" || refusal.Actual != 1000001 {
			t.Fatalf("one excess occurrence: %v", err)
		}
	})
	t.Run("cells", func(t *testing.T) {
		for _, size := range []struct{ states, rules int }{{100, 10000}, {101, 9901}} {
			in := projection944Input(t, size.states, false)
			in.Lock.Statements = in.Lock.Statements[:1]
			for i := 0; i < size.rules; i++ {
				in.Lock.Statements = append(in.Lock.Statements, project.Statement{Name: fmt.Sprintf("Rule%d", i), Kind: project.Invariant, Says: "Every cell is checked.", SHA256: strings.Repeat("a", 64)})
			}
			for _, s := range in.Graph.Graph.States {
				row := map[string]bool{}
				for _, rule := range in.Lock.Statements[1:] {
					row[rule.Name] = true
				}
				in.Invariants[s.ID] = row
			}
			view := strings.Replace(projection944All, `"when":true`, `"when":false`, 1)
			_, err := statechart.Compute(t.Context(), in, []byte(view))
			var refusal *statechart.Refusal
			if !errors.As(err, &refusal) {
				t.Fatal(err)
			}
			if size.states*size.rules == 1000000 {
				if refusal.Code != "unmapped_state" {
					t.Fatalf("exact cell ceiling skipped runtime validation: %v", err)
				}
			} else if refusal.Limitation == nil || refusal.Limitation.Dimension != "invariant_cells" || refusal.Actual != 1000001 {
				t.Fatalf("one excess cell: %v", err)
			}
		}
	})
}

type replay944Stop struct{ calls int }

var replay944Stopped = errors.New("replay reached after complete byte admission")

func (r *replay944Stop) DumpGraph(context.Context, string, string, tlc.Config) (*tlc.GraphResult, error) {
	r.calls++
	return nil, replay944Stopped
}

func TestProjection944ReplayByteCeiling(t *testing.T) {
	in := projection944Input(t, 2001, false)
	model := strings.Replace(projection944Model, "CONSTANT Limit", "CONSTANT Limit, Pad", 1)
	for i := range in.Lock.Statements {
		st := &in.Lock.Statements[i]
		var err error
		st.SHA256, err = tla.PinHash(model, st.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Projection944.tla"), []byte(model), 0600); err != nil {
		t.Fatal(err)
	}
	pad := 22 << 20
	setPad := func(n int) {
		in.Lock.Bounds["Pad"] = `"` + strings.Repeat("p", n) + `"`
		in.Graph.Config.Constants = in.Lock.Bounds
	}
	setPad(pad)
	runner := &replay944Stop{}
	_, err := statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock)
	var refusal *statechart.Refusal
	if !errors.As(err, &refusal) || refusal.Limitation == nil || refusal.Limitation.Dimension != "replay_bytes" || runner.calls != 0 {
		t.Fatalf("aggregate preflight: %v calls=%d", err, runner.calls)
	}
	// Three batches repeat the configuration three times. A harmless space
	// in one state's value adjusts the remainder by one generated UTF-8 byte.
	delta := refusal.Actual - statechart.MaxReplayBytes
	reduce := (delta + 2) / 3
	setPad(pad - reduce)
	padding := reduce*3 - delta
	in.Graph.Graph.States[0].Values["n"] = "0" + strings.Repeat(" ", padding)
	_, err = statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock)
	if !errors.Is(err, replay944Stopped) || runner.calls != 1 {
		t.Fatalf("exact replay byte ceiling: %v calls=%d", err, runner.calls)
	}
	in.Graph.Graph.States[0].Values["n"] += " "
	runner.calls = 0
	_, err = statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock)
	if !errors.As(err, &refusal) || refusal.Actual != statechart.MaxReplayBytes+1 || refusal.Limit != statechart.MaxReplayBytes || runner.calls != 0 {
		t.Fatalf("one replay byte over: %v calls=%d", err, runner.calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "Projection944.tla" {
		t.Fatal("replay cleanup", err)
	}
}

func TestProjection944OutputByteCeiling(t *testing.T) {
	in := projection944Input(t, 1, false)
	source := statechart.Source{Module: in.Graph.Module, Config: in.Graph.Config, Tool: in.Graph.Tool, TLC: in.Graph.Run.Version}
	for _, representation := range []string{"compact_json", "indented_json", "markdown", "svg", "animated_poster", "still_poster"} {
		t.Run(representation, func(t *testing.T) {
			for _, extra := range []int{0, 1} {
				var value any = struct{}{}
				markdown := func() string { return "" }
				renders := []func() (string, error){nil, nil, nil}
				n := statechart.MaxEvidenceBytes + extra
				text := strings.Repeat("x", n)
				switch representation {
				case "compact_json":
					value = text[:n-2]
				case "indented_json":
					value = text[:n-3]
				case "markdown":
					markdown = func() string { return text }
				default:
					i := 0
					if representation == "animated_poster" {
						i = 1
					}
					if representation == "still_poster" {
						i = 2
					}
					renders[i] = func() (string, error) { return text, nil }
				}
				l, err := statechart.EvidenceLimit(value, source, 1, 1, "projection", markdown, renders[0], renders[1:]...)
				if err != nil {
					t.Fatal(err)
				}
				if representation == "compact_json" && extra == 0 {
					// Compact JSON fits exactly; its required newline is the
					// first excess byte in the subsequent representation.
					if l == nil || l.Representation != "indented_json" || l.Actual != n+1 {
						t.Fatal("compact boundary did not advance to indented JSON", l)
					}
				} else if extra == 0 {
					if l != nil {
						t.Fatal("inclusive output boundary", l)
					}
				} else if l == nil || l.Actual != n || l.Representation != representation || l.Validate() != nil {
					t.Fatal("exact output overflow", l)
				}
			}
		})
	}
}

func TestProjection944CompanionByteCeilingAndBindings(t *testing.T) {
	in := projection944Input(t, 1, false)
	c := projection944Chart(t, in, projection944All)
	s := statechart.ComparisonSnapshot{Kind: "proposal", Name: "Complete candidate", Module: in.Graph.Module,
		Model: projection944Model, Includes: map[string]string{}, View: projection944All, Lock: in.Lock, Chart: c}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// Whitespace is retained original payload, and counts toward the complete
	// entry's ceiling just as indentation and its final newline do.
	canonical := data
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	l := c.OutputLimit("indented_json", "comparison", statechart.MaxEvidenceBytes+1)
	data = append(data, []byte(strings.Repeat(" ", statechart.MaxEvidenceBytes-len(data)))...)
	ref := statechart.NewEvidenceReference("reader-candidate.json", data, fingerprint, l)
	if _, err := ref.Snapshot("reader-candidate.json", data); err != nil {
		t.Fatal("exact companion ceiling", err)
	}
	data = append(data, ' ')
	ref = statechart.NewEvidenceReference("reader-candidate.json", data, fingerprint, l)
	if _, err := ref.Snapshot("reader-candidate.json", data); err == nil {
		t.Fatal("one excess companion byte was accepted")
	}
	ref = statechart.NewEvidenceReference("reader-candidate.json", canonical, fingerprint, l)
	if err := ref.ValidateChart(c); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source", "concrete", "projected"} {
		copy := *ref
		limitation := *l
		copy.Limitation = &limitation
		switch field {
		case "source":
			limitation.Source.Module = "Other"
		case "concrete":
			limitation.ConcreteStates++
		case "projected":
			projected := 2
			limitation.ProjectedStates = &projected
		}
		if err := copy.ValidateChart(c); err == nil {
			t.Fatal("unbound reference", field)
		}
	}
	var chained map[string]any
	if err := json.Unmarshal(canonical, &chained); err != nil {
		t.Fatal(err)
	}
	chained["candidate_ref"] = ref
	data, _ = json.Marshal(chained)
	ref = statechart.NewEvidenceReference("reader-candidate.json", data, fingerprint, l)
	if _, err := ref.Snapshot("reader-candidate.json", data); err == nil {
		t.Fatal("nested companion reference was accepted")
	}
}
