package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Default acceptance checks every saved view through the common pure-view
// parser, pins the original model and lock bytes and inventories sibling
// projects separately. This is not claimed to be a completed TLC exploration.
// The required real validation is:
//
//	INVARIANT_DASHBOARD_READER_REAL=1 go test ./internal/dashboard \
//	  -run '^TestReader631ExampleViews$' -count=1 -timeout 30m
//
// It cannot skip missing tools. The merge's full gate separately supplies the
// code-verification receipts uploaded by CI, including Python and TLAPS checks.
type reader631Example struct {
	Name, ModelHash, LockHash string
	States                    int
	Words                     []string
}

var reader631Examples = []reader631Example{
	{"02-twophase-commit", "f4aee44c7457d9b3b00d2628eb391f125ed6f8094a87464beb81f4c2810f1cea", "b185f7e101da4e787ecfa805a4975c19b03a767fd71c2c19bf3fe20eb37bec1b", 288, []string{"prepar", "commit", "abort"}},
	{"02-twophase-commit-py", "f4aee44c7457d9b3b00d2628eb391f125ed6f8094a87464beb81f4c2810f1cea", "a985b902ab452796493483e606a6b0fd535e5dcec1f0340b00419993a8aaa46e", 288, []string{"prepar", "commit", "abort"}},
	{"02-twophase-commit-py-proved", "f4aee44c7457d9b3b00d2628eb391f125ed6f8094a87464beb81f4c2810f1cea", "773361bd5b73ee435903596db10e991e24fb14b272182bb2cc28aa84c4633838", 288, []string{"prepar", "commit", "abort"}},
	{"02-twophase-commit-synthesized", "5f167400260ae56b36e8f1292723d8c76dcff4229144dddc75aa7fa62abeb429", "f3a583fae2a838c31ac00bdca175fe65bacb1005af9203acc9b9a5cd8fdf7078", 288, []string{"prepar", "commit", "abort"}},
	{"02-twophase-commit-ts", "f4aee44c7457d9b3b00d2628eb391f125ed6f8094a87464beb81f4c2810f1cea", "53117fff9a1b040be2b233c98d96e547b822a3e4718ba45b92256acc73f6bce5", 288, []string{"prepar", "commit", "abort"}},
	{"03-log-buffer", "eec9fca8f823c89b871a040a08069a1f607fde111c4b9155fb350f717c218ccf", "a75a6846ecf8baa6f02bb5e647154fb029514ddcd2de8bd998bfdbcdd7f2413d", 87, []string{"buffer", "ship", "retr"}},
	{"03-log-buffer-py", "07599ab094b0779cef490c5aac4110992ebfa3711a3bfddef46e84605f6f08d6", "bb3c872a698270d254687d1f83c25af10abda251817fce7f4c90d63c2723fd69", 87, []string{"buffer", "ship", "retr"}},
	{"03-log-buffer-ts", "07599ab094b0779cef490c5aac4110992ebfa3711a3bfddef46e84605f6f08d6", "7214b5f6bf9e449f4470a5da7afe80fb5efeaebfe17cf9f810ed758f458a01fe", 87, []string{"buffer", "ship", "retr"}},
	{"04-api-rate-limiter", "5b60ade0c8f175333b06c6c4fb78c058ac94f54a282d972a88764dc71567a69f", "6668be68f690ab8ea48d554ea4d896eafc17f7d92b67e29e19eac5307ce8a449", 19489, nil},
	{"05-connection-pool", "d14e5bb60f69e554762314561a2f7b25343c243597da59fe381308d09a2a8b1b", "104450d9eed0da0a6eba29882d6503af0f1a4b9b78a12297e57d47463496f1ca", 13, []string{"connection", "client", "free"}},
}

