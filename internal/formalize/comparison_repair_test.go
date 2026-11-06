package formalize_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

func TestComparisonRepairKeepsCapturedSources(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	for _, mode := range []string{"amendment view", "amendment unreadable", "revision unreadable"} {
		t.Run(mode, func(t *testing.T) {
			cur := &formalize.Current{Dir: "examples/reader", Manifest: old.Manifest(), ModuleText: old.ModuleText, View: old.View, Includes: old.Includes,
				Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner", Proposal: old.Hash}, Bounds: old.Bounds, Statements: old.Statements}}
			req := formalize.Request{Current: cur}
			if mode == "revision unreadable" {
				req.Current = nil
				req.Previous = old
			}
			bad := reader603Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				file, text := "proposal.json", "{"
				if mode == "amendment view" {
					file, text = "view.json", `{"version":99}`
				}
				reader603Write(t, filepath.Join(j.Workspace, file), text)
				return synth.Usage{}, nil
			}}
			r, err := (formalize.Formalizer{Backend: bad, Toolchain: tc}).Formalize(t.Context(), req, t.TempDir())
			if err != nil || r.Problem == "" || r.Proposal == nil || r.Proposal.Baseline == nil {
				t.Fatalf("repair lost caller source: %+v %v", r, err)
			}
			if err := r.Proposal.ValidateReader(); err == nil {
				t.Fatal("failed comparison remained publishable")
			}
			var saved formalize.Proposal
			if err := json.Unmarshal(reader603JSON(t, r.Proposal), &saved); err != nil {
				t.Fatal(err)
			}
			cur.ModuleText += "\\* moved branch\n"
			cur.View = strings.ReplaceAll(cur.View, "Working", "Branch moved")
			req.Previous = &saved
			good := reader603Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				return synth.Usage{}, nil
			}}
			r, err = (formalize.Formalizer{Backend: good, Toolchain: tc}).Formalize(t.Context(), req, t.TempDir())
			if err != nil || r.Problem != "" {
				t.Fatalf("repair: %+v %v", r, err)
			}
			c, _ := comparison648Of(t, r.Proposal)
			if c.Baseline.Model != old.ModuleText || c.Baseline.View != old.View {
				t.Fatal("repair replaced captured source with failed draft or moved branch")
			}
		})
	}
}
