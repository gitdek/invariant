package factory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

// A failed preview check keeps every option's snapshot in its result, for
// repair. The stuck post that reports it must still read back and fit: a
// proposal too big for either stays in the saved run, not the marker.
func TestFailedPreviewPostKeepsItsLimits(t *testing.T) {
	noise := make([]byte, 48<<10)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, include string
		kept          bool
	}{
		{"small", "a short note", true},
		// Repetition barely grows the compressed marker past its expanded limit.
		{"compressible", strings.Repeat("x", maxMarker), false},
		// Random hex only halves, so its marker outgrows any post.
		{"incompressible", hex.EncodeToString(noise), false},
	} {
		for _, recovered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s recovered=%v", c.name, recovered), func(t *testing.T) {
				forks := checkedForks(t, []formalize.Fork{{ID: "F1", Question: "How many advances?",
					Options: []formalize.Option{{ID: "A", Says: "One advance."}, {ID: "B", Says: "Two advances."}}}}, nil)
				include := "---- MODULE Helper ----\n\\* " + c.include + "\n====\n"
				v := forks[0].Options[1].Preview
				v.Model.Includes = map[string]string{"Helper.tla": include}
				v.Problem = "TLC found a deadlock."
				p := &formalize.Proposal{Draft: formalize.Draft{Forks: forks}}
				form := &markerCapacityFormalizer{result: &formalize.Result{Proposal: p, Problem: "Option F1 B failed its check: TLC found a deadlock."}}
				r := newRig(t)
				r.f.Formalizer = form
				r.gh.open(646, "gitdek", "Compare alternatives", "Model the alternatives.\n/invariant solve")
				wantCalls := 1
				if recovered {
					wantCalls = 0
					saved, err := r.f.saveResult(t.Context(), form.result, "saved failed preview check", "")
					if err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Record(t.Context(), 646, "draft-issue", "draft before restart"); err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Finish(t.Context(), 646, "draft-issue", saved); err != nil {
						t.Fatal(err)
					}
				}
				r.poll()
				post := r.expect(646, KindStuck, LabelHumanReview)
				if len(r.gh.comments[646]) != 1 || len(post.Comment.Body) > maxComment || !strings.Contains(post.Comment.Body, "TLC found a deadlock") {
					t.Fatalf("the failure must be one readable comment within the limit, not %d bytes", len(post.Comment.Body))
				}
				if kept := post.Marker.Proposal != nil; kept != c.kept {
					t.Fatalf("marker kept the proposal: %v, want %v", kept, c.kept)
				}
				if c.kept {
					want, _ := json.Marshal(p.Forks)
					got, _ := json.Marshal(post.Marker.Proposal.Forks)
					if string(got) != string(want) {
						t.Fatal("a marker that fits lost the failed preview's snapshots")
					}
				}
				state, commit, err := r.f.Repo.Run(t.Context(), 646, "draft-issue")
				if err != nil || state != RunDone {
					t.Fatalf("the draft's run is %v: %v", state, err)
				}
				var saved formalize.Result
				if err := r.f.loadResult(t.Context(), commit, &saved); err != nil {
					t.Fatal(err)
				}
				if q := saved.Proposal; q == nil || len(q.Forks) != 1 || q.Forks[0].Options[1].Preview.Model.Includes["Helper.tla"] != include {
					t.Fatal("the saved result lost the failed option's snapshot")
				}
				r.poll()
				if len(r.gh.comments[646]) != 1 || form.calls != wantCalls {
					t.Fatal("the next poll repeated the failure post or the agent run")
				}
			})
		}
	}
}
