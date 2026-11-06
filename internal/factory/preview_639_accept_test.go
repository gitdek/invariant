package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Draft wire contract: each alternative modeled behavior Option has preview:{fork,option,choices,
// model}. choices maps ONLY previously authorized fork IDs to their option IDs;
// the alternative being illustrated is identified separately by fork/option.
// model is an ordinary Proposal snapshot: Draft fields plus exact module_text,
// view and includes, with no nested forks, plans, ratification or derived authority.
// Inline sources permit two options to use the same module/include names without
// sharing a filesystem. Checking pins candidates and stages each independently.
// Saved previews add fingerprint, receipt (verify.Report), and problem (on
// failure); model.reader and receipt.reader retain the same computed chart or
// limitation. Chart and receipt.reader add preview:{fork,option,choices}.
// The preview fingerprint binds its containing question/option words, IDs,
// choice context, exact source snapshot, report and reader evidence. Existing
// statement hashes and project verification fingerprints keep their semantics.
// Check checks all independent options and returns their failures together.
// Read alone remains usable for chart-free planning and legacy deserialization.
// Plumbing, PRD and authenticated D-0328 forks with kind:"step_fit" remain
// chart-free; unmarked modeled behavior forks require every preview.
// statechart.Result carries preview as a value with json omitzero, never a
// pointer: SVG validation rejects pointers even when nil. Ordinary charts
// omit preview entirely. Formalizer stages caller-owned effective choice and
// parent context before the agent runs; Check and the final check share it.
type preview639Saved struct {
	Fork        string              `json:"fork"`
	Option      string              `json:"option"`
	Choices     map[string]string   `json:"choices"`
	Model       *formalize.Proposal `json:"model"`
	Receipt     *verify.Report      `json:"receipt"`
	Problem     string              `json:"problem"`
	Fingerprint string              `json:"fingerprint"`
}

func preview639Draft(t *testing.T, forks int, choices map[string]string) map[string]any {
	t.Helper()
	fs := []any{}
	for i := 1; i <= forks; i++ {
		id := fmt.Sprintf("F%d", i)
		opts := []any{}
		for j, oid := range []string{"A", "B"} {
			var model map[string]any
			if err := json.Unmarshal([]byte(reader603Draft), &model); err != nil {
				t.Fatal(err)
			}
			model["bounds"] = map[string]string{"Limit": fmt.Sprint(j + 1)}
			model["module_text"] = reader603Model
			model["includes"] = map[string]string{"Helper603.tla": reader603Include}
			model["view"] = strings.ReplaceAll(reader603View, "Continue", id+oid+" continue")
			model["statements"].([]any)[1].(map[string]any)["says"] = id + oid + " keeps  its own <limit> & result."
			opts = append(opts, map[string]any{"id": oid, "says": id + oid + " completes after " + fmt.Sprint(j+1) + " advances.", "preview": map[string]any{"fork": id, "option": oid, "choices": choices, "model": model}})
		}
		fs = append(fs, map[string]any{"id": id, "question": id + " How many advances?", "options": opts})
	}
	return map[string]any{"forks": fs}
}
func preview639Option(d map[string]any, f, o int) map[string]any {
	return d["forks"].([]any)[f].(map[string]any)["options"].([]any)[o].(map[string]any)
}
func preview639Input(d map[string]any, f, o int) map[string]any {
	return preview639Option(d, f, o)["preview"].(map[string]any)
}
func preview639Write(t *testing.T, ws string, d any) {
	t.Helper()
	reader603Write(t, filepath.Join(ws, "proposal.json"), string(reader603JSON(t, d)))
}
func preview639SavedOptions(t *testing.T, p *formalize.Proposal) []preview639Saved {
	t.Helper()
	if p == nil {
		t.Fatal("missing fork proposal")
	}
	var wire struct {
		Forks []struct {
			Options []struct {
				Preview *preview639Saved `json:"preview"`
			} `json:"options"`
		} `json:"forks"`
	}
	if err := json.Unmarshal(reader603JSON(t, p), &wire); err != nil {
		t.Fatal(err)
	}
	var out []preview639Saved
	for _, f := range wire.Forks {
		for _, o := range f.Options {
			if o.Preview == nil {
				t.Fatal("modeled option has no checked preview")
			}
			out = append(out, *o.Preview)
		}
	}
	if len(out) == 0 {
		t.Fatal("no options")
	}
	return out
}
func preview639Checked(t *testing.T, ws string, tc toolchain.Toolchain) *formalize.Proposal {
	t.Helper()
	p, _, err := formalize.Check(t.Context(), ws, tc)
	if err != nil {
		t.Fatalf("preview check: %v", err)
	}
	for _, v := range preview639SavedOptions(t, p) {
		if v.Problem != "" || v.Model == nil || v.Model.Reader == nil || v.Receipt == nil || !v.Receipt.Passed || !v.Receipt.ModelOnly || v.Receipt.Ratified != nil || v.Receipt.Reader == nil || !strings.HasPrefix(v.Fingerprint, "sha256:") {
			t.Fatalf("incomplete checked preview: %+v", v)
		}
	}
	if p.Ratifiable() || p.Hash != "" {
		t.Fatal("checking alternatives made a ratifiable proposal")
	}
	return p
}
func preview639Validate(t *testing.T, p *formalize.Proposal) error {
	t.Helper()
	v, ok := any(p).(interface{ ValidatePreviews() error })
	if !ok {
		t.Fatal("Proposal must validate bound preview evidence before publication and export")
	}
	return v.ValidatePreviews()
}
func preview639Export(t *testing.T, p *formalize.Proposal, dir string) error {
	t.Helper()
	v, ok := any(p).(interface{ WritePreviews(string) error })
	if !ok {
		t.Fatal("Proposal must export per-option model-only receipts with WritePreviews")
	}
	return v.WritePreviews(dir)
}

func TestPreview639PublicationRecoveryAndPartialAnswers(t *testing.T) {
	// The new rendering branch is only for previews. Existing chart-free forks
	// retain the exact wording and clipping pinned by closing_commands tests.
	bare := []formalize.Fork{{ID: "F1", Question: "Which result?", Options: []formalize.Option{{ID: "A", Says: "Show it."}, {ID: "B", Says: "Keep it."}}}}
	mark := Marker{Kind: KindForks}
	closing := "Answer each one with a comment like `/invariant choose F1 A`, one line per question. Or say what you want in your own words, then comment `/invariant revise`."
	want := "◉ **Invariant** · a decision for you\n\nBefore I write down what must be true for this issue, I need you to decide:\n\n**F1. Which result?**\n\n- **A.** Show it.\n- **B.** Keep it.\n\n" + closing + "\n\n" + mark.encode() + "\n"
	if got := forksComment(bare, mark); got != want {
		t.Fatal("preview-less fork comment changed")
	}
	bare[0].Options[0].Says = strings.Repeat("Long chart-free option. ", maxComment)
	clipped := forksComment(bare, mark)
	if len(clipped) > maxComment || !strings.Contains(clipped, "Some detail is left out") || !strings.Contains(clipped, closing) || strings.Contains(clipped, "stateDiagram-v2") {
		t.Fatal("preview-less fork lost its clipping or complete closing instructions")
	}
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 2, nil))
	p := preview639Checked(t, ws, tc)
	for _, recovered := range []bool{false, true} {
		t.Run(fmt.Sprint(recovered), func(t *testing.T) {
			r := newRig(t)
			calls := 0
			var revised formalize.Request
			form := reader603Formalizer{run: func(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
				calls++
				if calls > 1 || (recovered && len(req.Answers) > 0) {
					revised = req
					return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Unsupported: "Fixture stops after revision input."}}}, nil
				}
				return &formalize.Result{Proposal: p}, nil
			}}
			r.f.Formalizer = form
			r.gh.open(639, "gitdek", "Compare modeled alternatives", "Model the alternatives.\n\n/invariant solve")
			if recovered {
				saved, err := r.f.saveResult(t.Context(), &formalize.Result{Proposal: p}, "saved option previews", "")
				if err != nil {
					t.Fatal(err)
				}
				if err = r.f.Repo.Record(t.Context(), 639, "draft-issue", "recorded draft"); err != nil {
					t.Fatal(err)
				}
				if err = r.f.Repo.Finish(t.Context(), 639, "draft-issue", saved); err != nil {
					t.Fatal(err)
				}
			}
			r.poll()
			post := r.expect(639, KindForks, LabelAsking)
			if recovered && calls != 0 {
				t.Fatal("recovery reran a completed drafting agent")
			}
			if strings.Count(post.Comment.Body, "stateDiagram-v2") != 4 {
				t.Fatal("decision request does not show all four checked alternatives together")
			}
			for _, v := range preview639SavedOptions(t, p) {
				if !strings.Contains(post.Comment.Body, v.Model.Reader.Chart.Markdown()) {
					t.Fatalf("%s/%s Mermaid/report differs from its checked preview", v.Fork, v.Option)
				}
			}
			for _, word := range []string{"unratified", "model-only", "/invariant choose F1 A"} {
				if !strings.Contains(strings.ToLower(post.Comment.Body), strings.ToLower(word)) {
					t.Errorf("decision request lacks %q", word)
				}
			}
			if strings.Contains(post.Comment.Body, "/invariant ratify") || len(r.gh.prs) != 0 || post.Marker.Proposal.Ratifiable() {
				t.Fatal("previewing proposed ratification or started building")
			}
			if !reflect.DeepEqual(preview639SavedOptions(t, p), preview639SavedOptions(t, post.Marker.Proposal)) {
				t.Fatal("marker lost preview snapshots")
			}
			choice := r.gh.say(639, "gitdek", "/invariant choose F1 B")
			r.poll()
			partial := r.expect(639, KindForks, LabelAsking)
			if strings.Count(partial.Comment.Body, "stateDiagram-v2") != 2 || len(partial.Marker.Answers) != 1 || partial.Marker.Answers[0].Option != "B" || partial.Marker.Answers[0].Comment != choice.URL {
				t.Fatal("partial answer did not retain its author/context and remaining charts")
			}
			if !reflect.DeepEqual(preview639SavedOptions(t, p), preview639SavedOptions(t, partial.Marker.Proposal)) {
				t.Fatal("partial choice silently rewrote checked preview context")
			}
			old := r.f
			r.f = &Factory{Repository: old.Repository, GitHub: r.gh, Repo: old.Repo, Formalizer: form, Builder: r.build, Base: old.Base, Projects: old.Projects, Work: old.Work, Check: old.Check, Log: t.Logf}
			posts := len(r.gh.posts(639))
			r.poll()
			if len(r.gh.posts(639)) != posts {
				t.Fatal("restart repeated the partial-choice response")
			}
			r.gh.say(639, "gitdek", "Keep the chosen limit; reconsider the other fork.\n/invariant revise")
			r.poll()
			if revised.Previous == nil || len(revised.Answers) != 1 || revised.Answers[0].Option != "B" || !reflect.DeepEqual(preview639SavedOptions(t, revised.Previous), preview639SavedOptions(t, p)) {
				t.Fatal("revision lost previous models or actual partial choices")
			}
			if len(r.gh.prs) != 0 {
				t.Fatal("choosing or revising a preview started a build")
			}
		})
	}
}

