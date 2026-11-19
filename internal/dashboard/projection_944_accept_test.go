package dashboard

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

func dashboard944Fixture(t *testing.T, n int, label string) reader631Fixture {
	t.Helper()
	// Preserve #631's hand-built historical limitations unchanged. This new
	// fixture replaces only its own outcome with a fresh full computation.
	f := reader631FixtureFor(t, n, label)
	source := f.Report.Reader.Limitation.Source
	g := tlc.GraphResult{Module: source.Module, Config: source.Config, Tool: source.Tool, Run: tlc.Result{Version: source.TLC, Outcome: tlc.Passed, Depth: n}, Graph: tlc.Graph{Initial: []string{"0"}}}
	truth := map[string]map[string]bool{}
	for i := 0; i < n; i++ {
		id := strconv.Itoa(i)
		action := "Advance"
		if i == n-1 {
			action = "Reset"
		}
		g.Graph.States = append(g.Graph.States, tlc.GraphState{ID: id, Label: "n = " + id, Values: map[string]string{"n": id}})
		g.Graph.Steps = append(g.Graph.Steps, tlc.GraphStep{From: id, To: strconv.Itoa((i + 1) % n), Action: action}, tlc.GraphStep{From: id, To: id, Action: "Poll"})
		truth[id] = map[string]bool{"Safe": true}
	}
	g.StateCount = n
	g.StepCount = len(g.Graph.Steps)
	g.Run.DistinctStates = int64(n)
	g.Run.StatesGenerated = int64(g.StepCount + 1)
	c, err := statechart.Compute(t.Context(), statechart.Input{Graph: g, Lock: f.Lock, Invariants: truth}, f.Files[".invariant/view.json"])
	if err != nil || c == nil {
		t.Fatalf("fresh %d-state archive fixture must draw: %v", n, err)
	}
	f.Report.Reader.Chart = c
	f.Report.Reader.Limitation = nil
	f.bind(t)
	return f
}

func dashboard944Zip(t *testing.T, dir string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatal("unexpected archive directory")
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 60<<20 {
			t.Fatal("entry consumed archive wrapper headroom", entry.Name(), len(data))
		}
		w, err := z.Create("reader/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.UncompressedSize64 > 64<<20 {
			t.Fatal("archive limit increased")
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, r)
		closed := r.Close()
		if err != nil || closed != nil {
			t.Fatal(err, closed)
		}
	}
	return b.Bytes()
}

func TestReader944ArchiveAndSavedDraftRoundTrips(t *testing.T) {
	a := dashboard944Fixture(t, 601, "Before <&>")
	b := dashboard944Fixture(t, 702, "Candidate <&>")
	comparison, err := statechart.Compare(receipt723Snapshot(a, "Captured baseline"), receipt723Snapshot(b, "Candidate"))
	if err != nil || comparison.Unavailable != nil {
		t.Fatal("large projections did not compare", err)
	}
	final := dashboard944Fixture(t, 803, "Final <&>")
	final.Report.Reader.Comparison = comparison
	final.Files[".invariant/comparison.json"] = reader631JSON(t, comparison)
	final.bind(t)
	for i, f := range []reader631Fixture{a, final} {
		dir := t.TempDir()
		before := reader631JSON(t, f.Report)
		if err := receipt.Write(dir, f.Report); err != nil {
			t.Fatal(err)
		}
		raw := dashboard944Zip(t, dir)
		set, err := readReceiptZip(raw)
		if err != nil {
			t.Fatal("production archive reader refused admitted evidence", err)
		}
		if !sameReaderJSON(set.reports["reader"], f.Report) || !bytes.Equal(before, reader631JSON(t, f.Report)) {
			t.Fatal("archive round trip changed complete receipt")
		}
		r := reader631Repo(t, fmt.Sprintf("owner/projection%d", i), strings.Repeat("a", 40), 944, []reader631Fixture{f}, raw)
		s := &Server{Repos: []*Repo{r}, Cache: t.TempDir(), Branch: "main"}
		ps := reader631Load(t, s, r)
		p := reader631Find(t, ps, r.Name, f.Dir)
		if p.Status != "chart" || p.SVG == "" {
			t.Fatalf("large evidence did not reach dashboard: %s %s", p.Status, p.Message)
		}
	}
	// Actual exported preview receipt, through the same ZIP reader.
	tc, _ := reader603Tools(t)
	draft := preview639Draft(t, 1, nil)
	preview639Input(draft, 0, 0)["model"].(map[string]any)["bounds"] = map[string]string{"Limit": "500"}
	ws := t.TempDir()
	preview639Write(t, ws, draft)
	p := preview639Checked(t, ws, tc)
	out := t.TempDir()
	if err := p.WritePreviews(out); err != nil {
		t.Fatal(err)
	}
	set, err := readReceiptZip(dashboard944Zip(t, filepath.Join(out, "previews", "F1", "A")))
	if err != nil {
		t.Fatal(err)
	}
	if set.reports["reader"].Reader.Chart == nil || set.reports["reader"].Reader.Chart.Report.Counts["tlc_states"] != 501 {
		t.Fatal("preview archive lost newly drawable projection")
	}
	// Saved draft download and restart retain the original bytes, including
	// whitespace, rather than remarshal to the new format.
	result := saved897Proposal(t, true, 500, nil)
	f := saved897FixtureFor(t, result)
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	if w.Reader == nil || w.Reader.Status != "chart" {
		t.Fatal("saved draft still limits 501 concrete states")
	}
	saved897Evidence(t, f.s, w.SavedDraft, f.payload)
	cold := f.restart(t, saved897Author)
	saved897Evidence(t, cold, w.SavedDraft, f.payload)
	var record map[string]any
	if err := json.Unmarshal(f.payload, &record); err != nil {
		t.Fatal(err)
	}
}

