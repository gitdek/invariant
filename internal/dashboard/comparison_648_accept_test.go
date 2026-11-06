package dashboard

import (
	"context"
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/synth"
)

// Modeled proposal Waiting records preserve proposal (the checked snapshot)
// and reader (the existing ReaderPresentation shape), rendered on public and
// /act cards. Their immutable evidence pages include the whole comparison.
// Restarts validate saved source/delta fingerprints before restoring images;
// a damaged proposal gets a diagnostic, not a silently empty comparison.
type comparison648Agent struct {
	run func(context.Context, synth.Job) (synth.Usage, error)
}

func (comparison648Agent) Name() string { return "comparison-fixture" }
func (a comparison648Agent) Run(c context.Context, j synth.Job) (synth.Usage, error) {
	return a.run(c, j)
}

func comparison648Waiting(t *testing.T, b []byte) (*formalize.Proposal, *ReaderPresentation) {
	t.Helper()
	var w struct {
		Issues []struct {
			Waiting *struct {
				Proposal *formalize.Proposal
				Reader   *ReaderPresentation
			}
		}
	}
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Issues) != 1 || w.Issues[0].Waiting == nil || w.Issues[0].Waiting.Proposal == nil || w.Issues[0].Waiting.Reader == nil {
		t.Fatal("modeled proposal card dropped its comparison source or presentation")
	}
	return w.Issues[0].Waiting.Proposal, w.Issues[0].Waiting.Reader
}

func comparison648Page(t *testing.T, s *Server, p *ReaderPresentation, words []string) string {
	t.Helper()
	if p.Status != "chart" || p.Page == "" || p.SVG == "" {
		t.Fatalf("comparison presentation not drawable: %+v", p)
	}
	page := reader631GET(s, "GET", p.Page, "")
	svg := reader631GET(s, "GET", p.SVG, "")
	if page.Code != 200 || svg.Code != 200 || !strings.HasPrefix(svg.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatal("comparison links do not serve native evidence")
	}
	for _, word := range words {
		for _, body := range []string{page.Body.String(), svg.Body.String()} {
			if !strings.Contains(strings.ToLower(html.UnescapeString(body)), strings.ToLower(word)) {
				t.Errorf("comparison format lost %q", word)
			}
		}
	}
	if strings.Contains(strings.ToLower(svg.Body.String()), "<script") || strings.Contains(page.Body.String(), "/act") {
		t.Fatal("public comparison gained active or private content")
	}
	for _, mark := range []string{`data-change="added"`, `data-change="removed"`} {
		if !strings.Contains(svg.Body.String(), mark) {
			t.Fatalf("dashboard SVG lost comparison annotation %s", mark)
		}
	}
	etag := svg.Header().Get("ETag")
	if etag == "" || reader631GET(s, "GET", p.SVG, etag).Code != 304 {
		t.Fatal("comparison lacks immutable conditional response")
	}
	return svg.Body.String()
}

