package dashboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

func TestProjection944CompanionPresentationRestart(t *testing.T) {
	a := reader631FixtureFor(t, 1, "Captured baseline")
	b := reader631FixtureFor(t, 501, "Captured candidate")
	baseline, candidate := receipt723Snapshot(a, "Baseline"), receipt723Snapshot(b, "Candidate")
	comment := "\n\\* " + strings.Repeat("x", 31<<20) + "\n"
	baseline.Model += comment
	candidate.Model += comment
	c, err := statechart.Compare(baseline, candidate)
	if err != nil || c.Volume() == nil {
		t.Fatal("comparison must need bounded companions", err)
	}
	captured, companions, err := statechart.ComparisonFiles(c)
	if err != nil || len(companions) != 2 {
		t.Fatal("comparison must retain both snapshots separately", err)
	}
	for _, outcome := range []string{"chart", "too_large", "missing_view"} {
		t.Run(outcome, func(t *testing.T) {
			f := dashboard944Fixture(t, 501, "Independent final")
			if outcome == "too_large" {
				checked, projected := true, 501
				f.Report.Reader.Limitation = &statechart.Limitation{Code: "too_large", Kind: "projected_states",
					Actual: projected, Limit: 500, ConcreteStates: 501, ProjectedStates: &projected, ViewChecked: &checked,
					Source: f.Report.Reader.Chart.Report.Source}
				f.Report.Reader.Chart = nil
				f.Files[".invariant/view.json"] = []byte(reader944PartitionView(501))
			}
			if outcome == "missing_view" {
				f.Report.Reader.Chart = nil
				f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: "missing_view", Message: "No saved reader view."}
				delete(f.Files, ".invariant/view.json")
			}
			f.Report.Reader.Comparison = c
			f.Files[".invariant/comparison.json"] = captured
			for name, data := range companions {
				f.Files[".invariant/"+name] = data
			}
			f.bind(t)
			dir := t.TempDir()
			if err := receipt.Write(dir, f.Report); err != nil {
				t.Fatal(err)
			}
			readerBytes, err := os.ReadFile(filepath.Join(dir, "reader.json"))
			if err != nil {
				t.Fatal(err)
			}
			var wire verify.Reader
			if err := json.Unmarshal(readerBytes, &wire); err != nil || wire.ComparisonRef == nil {
				t.Fatal("archive must render the reference representation", err)
			}
			r := reader631Repo(t, receipt723RepoName, receipt723Commit, 944, []reader631Fixture{f}, dashboard944Zip(t, dir))
			s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
			p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
			if p.Status != outcome {
				t.Fatalf("healthy saved representation: %s %s", p.Status, p.Message)
			}
			page := receipt723HTTP(t, s, p.Page)
			cold := receipt723Restart(t, s, p, f)
			if !bytes.Equal(page, receipt723HTTP(t, cold, p.Page)) {
				t.Fatal("offline restart changed the evidence page")
			}
			if p.SVG != "" && !bytes.Equal(receipt723HTTP(t, s, p.SVG), receipt723HTTP(t, cold, p.SVG)) {
				t.Fatal("offline restart changed the saved reference rendering")
			}
			// The restored representation remains bound to the original SVG.
			if err := os.WriteFile(filepath.Join(dir, "reader.svg"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			badRepo := reader631Repo(t, receipt723RepoName, receipt723Commit, 944, []reader631Fixture{f}, dashboard944Zip(t, dir))
			bad := &Server{Repos: []*Repo{badRepo}, Cache: t.TempDir()}
			got := reader631Find(t, reader631Load(t, bad, badRepo), badRepo.Name, f.Dir)
			if got.Status != "artifact_mismatch" {
				t.Fatal("reference form accepted a changed archived SVG", got.Status)
			}
		})
	}
}