func TestPreview639PublicationRejectsStaleAndOversized(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 1, nil))
	p := preview639Checked(t, ws, tc)
	for _, kind := range []string{"missing", "swapped", "words", "oversized"} {
		for _, recovered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", kind, recovered), func(t *testing.T) {
				raw := reader603Object(t, p)
				switch kind {
				case "missing":
					delete(preview639Option(raw, 0, 0), "preview")
				case "swapped":
					a, b := preview639Option(raw, 0, 0), preview639Option(raw, 0, 1)
					a["preview"], b["preview"] = b["preview"], a["preview"]
				case "words":
					preview639Option(raw, 0, 0)["says"] = "A different promise."
				case "oversized":
					d := preview639Draft(t, 1, nil)
					// Change explanatory words before checking, so this is a valid oversized
					// artifact and cannot be refused merely as stale evidence.
					preview639Input(d, 0, 0)["model"].(map[string]any)["statements"].([]any)[1].(map[string]any)["says"] = strings.Repeat("All of these words must stay visible. ", 2400)
					preview639Write(t, ws, d)
					raw = reader603Object(t, preview639Checked(t, ws, tc))
				}
				var q formalize.Proposal
				if err := json.Unmarshal(reader603JSON(t, raw), &q); err != nil {
					t.Fatal(err)
				}
				r := newRig(t)
				res := &formalize.Result{Proposal: &q}
				calls := 0
				r.f.Formalizer = reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) { calls++; return res, nil }}
				r.gh.open(639, "gitdek", "Refuse invalid alternatives", "Model the alternatives.\n/invariant solve")
				if recovered {
					saved, err := r.f.saveResult(t.Context(), res, "saved preview result", "")
					if err != nil {
						t.Fatal(err)
					}
					if err = r.f.Repo.Record(t.Context(), 639, "draft-issue", "draft"); err != nil {
						t.Fatal(err)
					}
					if err = r.f.Repo.Finish(t.Context(), 639, "draft-issue", saved); err != nil {
						t.Fatal(err)
					}
				}
				r.poll()
				post := r.expect(639, KindStuck, LabelHumanReview)
				if recovered && calls != 0 {
					t.Fatal("invalid recovered result reran drafting instead of refusing publication")
				}
				if strings.Contains(post.Comment.Body, "stateDiagram-v2") || strings.Contains(post.Comment.Body, "Some detail is left out") || strings.Contains(post.Comment.Body, "/invariant choose") || len(r.gh.prs) != 0 {
					t.Fatal("refusal leaked a clipped or usable decision request")
				}
				if kind == "oversized" && !strings.Contains(strings.ToLower(post.Comment.Body), "size") {
					t.Fatal("oversized chart lacked actionable size diagnostic")
				}
			})
		}
	}
}
