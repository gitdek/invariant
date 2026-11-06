package formalize_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
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

func TestPreview639ChecksEachAlternative(t *testing.T) {
	tc, log := reader603Tools(t)
	ws := t.TempDir()
	d := preview639Draft(t, 2, nil)
	// Agent-supplied derived output is not authority, even when syntactically valid.
	preview639Input(d, 0, 0)["fingerprint"] = "forged"
	preview639Input(d, 0, 0)["receipt"] = map[string]any{"passed": true}
	preview639Input(d, 0, 0)["model"].(map[string]any)["reader"] = map[string]any{"version": 1, "fingerprint": "forged"}
	preview639Write(t, ws, d)
	p := preview639Checked(t, ws, tc)
	vs := preview639SavedOptions(t, p)
	if len(vs) != 4 {
		t.Fatalf("got %d previews", len(vs))
	}
	fingerprints := map[string]bool{}
	for i, v := range vs {
		n := int64(i%2 + 2)
		c := v.Model.Reader.Chart
		if c == nil || c.Report.Counts["tlc_states"] != n || c.Report.Counts["hidden_steps"] != n || int64(len(c.Arrows)) != n {
			t.Fatalf("%s/%s wrong graph or hiding: %+v", v.Fork, v.Option, c)
		}
		if c.Report.Rules[1].Says != v.Fork+v.Option+" keeps  its own <limit> & result." || c.Report.Source.Config.Constants["Limit"] != fmt.Sprint(n-1) {
			t.Fatal("option used another option's words or bounds")
		}
		if c.Report.Source.Tool.Image != tc.JavaImage || c.Report.Source.Tool.JarSHA256 == "" {
			t.Fatal("lost checked tool provenance")
		}
		if !reflect.DeepEqual(v.Receipt.Reader.Chart, c) {
			t.Fatal("receipt and proposal charts disagree")
		}
		var scope struct {
			Preview struct {
				Fork, Option string
				Choices      map[string]string
			} `json:"preview"`
		}
		if err := json.Unmarshal(reader603JSON(t, c), &scope); err != nil {
			t.Fatal(err)
		}
		if scope.Preview.Fork != v.Fork || scope.Preview.Option != v.Option {
			t.Fatal("chart lacks exact preview identity")
		}
		if fingerprints[v.Fingerprint] {
			t.Fatal("different option identities share a fingerprint")
		}
		fingerprints[v.Fingerprint] = true
	}
	if reflect.DeepEqual(vs[0].Model.Reader.Chart.Arrows, vs[1].Model.Reader.Chart.Arrows) {
		t.Fatal("observably different alternatives show identical transitions")
	}
	if len(reader603Calls(t, log)) < 12 {
		t.Fatal("not every option ran model checking, complete exploration and invariant replay")
	}
	if err := preview639Validate(t, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Write(t.TempDir(), "", nil); err == nil {
		t.Fatal("an alternatives request was written as a project")
	}
}

func TestPreview639FailuresAndRepair(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, kind := range []string{"missing", "wrong option", "wrong fork", "invalid view", "hiding", "model"} {
		t.Run(kind, func(t *testing.T) {
			d := preview639Draft(t, 2, nil)
			bad := preview639Input(d, 0, 0)
			model := bad["model"].(map[string]any)
			want := "F1"
			switch kind {
			case "missing":
				delete(preview639Option(d, 0, 0), "preview")
			case "wrong option":
				bad["option"] = "B"
			case "wrong fork":
				bad["fork"] = "F2"
			case "invalid view":
				model["view"] = `{"version":1,"state":[],"step":[]}`
				want = "invalid_view"
			case "hiding":
				model["view"] = strings.Replace(model["view"].(string), `"label":"F1A continue"`, `"label":"F1A continue","hide":true`, 1)
				want = "hiding"
			case "model":
				model["module_text"] = strings.Replace(reader603Model, `Safe == n \in 0..Limit`, `Safe == FALSE`, 1)
				want = "Safe"
			}
			// A second failure is independent; successful siblings still have evidence.
			preview639Input(d, 1, 1)["model"].(map[string]any)["view"] = `{"version":1,"state":[],"step":[]}`
			ws := t.TempDir()
			preview639Write(t, ws, d)
			p, _, err := formalize.Check(t.Context(), ws, tc)
			if err == nil {
				t.Fatal("invalid option set was accepted")
			}
			for _, s := range []string{"F1", "A", "F2", "B", want, "invalid_view"} {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("feedback omits %q: %v", s, err)
				}
			}
			if p != nil && p.Ratifiable() {
				t.Fatal("failed alternatives became ratifiable")
			}
			if kind == "invalid view" || kind == "model" || kind == "hiding" {
				vs := preview639SavedOptions(t, p)
				if vs[0].Problem == "" || vs[3].Problem == "" || vs[1].Receipt == nil || !vs[1].Receipt.Passed || vs[2].Receipt == nil || !vs[2].Receipt.Passed {
					t.Fatal("one preview failure erased siblings or lost independent diagnostics")
				}
				if err := preview639Validate(t, p); err == nil {
					t.Fatal("partial success passed publication validation")
				}
			}
			preview639Write(t, ws, preview639Draft(t, 2, nil))
			preview639Checked(t, ws, tc)
		})
	}
	// Capacity is the existing explicit limitation, never a truncated chart or
	// a request to shrink faithful bounds, and is bound to this particular option.
	d := preview639Draft(t, 1, nil)
	preview639Input(d, 0, 1)["model"].(map[string]any)["bounds"] = map[string]string{"Limit": "500"}
	ws := t.TempDir()
	preview639Write(t, ws, d)
	p := preview639Checked(t, ws, tc)
	v := preview639SavedOptions(t, p)[1]
	if v.Model.Reader.Chart != nil || v.Model.Reader.Limitation == nil || v.Model.Reader.Limitation.Actual != 501 {
		t.Fatal("large preview lost the existing size limitation")
	}
}

