package formalize_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/synth"
)

func fixed701Guidance(t *testing.T, text, cause string) {
	t.Helper()
	for _, want := range []string{cause, "examples/reader-701/.invariant/view.json", "separate change", "new amendment issue", "revis"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("fixed captured view guidance omits %q: %s", want, text)
		}
	}
	if strings.Contains(text, "repair baseline/view.json") || strings.Contains(text, "supply a pure `baseline/view.json`") {
		t.Errorf("fixed captured view offered an impossible workspace repair: %s", text)
	}
}

func TestComparison701FixedViewRefusalAndRecapture(t *testing.T) {
	for _, badView := range []struct{ name, view, cause string }{
		{"malformed", `{"version":99}`, "invalid_view"},
		{"hiding", strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1), "unreachable_chart"},
	} {
		t.Run(badView.name, func(t *testing.T) {
			tc, _ := reader603Tools(t)
			cur := baseline701Current(t, badView.view, "2")
			req := formalize.Request{Repo: "o/r", Issue: 701, Current: cur}
			fixed701Guidance(t, formalize.Prompt(req, 4), "")
			badAgent := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				// Even a valid replacement cannot supersede a nonempty capture.
				reader603Write(t, filepath.Join(j.Workspace, "baseline/view.json"), reader603View)
				reader603Write(t, filepath.Join(j.Workspace, "baseline/Reader603.tla"), "replacement model")
				reader603Write(t, filepath.Join(j.Workspace, "baseline/ratified.lock"), `{}`)
				authority := loadDraftContext(t, ctx, j, 0)
				reader603Write(t, filepath.Join(j.Workspace, "check-context.json"), `{}`)
				for _, call := range []func() error{
					func() error { _, _, err := formalize.Check(ctx, j.Workspace, tc); return err },
					func() error { _, _, err := formalize.CheckWithContext(ctx, j.Workspace, tc, authority); return err },
				} {
					err := call()
					if err == nil {
						t.Fatal("staged replacement repaired a fixed captured view")
					}
					fixed701Guidance(t, err.Error(), badView.cause)
					if badView.name == "hiding" && (!strings.Contains(err.Error(), "advance") || !strings.Contains(err.Error(), "Advance")) {
						t.Fatal("fixed-view wrapper lost original rule/action refusal detail")
					}
				}
				return synth.Usage{}, nil
			}}
			out := t.TempDir()
			r, err := (formalize.Formalizer{Backend: badAgent, Toolchain: tc}).Formalize(t.Context(), req, out)
			if err != nil || r == nil || r.Problem == "" {
				t.Fatalf("fixed view final: %+v %v", r, err)
			}
			fixed701Guidance(t, r.Problem, badView.cause)
			if r.Proposal == nil || r.Proposal.Target == nil || r.Proposal.Target.Baseline == nil || r.Proposal.Target.Baseline.View != badView.view {
				t.Fatal("refusal lost the fixed amendment baseline")
			}
			var saved formalize.Result
			data, err := os.ReadFile(filepath.Join(out, "formalization.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			// A real view-only branch repair retains the same lock and model.
			moved := *cur
			moved.View, moved.Commit = reader603View, "view-only-fix-701"
			req.Current, req.Previous = &moved, saved.Proposal
			fixed701Guidance(t, formalize.Prompt(req, 4), "")
			r, err = (formalize.Formalizer{Backend: badAgent, Toolchain: tc}).Formalize(t.Context(), req, t.TempDir())
			if err != nil || r == nil || r.Problem == "" {
				t.Fatalf("existing issue picked up view-only fix: %+v %v", r, err)
			}
			fixed701Guidance(t, r.Problem, badView.cause)
			// A new amendment has no previous capture and can use the repaired view.
			goodAgent := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				p := baseline701Check(t, ctx, j.Workspace, tc)
				baseline701AssertSources(t, p, &moved, moved.View)
				return synth.Usage{}, nil
			}}
			r, err = (formalize.Formalizer{Backend: goodAgent, Toolchain: tc}).Formalize(t.Context(), formalize.Request{Repo: "o/r", Issue: 702, Current: &moved}, t.TempDir())
			if err != nil || r == nil || r.Problem != "" {
				t.Fatalf("new amendment did not capture repaired view: %+v %v", r, err)
			}
			baseline701AssertSources(t, r.Proposal, &moved, moved.View)
		})
	}
}
