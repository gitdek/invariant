package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

type markerCapacityFormalizer struct {
	result *formalize.Result
	calls  int
}

// Valid completed evidence may outgrow a marker without outgrowing the
// decision. Only an explicitly configured dashboard enables that publication.
func TestModeledPublicationExpandedMarkerWithDashboard(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", maxMarker+1024)+"\n====", 1))
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, formalize.CheckContext{})
	if err != nil {
		t.Fatal(err)
	}
	result := &formalize.Result{Proposal: p, Report: report, CheckContext: &formalize.CheckContext{}}
	for _, recovered := range []bool{false, true} {
		t.Run(fmt.Sprintf("recovered=%v", recovered), func(t *testing.T) {
			r := newRig(t)
			r.f.DashboardURL = "https://dashboard.example/repository"
			form := &markerCapacityFormalizer{result: result}
			r.f.Formalizer = form
			r.gh.open(603, "gitdek", "A complete saved draft", "/invariant solve")
			if recovered {
				saved := &savedDraftResult{Result: *result, Publication: &draftPublication{Agent: "claude-code"}}
				commit, err := r.f.saveResult(t.Context(), saved, "completed checked evidence", "")
				if err != nil {
					t.Fatal(err)
				}
				if err := r.f.Repo.Record(t.Context(), 603, "draft-issue", "draft before restart"); err != nil {
					t.Fatal(err)
				}
				if err := r.f.Repo.Finish(t.Context(), 603, "draft-issue", commit); err != nil {
					t.Fatal(err)
				}
			}
			r.poll()
			post := r.expect(603, KindProposal, LabelProposal)
			m, ok := DecodeMarker(post.Comment.Body)
			if !ok || m.ProposalRef == nil || m.Proposal != nil || !completeComment(post.Comment.Body, m) {
				t.Fatal("valid evidence overflow must publish one readable reference")
			}
			_, saved, err := m.ResolveDraft(t.Context(), r.f.Repository, 603, r.f.Repo.Run, r.f.Repo.Show)
			if err != nil || !draftEqual(saved.Proposal, p) || !draftEqual(saved.Report, report) {
				t.Fatalf("reference lost the full checked result: %v", err)
			}
			r.poll()
			wantCalls := 1
			if recovered {
				wantCalls = 0
			}
			if form.calls != wantCalls || len(r.gh.comments[603]) != 1 {
				t.Fatal("completed evidence was drafted or posted again")
			}
		})
	}
}

func (f *markerCapacityFormalizer) Formalize(context.Context, formalize.Request, string) (*formalize.Result, error) {
	f.calls++
	return f.result, nil
}

func TestModeledPublicationChecksExpandedMarker(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		for _, size := range []int{maxMarker / 2, maxMarker} {
			t.Run(fmt.Sprintf("include=%d recovered=%v", size, recovered), func(t *testing.T) {
				p := bufferProposal(t)
				// Repetitive comments in a valid sibling module barely grow the
				// compressed marker, even when its expanded JSON exceeds 4 MiB.
				p.Includes = map[string]string{"Helper.tla": "---- MODULE Helper ----\n\\* " + strings.Repeat("x", size) + "\n====\n"}
				checkedBufferReader(t, p)
				m := Marker{Kind: KindProposal, Proposal: p}
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				oversized := size == maxMarker
				if (len(data) > maxMarker) != oversized {
					t.Fatalf("fixture marker has %d expanded bytes", len(data))
				}
				if n := len(proposalComment(p, nil, m)); n >= 20000 {
					t.Fatalf("fixture must fit comfortably in a comment: %d bytes", n)
				}
				if _, ok := DecodeMarker(m.encode()); ok == oversized {
					t.Fatal("decoder did not enforce its existing expanded size limit")
				}

				r := newRig(t)
				form := &markerCapacityFormalizer{result: &formalize.Result{Proposal: p}}
				r.f.Formalizer = form
				r.gh.open(603, "gitdek", "A buffer with an include", "/invariant solve")
				if recovered {
					commit, err := r.f.saveResult(t.Context(), form.result, "saved draft with include", "")
					if err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Record(t.Context(), 603, "draft-issue", "draft before restart"); err != nil {
						t.Fatal(err)
					}
					if err := r.f.Repo.Finish(t.Context(), 603, "draft-issue", commit); err != nil {
						t.Fatal(err)
					}
				}
				r.poll()
				wantCalls := 1
				if recovered {
					wantCalls = 0
				}
				if form.calls != wantCalls {
					t.Fatalf("agent calls = %d, want %d", form.calls, wantCalls)
				}
				kind, label := KindProposal, LabelProposal
				if oversized {
					kind, label = KindStuck, LabelHumanReview
				}
				post := r.expect(603, kind, label)
				if len(r.gh.comments[603]) != 1 || len(post.Comment.Body) > maxComment {
					t.Fatal("publication must produce one readable comment within the limit")
				}
				if oversized {
					if post.Marker.Proposal != nil || strings.Contains(post.Comment.Body, "/invariant ratify ") || strings.Contains(post.Comment.Body, "```mermaid") {
						t.Fatal("refusal carried the unusable proposal")
					}
					for _, want := range []string{"marker", "4 MiB", "size limit", "check the draft again"} {
						if !strings.Contains(post.Comment.Body, want) {
							t.Errorf("refusal omits %q: %s", want, post.Comment.Body)
						}
					}
				} else {
					q := post.Marker.Proposal
					if q == nil || !reflect.DeepEqual(q.Includes, p.Includes) || !reflect.DeepEqual(q.Reader, p.Reader) {
						t.Fatal("readable proposal lost its exact include or checked evidence")
					}
				}
				r.poll()
				if len(r.gh.comments[603]) != 1 || form.calls != wantCalls {
					t.Fatal("the next poll did not retain the publication or refusal")
				}
			})
		}
	}
}
