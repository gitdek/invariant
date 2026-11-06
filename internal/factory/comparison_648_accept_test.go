package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

type comparison648Agent struct {
	run func(context.Context, synth.Job) (synth.Usage, error)
}

func (comparison648Agent) Name() string { return "comparison-fixture" }
func (a comparison648Agent) Run(c context.Context, j synth.Job) (synth.Usage, error) {
	return a.run(c, j)
}

type comparison648Repo struct {
	Repo
}

func (r comparison648Repo) Show(ctx context.Context, ref, file string) ([]byte, error) {
	if strings.HasPrefix(file, "examples/reader-lifecycle/") && (len(ref) != 40 || strings.Trim(ref, "0123456789abcdef") != "") {
		return nil, fmt.Errorf("baseline read uses a moving ref: %s", ref)
	}
	return r.Repo.Show(ctx, ref, file)
}
func (r comparison648Repo) Export(ctx context.Context, ref string, paths []string, dst string) error {
	for _, p := range paths {
		if strings.HasPrefix(p, "examples/reader-lifecycle/") && (len(ref) != 40 || strings.Trim(ref, "0123456789abcdef") != "") {
			return fmt.Errorf("baseline export uses a moving ref: %s", ref)
		}
	}
	return r.Repo.Export(ctx, ref, paths, dst)
}

func comparison648Record(t *testing.T, p *formalize.Proposal) map[string]any {
	t.Helper()
	w := reader603Object(t, p)
	r, ok := w["reader"].(map[string]any)
	if !ok {
		t.Fatal("missing reader")
	}
	c, ok := r["comparison"].(map[string]any)
	if !ok {
		t.Fatal("factory published a revision without a reader comparison")
	}
	return c
}

func TestComparison648FactoryCommentsAndRecovery(t *testing.T) {
	tc, _ := reader603Tools(t)
	r := newRig(t)
	run := 0
	r.f.Formalizer = formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		if run > 0 {
			reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised working"))
			reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), strings.ReplaceAll(reader603Draft, `"Limit":"2"`, `"Limit":"3"`))
		}
		run++
		return synth.Usage{}, nil
	}}}
	r.gh.open(648, "gitdek", "Compare revisions", "A reader chart.\n/invariant solve")
	r.poll()
	first := r.expect(648, KindProposal, LabelProposal)
	r.gh.say(648, "gitdek", "Change the view and checking scope.\n/invariant revise")
	r.poll()
	second := r.expect(648, KindProposal, LabelProposal)
	p := second.Marker.Proposal
	c := comparison648Record(t, p)
	baseline := c["baseline"].(map[string]any)
	if baseline["view"] != first.Marker.Proposal.View || !strings.Contains(baseline["name"].(string), first.Marker.Proposal.Hash) {
		t.Fatal("post compares the wrong preceding draft")
	}
	visible := html.UnescapeString(second.Comment.Body)
	for _, want := range []string{"view change", "scope", "added", "removed", "Limit", "2", "3", "ratify", "Revised working"} {
		if !strings.Contains(strings.ToLower(visible), strings.ToLower(want)) {
			t.Errorf("comment lost %q", want)
		}
	}
	if !strings.Contains(second.Comment.Body, p.Reader.Chart.Markdown()) {
		t.Fatal("proposal comparison omitted the complete new chart")
	}
	for _, side := range []string{"baseline_identity", "candidate_identity"} {
		id, ok := c[side].(map[string]any)
		if !ok {
			t.Fatalf("missing %s", side)
		}
		for _, field := range []string{"model_sha256", "view_sha256"} {
			if !strings.Contains(visible, id[field].(string)) {
				t.Fatalf("comparison is missing %s %s beside its chart", side, field)
			}
		}
	}
	// A fresh marker decode and saved-result reload must retain the exact
	// comparison, not ask a formalizer to reconstruct it from branch contents.
	m, ok := DecodeMarker(second.Comment.Body)
	if !ok {
		t.Fatal("comparison marker unreadable")
	}
	if !reflect.DeepEqual(c, comparison648Record(t, m.Proposal)) {
		t.Fatal("marker round trip changed the comparison")
	}
	saved, err := r.f.saveResult(t.Context(), &formalize.Result{Proposal: p}, "save comparison fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	var restored formalize.Result
	if err := r.f.loadResult(t.Context(), saved, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, comparison648Record(t, restored.Proposal)) {
		t.Fatal("saved-result recovery changed the comparison")
	}
	// A replacement draft cannot omit comparison just because its ordinary
	// chart validates. Caller context still requires the preceding snapshot.
	r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, _ formalize.Request, _ string) (*formalize.Result, error) {
		return &formalize.Result{Proposal: first.Marker.Proposal}, nil
	}}
	r.gh.say(648, "gitdek", "Revise again.\n/invariant revise")
	r.poll()
	stuck := r.expect(648, KindStuck, LabelHumanReview)
	if !strings.Contains(strings.ToLower(stuck.Comment.Body), "comparison") {
		t.Fatal("missing comparison publication failure is not actionable")
	}
}

func TestComparison648FactoryRetainsRatifiedBaseline(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	r := newRig(t)
	dir := "examples/reader-lifecycle"
	work := t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 571, By: "gitdek", Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed captured baseline")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	r.f.Repo = comparison648Repo{Repo: r.f.Repo}
	n := 0
	real := formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.ReplaceAll(reader603View, "Working", "Amended working"))
		reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), strings.ReplaceAll(reader603Draft, "The count stays within its limit.", "The count remains within its limit."))
		return synth.Usage{}, nil
	}}}
	r.f.Formalizer = reader603Formalizer{run: func(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
		if req.Current == nil || req.Current.ModuleText != old.ModuleText || req.Current.View != old.View {
			return nil, fmt.Errorf("amendment fetched a moving branch instead of its captured baseline")
		}
		n++
		return real.Formalize(ctx, req, out)
	}}
	r.gh.open(648, "gitdek", "Amend the reader", "Project: "+dir+"\n/invariant solve")
	r.poll()
	first := r.expect(648, KindProposal, LabelProposal)
	c := comparison648Record(t, first.Marker.Proposal)
	if c["baseline"].(map[string]any)["kind"] != "ratification" {
		t.Fatal("amendment compared with a proposal")
	}
	if !strings.Contains(first.Comment.Body, "**This changes the invariant `Safe`.**") {
		t.Fatal("reader comparison removed the existing statement warning")
	}
	reader603Write(t, filepath.Join(work, dir, ".invariant/view.json"), strings.ReplaceAll(reader603View, "Working", "Moved branch"))
	reader603Write(t, filepath.Join(work, dir, old.Manifest().Module), old.ModuleText+"\\* branch moved\n")
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "move branch after capture")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	r.gh.say(648, "gitdek", "Revise again.\n/invariant revise")
	r.poll()
	second := r.expect(648, KindProposal, LabelProposal)
	next := comparison648Record(t, second.Marker.Proposal)
	if n != 2 || !reflect.DeepEqual(c["baseline"], next["baseline"]) {
		t.Fatal("consecutive amendments do not keep the ratified baseline")
	}
	// Tampering with retained comparison bytes invalidates publication evidence.
	wire := reader603Object(t, second.Marker.Proposal)
	wire["reader"].(map[string]any)["comparison"].(map[string]any)["baseline"].(map[string]any)["view"] = "forged view"
	var forged formalize.Proposal
	if err := json.Unmarshal(reader603JSON(t, wire), &forged); err != nil {
		t.Fatal(err)
	}
	if err := forged.ValidateReader(); err == nil {
		t.Fatal("comparison tampering retained a valid reader identity")
	}
}