func TestReader631ExampleViews(t *testing.T) {
	root := filepath.Join("..", "..", "examples")
	manifests, err := filepath.Glob(filepath.Join(root, "*", ".invariant", "invariant.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotNames, wantNames []string
	for _, name := range manifests {
		gotNames = append(gotNames, filepath.Base(filepath.Dir(filepath.Dir(name))))
	}
	for _, e := range reader631Examples {
		wantNames = append(wantNames, e.Name)
	}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	if strings.Join(gotNames, "\n") != strings.Join(wantNames, "\n") {
		t.Fatalf("modeled example inventory changed: %v, expected %v", gotNames, wantNames)
	}
	for _, e := range reader631Examples {
		t.Run(e.Name, func(t *testing.T) {
			dir := filepath.Join(root, e.Name)
			p, err := project.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{p.Manifest.Module: e.ModelHash, ".invariant/ratified.lock": e.LockHash} {
				b, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if reader631Hash(b) != "sha256:"+want {
					t.Fatalf("%s changed protected model, statements, pins or bounds: %s", e.Name, name)
				}
			}
			if e.States > 500 {
				return
			} // No view is required for the rate limiter.
			name := filepath.Join(dir, ".invariant/view.json")
			info, err := os.Lstat(name)
			if err != nil {
				t.Fatalf("qualifying example (%d states) has no saved reader view: %v", e.States, err)
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("saved reader view is not a regular file: %s", name)
			}
			view, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			// At the documented size boundary Compute still validates the entire
			// pure view grammar before reporting too_large, without inventing any
			// state values or pretending a sample checks totality and hiding.
			oversized := statechart.Input{}
			oversized.Graph.Graph.States = make([]tlc.GraphState, 501)
			chart, err := statechart.Compute(t.Context(), oversized, view)
			var refusal *statechart.Refusal
			if chart != nil || !errors.As(err, &refusal) || refusal.Code != "too_large" {
				t.Fatalf("saved view is not accepted by the common pure-view parser: %v", err)
			}
			var labels struct{ State, Step []struct{ Label string } }
			if err := json.Unmarshal(view, &labels); err != nil {
				t.Fatal(err)
			}
			var words strings.Builder
			for _, rules := range [][]struct{ Label string }{labels.State, labels.Step} {
				for _, r := range rules {
					words.WriteString(strings.ToLower(r.Label))
					words.WriteByte(' ')
				}
			}
			for _, word := range e.Words {
				if !strings.Contains(words.String(), word) {
					t.Errorf("reader labels omit the README's %q vocabulary: %s", word, words.String())
				}
			}
		})
	}
	if t.Failed() {
		return
	}
	if os.Getenv("INVARIANT_DASHBOARD_READER_REAL") == "1" {
		reader631RealExamples(t, root)
	}
}

func reader631RealExamples(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	var fixtures []reader631Fixture
	for _, e := range reader631Examples {
		dir := filepath.Join(root, e.Name)
		p, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		cfg := tlc.Config{Constants: p.Lock.Bounds}
		for _, s := range p.Lock.Statements {
			switch s.Kind {
			case project.Spec:
				cfg.Specification = s.Name
			case project.Invariant:
				cfg.Invariants = append(cfg.Invariants, s.Name)
			}
		}
		// A complete pinned exploration, not README counts or Graph.Compact,
		// decides qualification at the unchanged lock's exact bounds.
		graph, err := runner.DumpGraph(ctx, filepath.Dir(p.ModulePath()), p.ModuleName(), cfg)
		if err != nil || graph == nil {
			t.Fatalf("%s complete pinned TLC exploration: %v", e.Name, err)
		}
		if graph.StateCount != e.States {
			t.Fatalf("%s reached %d states at %v; recorded inventory is %d", e.Name, graph.StateCount, p.Lock.Bounds, e.States)
		}
		report, err := verify.RunModel(ctx, dir, tc)
		if err != nil || report == nil {
			t.Fatalf("%s model gate: %v", e.Name, err)
		}
		if !report.Design.Passed || report.Design.DistinctStates != int64(graph.StateCount) {
			t.Fatalf("%s design did not complete at the inventoried bounds: %+v", e.Name, report.Design)
		}
		if graph.StateCount <= 500 {
			if report.Reader == nil || report.Reader.Chart == nil || report.Reader.Unavailable != nil || report.Reader.Limitation != nil {
				t.Fatalf("%s has no checked chart after the model gate: %+v", e.Name, report.Reader)
			}
			chart := report.Reader.Chart
			if chart.Report.Counts["tlc_states"] != int64(graph.StateCount) || chart.Report.Counts["tlc_steps"] != int64(graph.StepCount) {
				t.Fatalf("%s reader lost complete graph accounting", e.Name)
			}
			if len(chart.States) < 2 {
				t.Fatalf("%s view collapses every reachable condition into one label", e.Name)
			}
			if _, err := chart.SVG(); err != nil {
				t.Fatalf("%s native SVG: %v", e.Name, err)
			}
		} else if report.Reader == nil || report.Reader.Unavailable == nil || report.Reader.Unavailable.Code != "missing_view" {
			t.Fatalf("example without a saved view must explicitly say so: %+v", report.Reader)
		}
		f := reader631Fixture{Dir: "examples/" + e.Name, Manifest: p.Manifest, Lock: p.Lock, Report: report, Files: map[string][]byte{}}
		for _, name := range []string{".invariant/invariant.json", ".invariant/ratified.lock", ".invariant/view.json"} {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if name == ".invariant/view.json" && os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			f.Files[name] = b
		}
		includes, err := filepath.Glob(filepath.Join(filepath.Dir(p.ModulePath()), "*.tla"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range includes {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(dir, name)
			if err != nil {
				t.Fatal(err)
			}
			f.Files[filepath.ToSlash(rel)] = b
		}
		fixtures = append(fixtures, f)
		t.Logf("%s: completed pinned TLC exploration of %d states at %v", e.Name, graph.StateCount, p.Lock.Bounds)
	}
	archive := reader631Archive(t, fixtures, nil)
	r := reader631Repo(t, "owner/examples", strings.Repeat("e", 40), 631, fixtures, archive)
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	ps := reader631Load(t, s, r)
	if len(ps) != len(fixtures) {
		t.Fatalf("only %d of %d example receipts visible", len(ps), len(fixtures))
	}
	urls := map[string]string{}
	for _, f := range fixtures {
		p := reader631Find(t, ps, r.Name, f.Dir)
		if f.Report.Reader.Chart != nil {
			reader631Chart(t, s, p, f.Report.Reader.Chart)
			if old := urls[p.SVG]; old != "" {
				t.Errorf("sibling %s reused %s's project identity", f.Dir, old)
			}
			urls[p.SVG] = f.Dir
		} else {
			reader631Diagnostic(t, s, p, "missing_view")
		}
		// Sources on disk and receipt input hashes must still agree after all
		// checking and archive presentation; no verification rewrites a view.
		if f.Report.Reader.Inputs.View != "" && !bytes.Equal([]byte(f.Report.Reader.Inputs.View), []byte(reader631Hash(f.Files[".invariant/view.json"]))) {
			t.Fatal(fmt.Sprintf("%s changed view snapshot", f.Dir))
		}
	}
}
