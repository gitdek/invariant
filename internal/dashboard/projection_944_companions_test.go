package dashboard

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

func TestProjection944ReceiptCompanions(t *testing.T) {
	f := reader631FixtureFor(t, 1, "Dense duplicates")
	source := f.Report.Reader.Chart.Report.Source
	const count = 200000
	g := tlc.GraphResult{Module: source.Module, Config: source.Config, Tool: source.Tool,
		StateCount: 1, StepCount: count, Run: tlc.Result{Version: source.TLC, Outcome: tlc.Passed, Depth: 1, DistinctStates: 1, StatesGenerated: count + 1},
		Graph: tlc.Graph{Initial: []string{"0"}, States: []tlc.GraphState{{ID: "0", Label: "n = 0", Values: map[string]string{"n": "0"}}}, Steps: make([]tlc.GraphStep, count)}}
	for i := range g.Graph.Steps {
		g.Graph.Steps[i] = tlc.GraphStep{From: "0", To: "0", Action: "N"}
	}
	chart, err := statechart.Compute(t.Context(), statechart.Input{Graph: g, Lock: f.Lock, Invariants: map[string]map[string]bool{"0": {"Safe": true}}}, f.Files[".invariant/view.json"])
	if err != nil {
		t.Fatal(err)
	}
	f.Report.Reader.Chart = chart
	f.Report.Design.StatesGenerated = count + 1
	f.bind(t)
	baselineSnapshot, candidateSnapshot := receipt723Snapshot(f, "Baseline"), receipt723Snapshot(f, "Candidate")
	baselineSnapshot.Model += "\n\\* " + strings.Repeat("x", 20<<20) + "\n"
	candidateSnapshot.Model = baselineSnapshot.Model
	c, err := statechart.Compare(baselineSnapshot, candidateSnapshot)
	if err != nil || c.Volume() == nil {
		t.Fatal("complete comparison must require bounded companions", err)
	}
	f.Report.Reader.Comparison = c
	f.bind(t)
	before := reader631JSON(t, f.Report)
	dir := t.TempDir()
	if err := receipt.Write(dir, f.Report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reader-comparison.json", "reader-baseline.json", "reader-candidate.json"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || len(b) > statechart.MaxEvidenceBytes {
			t.Fatalf("%s: %v", name, err)
		}
	}
	set, err := readReceiptZip(dashboard944Zip(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !sameReaderJSON(set.reports["reader"], f.Report) || !bytes.Equal(before, reader631JSON(t, f.Report)) {
		t.Fatal("companion round trip changed evidence or identities")
	}
	baseline, err := os.ReadFile(filepath.Join(dir, "reader-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []string{"missing", "tampered", "swapped", "linked"} {
		t.Run(damage, func(t *testing.T) {
			file := filepath.Join(dir, "reader-baseline.json")
			if err := os.WriteFile(file, baseline, 0600); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "tampered":
				if err := os.WriteFile(file, append(append([]byte{}, baseline...), ' '), 0600); err != nil {
					t.Fatal(err)
				}
			case "swapped":
				candidate, err := os.ReadFile(filepath.Join(dir, "reader-candidate.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, candidate, 0600); err != nil {
					t.Fatal(err)
				}
			}
			raw := dashboard944Zip(t, dir)
			if damage == "linked" {
				zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
				if err != nil {
					t.Fatal(err)
				}
				var archive bytes.Buffer
				zw := zip.NewWriter(&archive)
				for _, f := range zr.File {
					header := f.FileHeader
					if header.Name == "reader/reader-baseline.json" {
						header.SetMode(os.ModeSymlink | 0600)
					}
					w, err := zw.CreateHeader(&header)
					if err != nil {
						t.Fatal(err)
					}
					r, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					_, err = io.Copy(w, r)
					r.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
				raw = archive.Bytes()
			}
			set, err := readReceiptZip(raw)
			if err != nil {
				t.Fatal(err)
			}
			r := set.reports["reader"]
			if r.Reader.Chart == nil || r.Reader.Chart.Report.Counts["shown_steps"] != count || r.Fingerprint != f.Report.Fingerprint || r.Reader.ComparisonDiagnostic == nil || r.Reader.ComparisonDiagnostic.Code != "comparison_evidence" {
				t.Fatal("damaged comparison erased independent final evidence")
			}
		})
	}
	if err := receipt.Write(dir, reader631FixtureFor(t, 1, "Small").Report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reader-comparison.json", "reader-baseline.json", "reader-candidate.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("stale companion", name)
		}
	}
}

func TestProjection944VolumeKeepsLimitedAndDrawableSources(t *testing.T) {
	limited := reader631FixtureFor(t, 501, "Historical limitation")
	drawable := reader631FixtureFor(t, 1, "Checked final")
	a, b := receipt723Snapshot(limited, "Limited source"), receipt723Snapshot(drawable, "Drawable source")
	comment := "\n\\* " + strings.Repeat("x", 32<<20) + "\n"
	a.Model += comment
	b.Model += comment
	for _, finalLimited := range []bool{false, true} {
		baseline, candidate := a, b
		final := limited
		if !finalLimited {
			baseline, candidate = b, a
			final = drawable
		}
		c, err := statechart.Compare(baseline, candidate)
		if err != nil || c.Volume() == nil || c.Volume().Actual <= statechart.MaxEvidenceBytes {
			t.Fatal("combined limited/drawable comparison lost its exact output limit", err)
		}
		if !sameReaderJSON(c.Baseline.Limitation, baseline.Limitation) || !sameReaderJSON(c.Candidate.Limitation, candidate.Limitation) ||
			!sameReaderJSON(c.Baseline.Chart, baseline.Chart) || !sameReaderJSON(c.Candidate.Chart, candidate.Chart) {
			t.Fatal("combined overflow changed independent outcomes")
		}
		final.Report.Reader.Comparison = c
		final.bind(t)
		dir := t.TempDir()
		if err := receipt.Write(dir, final.Report); err != nil {
			t.Fatal(err)
		}
		set, err := readReceiptZip(dashboard944Zip(t, dir))
		if err != nil || !sameReaderJSON(set.reports["reader"], final.Report) {
			t.Fatal("archive lost the independent final outcome or comparison", err)
		}
		if err := os.Remove(filepath.Join(dir, "reader-comparison.json")); err != nil {
			t.Fatal(err)
		}
		set, err = readReceiptZip(dashboard944Zip(t, dir))
		if err != nil {
			t.Fatal(err)
		}
		got := set.reports["reader"]
		if !sameReaderJSON(got.Reader.Chart, final.Report.Reader.Chart) || !sameReaderJSON(got.Reader.Limitation, final.Report.Reader.Limitation) ||
			got.Reader.ComparisonDiagnostic == nil || got.Reader.ComparisonDiagnostic.Code != "comparison_evidence" || got.Fingerprint != final.Report.Fingerprint {
			t.Fatal("missing comparison companion changed the final outcome")
		}
	}
}