func dashboard944Dense(t *testing.T, steps int) reader631Fixture {
	t.Helper()
	f := reader631FixtureFor(t, 1, "Dense <&>")
	source := f.Report.Reader.Chart.Report.Source
	action := strings.Repeat("N", 480) + ` <&> "quoted"`
	g := tlc.GraphResult{Module: source.Module, Config: source.Config, Tool: source.Tool,
		StateCount: 1, StepCount: steps, Run: tlc.Result{Version: source.TLC, Outcome: tlc.Passed, Depth: 1, DistinctStates: 1, StatesGenerated: int64(steps + 1)},
		Graph: tlc.Graph{Initial: []string{"0"}, States: []tlc.GraphState{{ID: "0", Label: "n = 0", Values: map[string]string{"n": "0"}}}}}
	for range steps {
		g.Graph.Steps = append(g.Graph.Steps, tlc.GraphStep{From: "0", To: "0", Action: action})
	}
	c, err := statechart.Compute(t.Context(), statechart.Input{Graph: g, Lock: f.Lock, Invariants: map[string]map[string]bool{"0": {"Safe": true}}}, f.Files[".invariant/view.json"])
	if err != nil || c == nil {
		t.Fatalf("independent dense chart must fit: %v", err)
	}
	f.Report.Reader.Chart = c
	f.Report.Design.StatesGenerated = int64(steps + 1)
	f.bind(t)
	if svg, err := c.SVG(); err != nil || len(svg) > 60<<20 {
		t.Fatal("independent dense SVG must fit", err)
	}
	return f
}

func TestReader944DenseComparisonVolumeArchives(t *testing.T) {
	a := dashboard944Dense(t, 20000)
	b := dashboard944Dense(t, 20001)
	c, err := statechart.Compare(receipt723Snapshot(a, "Dense baseline"), receipt723Snapshot(b, "Dense candidate"))
	if err != nil || c == nil || c.Unavailable == nil || c.Unavailable.Code != "too_large" {
		t.Fatal("combined repeated comparison evidence must become successful volume limitation", err)
	}
	if c.Candidate.Chart == nil {
		t.Fatal("comparison overflow discarded independently fitting candidate")
	}
	if len(c.Visible.ChangedArrows) != 0 || len(c.BeforeHiding.ChangedArrows) != 0 || len(c.Visible.AddedArrows) != 0 || len(c.BeforeHiding.AddedArrows) != 0 {
		t.Fatal("volume limitation retained a partial delta")
	}
	if err := c.Validate(); err != nil {
		t.Fatal("volume evidence failed canonical validation", err)
	}
	encoded := reader631JSON(t, c)
	if !bytes.Contains(encoded, []byte(`"evidence_volume"`)) || !bytes.Contains(encoded, []byte(`"output_bytes"`)) {
		t.Fatal("comparison volume is not fingerprint-bound structured evidence")
	}
	f := dashboard944Dense(t, 20002)
	f.Report.Reader.Comparison = c
	f.Files[".invariant/comparison.json"] = encoded
	f.bind(t)
	before := reader631JSON(t, f.Report)
	dir := t.TempDir()
	if err := receipt.Write(dir, f.Report); err != nil {
		t.Fatal("combined volume became archive failure", err)
	}
	set, err := readReceiptZip(dashboard944Zip(t, dir))
	if err != nil {
		t.Fatal("admitted volume archive refused", err)
	}
	r := set.reports["reader"]
	if r == nil || !r.Passed || r.Fingerprint != f.Report.Fingerprint || r.Reader == nil || r.Reader.Chart == nil || r.Reader.Chart.Report.Counts["shown_steps"] != 20002 {
		t.Fatal("volume overflow erased independent final evidence or changed verdict")
	}
	if !bytes.Equal(before, reader631JSON(t, f.Report)) {
		t.Fatal("writer mutated or resealed caller's evidence")
	}
}