func TestPreview639FinalCheckAndSavedInputs(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			var checked *formalize.Proposal
			a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				preview639Write(t, j.Workspace, preview639Draft(t, 2, nil))
				checked = preview639Checked(t, j.Workspace, tc)
				d := reader603Object(t, checked)
				model := preview639Input(d, 0, 1)["model"].(map[string]any)
				model["view"] = strings.Replace(model["view"].(string), "F1B continue", "Fresh final transition", 1)
				if invalid {
					model["view"] = `{"version":1,"state":[],"step":[]}`
				}
				preview639Write(t, j.Workspace, d)
				reader603Write(t, filepath.Join(j.Workspace, "formalization.json"), string(reader603JSON(t, formalize.Result{Proposal: checked})))
				return synth.Usage{}, nil
			}}
			out := t.TempDir()
			res, err := (formalize.Formalizer{Backend: a, Toolchain: tc, CheckRuns: 3}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 639}, out)
			if err != nil || res == nil {
				t.Fatalf("final check: %v", err)
			}
			if invalid {
				if !strings.Contains(res.Problem, "F1") || !strings.Contains(res.Problem, "invalid_view") {
					t.Fatalf("forged prior success bypassed final checking: %+v", res)
				}
				b, err := os.ReadFile(filepath.Join(out, "formalization.json"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(b), "invalid_view") {
					t.Fatal("saved failure lost option diagnostics")
				}
				var failed formalize.Result
				if err := json.Unmarshal(b, &failed); err != nil {
					t.Fatal(err)
				}
				options := preview639SavedOptions(t, failed.Proposal)
				if options[1].Problem == "" || options[1].Model.View != `{"version":1,"state":[],"step":[]}` || (options[1].Model.Reader != nil && options[1].Model.Reader.Chart != nil) || options[0].Receipt == nil || !options[0].Receipt.Passed {
					t.Fatal("failed final result lost exact failing inputs or retained a stale chart")
				}
				return
			}
			if res.Problem != "" {
				t.Fatal(res.Problem)
			}
			before, after := preview639SavedOptions(t, checked), preview639SavedOptions(t, res.Proposal)
			if before[1].Fingerprint == after[1].Fingerprint || !strings.Contains(after[1].Model.Reader.Chart.Markdown(), "Fresh final transition") {
				t.Fatal("final check reused old output")
			}
			var saved formalize.Result
			b, err := os.ReadFile(filepath.Join(out, "formalization.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(b, &saved); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, preview639SavedOptions(t, saved.Proposal)) {
				t.Fatal("saved result lost preview source or evidence")
			}
			for _, option := range after {
				for _, name := range []string{"preview.json", "receipt.json", "receipt.md", "reader.json", "reader.svg"} {
					if _, err := os.Stat(filepath.Join(out, "previews", option.Fork, option.Option, name)); err != nil {
						t.Fatalf("formalization did not export %s/%s/%s: %v", option.Fork, option.Option, name, err)
					}
				}
			}
			inspected := false
			next := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				b, err := os.ReadFile(filepath.Join(j.Workspace, "previous", "proposal.json"))
				if err != nil {
					t.Fatal(err)
				}
				var prev formalize.Proposal
				if err = json.Unmarshal(b, &prev); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(after, preview639SavedOptions(t, &prev)) {
					t.Fatal("previous draft lost exact option snapshots")
				}
				inspected = true
				reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), `{"unsupported":"Stop after examining previous input."}`)
				return synth.Usage{}, nil
			}}
			_, err = (formalize.Formalizer{Backend: next, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 639, Previous: saved.Proposal}, t.TempDir())
			if err != nil || !inspected {
				t.Fatalf("previous staging: %v", err)
			}
		})
	}
}

