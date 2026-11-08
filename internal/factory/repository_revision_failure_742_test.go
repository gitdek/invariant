package factory

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/setup"
	"github.com/gitdek/invariant/internal/synth"
)

type failedRevision742 struct {
	t       *testing.T
	problem string
}

func (failedRevision742) Name() string { return "failed revision742" }
func (a failedRevision742) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	captured742(a.t, job)
	return synth.Usage{}, errors.New(a.problem)
}

func TestConfiguredFailedRevisionRetainsAuthority742(t *testing.T) {
	goCache := t.TempDir()
	for _, failure := range []string{"planning failure", "provider unavailable after 4 attempts", "missing runner"} {
		for _, change := range []string{"guidance", "gate pin"} {
			t.Run(failure+"/"+change, func(t *testing.T) {
				x := new742(t, "pytest", false, goCache)
				original := x.start().Marker.Proposal
				retained := func(p *formalize.Proposal) {
					t.Helper()
					if !reflect.DeepEqual(p, original) {
						t.Fatalf("revision lost the original proposal and execution authority: %+v", p)
					}
				}
				backend := x.planner.real.Backend
				kind := KindStuck
				if failure == "missing runner" {
					x.r.f.Formalizer = nil
					kind = KindUnsupported
				} else {
					x.planner.real.Backend = failedRevision742{t: t, problem: failure}
				}
				x.r.gh.say(1, "gitdek", "/invariant revise")
				x.r.poll()
				retained(x.r.expect(1, kind, LabelHumanReview).Marker.Proposal)
				x.planner.real.Backend = backend
				x.r.f.Formalizer = x.planner
				x.changeBranch("main", func(root string) {
					for _, file := range []string{"AGENTS.md", "worker/AGENTS.md", "STATE.md"} {
						write742(t, root, file, "Later guidance must not replace captured authority742.\n")
					}
					if change == "gate pin" {
						workflow, err := setup.Workflow(strings.Repeat("b", 40))
						if err != nil {
							t.Fatal(err)
						}
						write742(t, root, setup.WorkflowPath, workflow)
					}
				})
				posts, requests, opened := len(x.r.gh.posts(1)), len(x.planner.requests), x.runner.opened
				x.restart()
				x.r.poll()
				x.r.poll()
				if len(x.r.gh.posts(1)) != posts || len(x.planner.requests) != requests || x.runner.opened != opened {
					t.Fatal("restart repeated a failed revision without a writer")
				}
				x.r.gh.say(1, "gitdek", "/invariant revise")
				x.r.poll()
				if change == "gate pin" {
					hold := x.r.expect(1, KindStuck, LabelHumanReview)
					retained(hold.Marker.Proposal)
					visible := strings.Split(hold.Comment.Body, "<!-- invariant:")[0]
					if !strings.Contains(visible, "invariant/gate") || !strings.Contains(visible, "changed") {
						t.Fatalf("revision did not explain changed gate authority: %s", visible)
					}
					if len(x.planner.requests) != requests || x.runner.opened != opened || x.build.calls != 0 || len(x.r.gh.prs) != 0 {
						t.Fatal("changed gate authority reached planning, checks or publication")
					}
					x.changeBranch("main", func(root string) {
						workflow, err := x.clone.Show(t.Context(), original.Plan.Execution.Base, setup.WorkflowPath)
						if err != nil {
							t.Fatal(err)
						}
						write742(t, root, setup.WorkflowPath, string(workflow))
					})
					posts = len(x.r.gh.posts(1))
					x.restart()
					x.r.poll()
					if len(x.r.gh.posts(1)) != posts || len(x.planner.requests) != requests || x.runner.opened != opened {
						t.Fatal("restoring the gate restarted a revision without a writer")
					}
					x.r.gh.say(1, "gitdek", "/invariant revise")
					x.r.poll()
				}
				p := x.r.expect(1, KindProposal, LabelProposal)
				retained(p.Marker.Proposal)
				if len(x.planner.requests) != requests+1 {
					t.Fatal("writer revision did not draft exactly once")
				}
				retained(x.planner.requests[requests].Previous)
				x.ratify(p)
				x.published(false)
				if x.build.calls != 1 {
					t.Fatal("revision recovery rebuilt or bypassed the real builder")
				}
			})
		}
	}
}
