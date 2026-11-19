package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestProjection944CompleteRecordPublicationRecovery(t *testing.T) {
	tc, _ := reader603Tools(t)
	include := strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", 32<<20)+"\n====", 1)
	for _, kind := range []string{"comparison", "questions", "limited_questions"} {
		t.Run(kind, func(t *testing.T) {
			ws := t.TempDir()
			reader603Workspace(t, ws)
			res := &formalize.Result{CheckContext: &formalize.CheckContext{}}
			if kind != "comparison" {
				draft := preview639Draft(t, 1, nil)
				for j := range 2 {
					preview639Input(draft, 0, j)["model"].(map[string]any)["includes"] = map[string]string{"Helper603.tla": include}
					if kind == "limited_questions" {
						model := preview639Input(draft, 0, j)["model"].(map[string]any)
						model["bounds"] = map[string]string{"Limit": "500"}
						model["view"] = reader944PartitionView(501)
					}
				}
				preview639Write(t, ws, draft)
				res.Proposal = preview639Checked(t, ws, tc)
				for _, option := range res.Proposal.Forks[0].Options {
					b, err := json.MarshalIndent(option.Preview, "", "  ")
					if err != nil || len(b)+1 > statechart.MaxEvidenceBytes {
						t.Fatal("each complete option must fit independently", err)
					}
				}
			} else {
				reader603Write(t, filepath.Join(ws, "Helper603.tla"), include)
				base, _ := reader603Checked(t, ws, tc)
				authority := (formalize.Request{Previous: base}).CheckContext()
				reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised"))
				p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
				if err != nil {
					t.Fatal(err)
				}
				// The selected baseline can also travel through an answered
				// question. All retained copies must keep their original sources.
				p.Baseline = authority.Baseline
				res.Proposal, res.Report, res.CheckContext = p, report, &authority
			}
			original, err := json.Marshal(res)
			if err != nil || len(original) <= statechart.MaxEvidenceBytes {
				t.Fatal("complete proposal must overflow", err)
			}
			r := newRig(t)
			r.f.DashboardURL = compact911DashboardURL
			r.f.Formalizer = reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) {
				t.Fatal("completed evidence must not be redrafted")
				return nil, nil
			}}
			complete := &savedDraftResult{Result: *res, Publication: &draftPublication{Agent: r.f.agentOf("", "")}}
			completeBytes, err := json.Marshal(complete)
			if err != nil { t.Fatal(err) }
			commit, err := r.f.saveResult(t.Context(), complete, "complete checked draft", "")
			if err != nil { t.Fatal(err) }
			if err := r.f.Repo.Record(t.Context(), 944, "draft-issue", "checked draft"); err != nil { t.Fatal(err) }
			if err := r.f.Repo.Finish(t.Context(), 944, "draft-issue", commit); err != nil { t.Fatal(err) }
			r.gh.open(944, "gitdek", "Keep the complete saved evidence", "/invariant solve")
			r.poll()
			wantKind, wantLabel := KindProposal, LabelProposal
			if kind != "comparison" {
				wantKind, wantLabel = KindForks, LabelAsking
			}
			post := r.expect(944, wantKind, wantLabel)
			m, ok := DecodeMarker(post.Comment.Body)
			if !ok || m.ProposalRef == nil || len(post.Comment.Body) > 60000 {
				t.Fatal("complete record did not use compact publication")
			}
			var ref DraftReference
			if err := json.Unmarshal(m.ProposalRef, &ref); err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			show := func(ctx context.Context, commit, name string) ([]byte, error) {
				if name != "result.json" && !formalize.EvidenceCompanion(name) {
					t.Fatalf("unexpected companion path %s", name)
				}
				b, err := r.f.Repo.Show(ctx, commit, name)
				if len(b) > statechart.MaxEvidenceBytes {
					t.Fatalf("%s exceeds the complete entry ceiling", name)
				}
				files[name] = b
				return b, err
			}
			_, saved, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, r.f.Repo.Run, show)
			if err != nil || !draftEqual(saved, res) {
				t.Fatal("saved publication lost complete sources, receipts or identities", err)
			}
			if kind == "comparison" && len(files["reader-baseline.json"]) == 0 || kind != "comparison" && len(files["previews/F1/B/reader-candidate.json"]) == 0 {
				t.Fatal("fixture did not exercise complete source companions")
			}
			if kind == "limited_questions" {
				var wire formalize.Result
				if err := json.Unmarshal(files["result.json"], &wire); err != nil { t.Fatal(err) }
				for _, o := range wire.Proposal.Forks[0].Options {
					ref := o.Preview.EvidenceRef
					if ref == nil || ref.Actual != len(completeBytes) || ref.Limit != statechart.MaxEvidenceBytes || ref.Representation != "compact_json" {
						t.Fatal("limited alternative lost the exact complete-record byte count", ref)
					}
				}
			}
			after, _ := json.Marshal(res)
			if !bytes.Equal(original, after) {
				t.Fatal("saving mutated or resealed the original evidence")
			}
			for _, damage := range []string{"missing", "changed", "swapped"} {
				_, bad, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, r.f.Repo.Run,
					func(_ context.Context, commit, name string) ([]byte, error) {
						if commit != ref.Commit {
							return nil, fmt.Errorf("wrong commit")
						}
						b := files[name]
						if name != "result.json" {
							switch damage {
							case "missing":
								return nil, fmt.Errorf("missing companion")
							case "changed":
								return append(append([]byte{}, b...), ' '), nil
							case "swapped":
								return files["result.json"], nil
							}
						}
						return b, nil
					})
				if err == nil || bad != nil {
					t.Fatal("damaged evidence produced a partial usable draft", damage)
				}
			}
			compact911Fresh(t, r)
			r.poll()
			if len(r.gh.comments[944]) != 1 {
				t.Fatal("recovery republished or redrafted complete evidence")
			}
		})
	}
}