func TestPreview639ChoicesAndChartFreePlanning(t *testing.T) {
	tc, log := reader603Tools(t)
	answer := formalize.Answer{Fork: "F0", Option: "B", Question: "Keep results?", Says: "Keep results.", By: "owner", Comment: "https://example.test/comment/1"}
	for _, kind := range []string{"valid", "missing", "contradiction", "invented"} {
		t.Run(kind, func(t *testing.T) {
			choices := map[string]string{"F0": "B"}
			switch kind {
			case "missing":
				delete(choices, "F0")
			case "contradiction":
				choices["F0"] = "A"
			case "invented":
				choices["F2"] = "A"
			}
			a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				preview639Write(t, j.Workspace, preview639Draft(t, 2, choices))
				// Check has no Answers argument. It must see the same caller-staged
				// choices during the agent's run as the independent final check.
				checked, _, checkErr := formalize.Check(ctx, j.Workspace, tc)
				if kind == "valid" {
					if checkErr != nil {
						t.Fatalf("agent check rejected authorized choices: %v", checkErr)
					}
					for _, v := range preview639SavedOptions(t, checked) {
						if !reflect.DeepEqual(v.Choices, choices) {
							t.Fatal("agent check did not retain effective prior choices")
						}
					}
				} else if checkErr == nil {
					t.Fatal("agent check accepted omitted, contradicted or invented prior choices")
				}
				return synth.Usage{}, nil
			}}
			res, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 639, Answers: []formalize.Answer{answer}}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if kind != "valid" {
				if res.Problem == "" {
					t.Fatal("preview silently omitted, contradicted or invented a chosen fork")
				}
				return
			}
			if res.Problem != "" {
				t.Fatal(res.Problem)
			}
			for _, v := range preview639SavedOptions(t, res.Proposal) {
				if !reflect.DeepEqual(v.Choices, choices) {
					t.Fatal("authoritative choice context was not saved")
				}
			}
		})
	}
	// A mandatory preview is a modeled-behavior rule, not a restriction on
	// plumbing/PRD or D-0328 step-fit questions. Caller context is authoritative.
	const bare = `{"forks":[{"id":"F1","question":"Which wording?","options":[{"id":"A","says":"First."},{"id":"B","says":"Second."}]}]}`
	ws := t.TempDir()
	reader603Write(t, filepath.Join(ws, "proposal.json"), bare)
	if _, _, err := formalize.Check(t.Context(), ws, tc); err == nil {
		t.Fatal("modeled forks still bypass checking")
	}
	for _, kind := range []string{"plumbing", "prd"} {
		req := formalize.Request{Repo: "o/r", Issue: 639}
		root := t.TempDir()
		if kind == "plumbing" {
			req.Plumbing = root
		} else {
			req.PRD = root
		}
		a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
			reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), bare)
			return synth.Usage{}, nil
		}}
		res, err := (formalize.Formalizer{Backend: a}).Formalize(t.Context(), req, t.TempDir())
		if err != nil || res.Problem != "" || res.Proposal == nil || res.Report != nil || strings.Contains(string(reader603JSON(t, res.Proposal)), `"preview"`) {
			t.Fatalf("%s fork acquired model checking: %+v %v", kind, res, err)
		}
	}
	const fit = `{"forks":[{"id":"F1","kind":"step_fit","question":"The storage and UI work will not fit one reviewable pull request; should they become replacement steps of parent #571?","options":[{"id":"A","says":"Build the complete step as one change."},{"id":"B","says":"A writer stops parent #571 with /invariant stop and requests a replacement plan on a new top-level issue with /invariant plan, splitting storage and UI into sibling steps."}]}]}`
	for _, parent := range []int{571, 0} {
		t.Run(fmt.Sprintf("step-fit/parent=%d", parent), func(t *testing.T) {
			before := len(reader603Calls(t, log))
			a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), fit)
				p, r, err := formalize.Check(ctx, j.Workspace, tc)
				if parent == 0 {
					if err == nil {
						t.Fatal("agent-supplied step-fit kind invented parent authorization")
					}
				} else if err != nil || p == nil || len(p.Forks) != 1 || p.Ratifiable() || r != nil || strings.Contains(string(reader603JSON(t, p)), `"preview"`) {
					t.Fatalf("agent check required a model for a step-fit question: %+v %v", p, err)
				}
				return synth.Usage{}, nil
			}}
			res, err := (formalize.Formalizer{Backend: a, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 639, ParentPlan: parent}, t.TempDir())
			if err != nil || res == nil {
				t.Fatalf("step-fit final check: %+v %v", res, err)
			}
			if parent == 0 {
				if res.Problem == "" {
					t.Fatal("final check accepted unauthenticated step-fit exemption")
				}
			} else if res.Problem != "" || res.Proposal == nil || len(res.Proposal.Forks) != 1 || res.Proposal.Ratifiable() || res.Report != nil || strings.Contains(string(reader603JSON(t, res.Proposal)), `"preview"`) {
				t.Fatalf("final check required a model for a step-fit question: %+v", res)
			}
			if len(reader603Calls(t, log)) != before {
				t.Fatal("step-fit checking invoked TLC")
			}
		})
	}
	prompt := formalize.Prompt(formalize.Request{}, 3)
	if strings.Contains(prompt, "don't write the module: people answer first") || !strings.Contains(prompt, "preview") || !strings.Contains(prompt, "unanswered") {
		t.Fatal("modeled prompt still prevents option models or omits unanswered-fork guidance")
	}
	stepPrompt := formalize.Prompt(formalize.Request{ParentPlan: 571}, 3)
	if !strings.Contains(stepPrompt, "step_fit") || !strings.Contains(stepPrompt, "/invariant stop") || !strings.Contains(stepPrompt, "/invariant plan") {
		t.Fatal("modeled step prompt omits its chart-free step-fit escape")
	}
}