func TestComparison648DashboardProposalAndRestart(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _, err := formalize.Check(t.Context(), ws, tc)
	if err != nil {
		t.Fatal(err)
	}
	a := comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised working"))
		reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), strings.ReplaceAll(reader603Draft, `"Limit":"2"`, `"Limit":"3"`))
		return synth.Usage{}, nil
	}}
	r, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Repo: "owner/project", Issue: 648, Previous: old}, t.TempDir())
	if err != nil || r == nil || r.Problem != "" {
		t.Fatalf("comparison draft: %+v %v", r, err)
	}
	const at = "2026-11-05T12:00:00Z"
	is := github.Issue{Number: 648, Title: "Compare revisions", State: "open", CreatedAt: at, Labels: []github.Label{{Name: factory.LabelProposal}}}
	m := factory.Marker{Kind: factory.KindProposal, Proposal: r.Proposal}
	repo := &Repo{Name: "owner/project", src: sources{issues: []github.Issue{is}, comments: map[int]cachedComments{648: {comments: []github.Comment{{ID: 648, User: github.User{Login: "factory[bot]"}, CreatedAt: at, Body: marker(t, m)}}}}}}
	s := &Server{Repos: []*Repo{repo}, Cache: t.TempDir()}
	p, presentation := comparison648Waiting(t, preview639Snapshot(t, s))
	if !reflect.DeepEqual(p, r.Proposal) {
		t.Fatal("dashboard relabeled proposal comparison sources")
	}
	words := []string{old.Hash, "Revised working", "Working", "view change", "scope", "added", "removed"}
	svg := comparison648Page(t, s, presentation, words)
	cold := &Server{Repos: []*Repo{{Name: repo.Name}}, Cache: s.Cache}
	cold.loadSnapshot()
	res := reader631GET(cold, "GET", "/api/state.json", "")
	if res.Code != 200 {
		t.Fatal("restart lost comparison snapshot")
	}
	p2, pres2 := comparison648Waiting(t, res.Body.Bytes())
	if !reflect.DeepEqual(p, p2) || !reflect.DeepEqual(presentation, pres2) || svg != comparison648Page(t, cold, pres2, words) {
		t.Fatal("restart changed comparison identity or contents")
	}
	// Corrupt the retained evidence while leaving the saved page untouched.
	damaged := false
	err = filepath.WalkDir(s.Cache, func(file string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(file, ".json") {
			return nil
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), `"comparison"`) && strings.Contains(string(b), "Revised working") {
			damaged = true
			return os.WriteFile(file, []byte(strings.ReplaceAll(string(b), "Revised working", "Corrupt working")), 0600)
		}
		return nil
	})
	if err != nil || !damaged {
		t.Fatalf("no saved structured comparison to validate on restart: %v", err)
	}
	broken := &Server{Repos: []*Repo{{Name: repo.Name}}, Cache: s.Cache}
	broken.loadSnapshot()
	res = reader631GET(broken, "GET", "/api/state.json", "")
	_, bad := comparison648Waiting(t, res.Body.Bytes())
	if bad.Status == "chart" || bad.SVG != "" || bad.Message == "" {
		t.Fatal("tampered comparison recovered as an intact chart")
	}
}

func TestComparison648DashboardReceiptKeepsProposalSources(t *testing.T) {
	old := reader631FixtureFor(t, 2, "Original ready")
	f := reader631FixtureFor(t, 3, "Revised ready")
	snapshot := func(f reader631Fixture, name string) statechart.ComparisonSnapshot {
		return statechart.ComparisonSnapshot{Kind: "proposal", Name: name, Module: "Dashboard631", Model: string(f.Files[f.Manifest.Module]), View: string(f.Files[".invariant/view.json"]), Includes: map[string]string{"Helper631.tla": string(f.Files[".invariant/specs/Helper631.tla"])}, Lock: f.Lock, Chart: f.Report.Reader.Chart}
	}
	c, err := statechart.Compare(snapshot(old, "Original proposal"), snapshot(f, "Revised proposal"))
	if err != nil {
		t.Fatal(err)
	}
	wire := reader603Object(t, f.Report)
	wire["reader"].(map[string]any)["comparison"] = c
	if err := json.Unmarshal(reader603JSON(t, wire), f.Report); err != nil {
		t.Fatal(err)
	}
	f.Files[".invariant/comparison.json"] = reader603JSON(t, c)
	f.Files[f.Manifest.Module] = append(f.Files[f.Manifest.Module], []byte("\\* final synthesized model\n")...)
	f.bind(t)
	archive := reader631Archive(t, []reader631Fixture{f}, nil)
	repo := reader631Repo(t, "owner/project", strings.Repeat("6", 40), 648, []reader631Fixture{f}, archive)
	s := &Server{Repos: []*Repo{repo}, Cache: t.TempDir()}
	p := reader631Find(t, reader631Load(t, s, repo), repo.Name, f.Dir)
	pres := ReaderPresentation{Status: p.Status, Message: p.Message, Commit: p.Commit, Fingerprint: p.Fingerprint, Format: p.Format, Page: p.Page, SVG: p.SVG}
	words := []string{"Original proposal", "Revised proposal", "Original ready", "Revised ready", "view change", "scope", "proposal comparison", "final", c.BaselineIdentity.ModelSHA256, c.CandidateIdentity.ViewSHA256}
	svg := comparison648Page(t, s, &pres, words)
	cold := &Server{Repos: []*Repo{{Name: repo.Name}}, Cache: s.Cache}
	cold.loadSnapshot()
	if got := comparison648Page(t, cold, &pres, words); got != svg {
		t.Fatal("receipt restart substituted the latest model for the proposal comparison")
	}
	page := reader631GET(s, "GET", pres.Page, "")
	if !strings.Contains(page.Body.String(), f.Report.Reader.Inputs.Model) {
		t.Fatal("receipt page lost the different final-model identity")
	}
}