func TestPreview639ReceiptsAndSelectedModel(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 2, nil))
	p := preview639Checked(t, ws, tc)
	out := t.TempDir()
	if err := preview639Export(t, p, out); err != nil {
		t.Fatal(err)
	}
	for _, v := range preview639SavedOptions(t, p) {
		dir := filepath.Join(out, "previews", v.Fork, v.Option)
		var report verify.Report
		b, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &report); err != nil {
			t.Fatal(err)
		}
		if !report.ModelOnly || !report.Passed || report.Ratified != nil || report.Code != nil || report.Conformance != nil || report.Reader == nil || !reflect.DeepEqual(report.Reader.Chart, v.Model.Reader.Chart) {
			t.Fatal("preview export changed evidence or claimed code/ratification")
		}
		var rr verify.Reader
		b, err = os.ReadFile(filepath.Join(dir, "reader.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &rr); err != nil || !reflect.DeepEqual(&rr, report.Reader) {
			t.Fatal("reader companion mismatch")
		}
		var snapshot preview639Saved
		b, err = os.ReadFile(filepath.Join(dir, "preview.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &snapshot); err != nil || !reflect.DeepEqual(snapshot, v) {
			t.Fatal("export lost complete inputs or preview identity")
		}
		md, err := os.ReadFile(filepath.Join(dir, "receipt.md"))
		if err != nil {
			t.Fatal(err)
		}
		svg, err := os.ReadFile(filepath.Join(dir, "reader.svg"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := v.Model.Reader.Chart.SVG()
		if err != nil || string(svg) != want {
			t.Fatal("receipt SVG differs from shared renderer")
		}
		if !strings.Contains(string(md), v.Model.Reader.Chart.Markdown()) || !strings.Contains(string(md), "stateDiagram-v2") {
			t.Fatal("receipt Mermaid differs from option")
		}
		for _, body := range []string{string(md), string(svg)} {
			for _, s := range []string{"unratified", "model-only", v.Fork, v.Option, "Limit"} {
				if !strings.Contains(strings.ToLower(body), strings.ToLower(s)) {
					t.Errorf("export omits %q", s)
				}
			}
			if !strings.Contains(strings.ToLower(body), "candidate") || strings.Contains(body, "Locked rules") {
				t.Fatal("alternative rules are presented as locked or ratified")
			}
		}
		dec := xml.NewDecoder(strings.NewReader(string(svg)))
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if el, ok := tok.(xml.StartElement); ok {
				for _, bad := range []string{"script", "foreignObject", "image", "iframe"} {
					if el.Name.Local == bad {
						t.Fatal("SVG has active or external content")
					}
				}
			}
		}
	}
	// Reusing the export directory removes alternatives that no longer exist.
	preview639Write(t, ws, preview639Draft(t, 1, nil))
	q := preview639Checked(t, ws, tc)
	if err := preview639Export(t, q, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "previews", "F2")); !os.IsNotExist(err) {
		t.Fatal("removed alternatives remain as current exported evidence")
	}
	// A real selected-project model check derives its own chart from final files.
	selected := preview639SavedOptions(t, q)[0].Model
	raw := reader603Object(t, selected)
	delete(raw, "reader")
	var clean formalize.Proposal
	if err := json.Unmarshal(reader603JSON(t, raw), &clean); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := clean.Write(dir, "Selected candidate, still unratified.", nil); err != nil {
		t.Fatal(err)
	}
	reader603Write(t, filepath.Join(dir, ".invariant", "view.json"), strings.Replace(clean.View, "F1A continue", "Selected final model", 1))
	report, err := verify.RunModel(t.Context(), dir, tc)
	if err != nil || report == nil || !report.Passed || report.Reader == nil || report.Reader.Chart == nil {
		t.Fatalf("selected model check: %+v %v", report, err)
	}
	if !strings.Contains(receipt.Markdown(report), "Selected final model") || strings.Contains(string(reader603JSON(t, report.Reader.Chart)), `"preview"`) {
		t.Fatal("selected model inherited preview chart or alternative identity")
	}
	if strings.Contains(string(reader603JSON(t, report.Reader)), `"preview"`) {
		t.Fatal("ordinary selected-model reader acquired preview metadata")
	}
	if _, err := report.Reader.Chart.SVG(); err != nil {
		t.Fatalf("ordinary selected-model chart is no longer renderable: %v", err)
	}
	// Tampering with an ID or source after checking invalidates publication/export.
	for _, kind := range []string{"id", "words", "view", "chart", "receipt"} {
		raw := reader603Object(t, q)
		o := preview639Option(raw, 0, 0)
		v := preview639Input(raw, 0, 0)
		switch kind {
		case "id":
			o["id"] = "C"
		case "words":
			o["says"] = "Different promise."
		case "view":
			v["model"].(map[string]any)["view"] = "{}"
		case "chart":
			v["model"].(map[string]any)["reader"].(map[string]any)["chart"].(map[string]any)["initial"] = []any{}
		case "receipt":
			v["receipt"].(map[string]any)["model_only"] = false
		}
		var changed formalize.Proposal
		if err := json.Unmarshal(reader603JSON(t, raw), &changed); err != nil {
			t.Fatal(err)
		}
		if err := preview639Validate(t, &changed); err == nil {
			t.Fatalf("%s tampering accepted", kind)
		}
		if err := preview639Export(t, &changed, t.TempDir()); err == nil {
			t.Fatalf("%s tampering exported", kind)
		}
	}
}
