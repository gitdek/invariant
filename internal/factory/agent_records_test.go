package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// These tests show every record of the factory's work naming the coding
// agent that did it (#179): each agent run's record and its result, the
// steps the watcher lists for its status file, each draft's and build's
// post and its marker, the merge post's numbers, and the pull request's
// body and receipt. A record names the agent and nothing else about it.

// arFormalizer, arBuilder and arPlanBuilder call look as each agent run
// starts, once its record says it's going.
type arFormalizer struct {
	Formalizer
	look func()
}

func (a arFormalizer) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	a.look()
	return a.Formalizer.Formalize(ctx, req, out)
}

type arBuilder struct {
	Builder
	look func()
}

func (a arBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	a.look()
	return a.Builder.Build(ctx, dir, out, amend)
}

type arPlanBuilder struct {
	PlanBuilder
	look func()
}

func (a arPlanBuilder) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	a.look()
	return a.PlanBuilder.Build(ctx, root, n, out)
}

// arWatch has each agent the rig's watcher runs, its own and the others,
// read issue n's run records as its run starts. It returns what they said of
// each run that was going, in order.
func arWatch(t *testing.T, r *rig, n int) *[]string {
	t.Helper()
	var going []string
	look := func() {
		for _, subject := range strings.Split(git(t, r.origin, "for-each-ref", "--format=%(subject)", fmt.Sprintf("%s%d/", runsRef, n)), "\n") {
			if strings.HasPrefix(subject, recordedMark) && !slices.Contains(going, subject) {
				going = append(going, subject)
			}
		}
	}
	wrap := func(run Runners) Runners {
		if run.Formalizer != nil {
			run.Formalizer = arFormalizer{run.Formalizer, look}
		}
		if run.Builder != nil {
			run.Builder = arBuilder{run.Builder, look}
		}
		if run.Plumbing != nil {
			run.Plumbing = arPlanBuilder{run.Plumbing, look}
		}
		return run
	}
	own := wrap(Runners{Formalizer: r.f.Formalizer, Builder: r.f.Builder, Plumbing: r.f.Plumbing})
	r.f.Formalizer, r.f.Builder, r.f.Plumbing = own.Formalizer, own.Builder, own.Plumbing
	for name, run := range r.f.Agents {
		r.f.Agents[name] = wrap(run)
	}
	return &going
}

// arSteps hears the steps the watcher lists for its status file each time
// they change, as "#n doing agent".
func arSteps(r *rig) *[]string {
	var heard []string
	r.f.Activity = func(steps []Step) {
		for _, s := range steps {
			heard = append(heard, fmt.Sprintf("#%d %s %s", s.Issue, s.Doing, s.Agent))
		}
	}
	return &heard
}

// arResults is what each of issue n's run records says now, by step: the
// commit its ref points at, and that commit's subject.
func arResults(t *testing.T, r *rig, n int) map[string][2]string {
	t.Helper()
	out := map[string][2]string{}
	prefix := fmt.Sprintf("%s%d/", runsRef, n)
	for _, line := range strings.Split(git(t, r.origin, "for-each-ref", "--format=%(refname) %(objectname) %(subject)", prefix), "\n") {
		ref, rest, _ := strings.Cut(line, " ")
		sha, subject, _ := strings.Cut(rest, " ")
		step, _ := strings.CutPrefix(ref, prefix)
		out[step] = [2]string{sha, subject}
	}
	return out
}

// arBuild is issue n's build record, by its step's name.
func arBuild(results map[string][2]string) (string, [2]string) {
	for step, rec := range results {
		if strings.HasPrefix(step, "build-") {
			return step, rec
		}
	}
	return "", [2]string{}
}

// Every record of an issue's work names the agent that did it: codex, for
// an issue whose Agent: line picks it, and the watcher's own, claude-code,
// for one that picks none. A run's record names it from the moment it's
// recorded, before the run starts, and its result keeps it. The watcher's
// step names it while the run goes. The draft's post and the build's name
// the agent that drafted and built it, in a line and in their markers, and
// the ratification's post, which ran no agent, names none. The pull
// request's body names the agents that drafted, built and reviewed it, and
// its receipt the agent that built the code. The merge post's numbers name
// every agent that ran.
func TestEveryRecordOfAnIssuesWorkNamesItsAgent(t *testing.T) {
	for _, c := range []struct{ name, lines, agent string }{
		{"codex", "Agent: codex", "codex"},
		{"no line", "", "claude-code"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks = nil
			_, build, _ := agCodex(t, r)
			r.build.review, build.review = "No problem.", "No problem."
			going, steps := arWatch(t, r, 1), arSteps(r)
			r.gh.open(1, "gitdek", "Add a bounded buffer", agBody(c.lines))
			r.poll()
			proposal := r.expect(1, KindProposal, LabelProposal)
			agRatify(t, r, 1)
			pr := r.expect(1, KindPR, LabelPR)
			r.gh.ci(pr.Marker.PR, "success")
			r.poll()
			merged := r.expect(1, KindMerged, LabelMerged)
			by := " by " + c.agent

			// The runs' records, while each run went, and their results.
			if want := []string{recordedMark + "a draft for #1" + by, recordedMark + "the build of " + pr.Marker.Project + " for #1" + by}; !slices.Equal(*going, want) {
				t.Errorf("the records said, as their runs started:\n%q\nwant\n%q", *going, want)
			}
			results := arResults(t, r, 1)
			if got, want := results["draft-issue"][1], "invariant: the result of a draft for #1"+by; got != want {
				t.Errorf("the draft's result says %q; want %q", got, want)
			}
			step, rec := arBuild(results)
			if want := "invariant: the result of the build for #1" + by; rec[1] != want {
				t.Errorf("the build's result, %s, says %q; want %q", step, rec[1], want)
			}
			var built builtResult
			if err := json.Unmarshal([]byte(git(t, r.origin, "show", rec[0]+":result.json")), &built); err != nil {
				t.Fatal(err)
			}
			if built.Result == nil || built.Result.Final == nil {
				t.Fatalf("the build's result holds no receipt: %+v", built)
			}
			if built.Agent != c.agent || built.Result.Final.Agent != c.agent {
				t.Errorf("the build's result names %q, and its receipt %q; want %s for both", built.Agent, built.Result.Final.Agent, c.agent)
			}

			// The steps the watcher listed for its status file.
			for _, want := range []string{"#1 formalizing " + c.agent, "#1 building " + c.agent} {
				if !slices.Contains(*steps, want) {
					t.Errorf("the watcher never listed %q:\n%q", want, *steps)
				}
			}
			for _, s := range *steps {
				if !strings.HasSuffix(s, " ") && !strings.HasSuffix(s, " "+c.agent) {
					t.Errorf("the watcher listed %q; want only %s as the agent", s, c.agent)
				}
			}

			// The posts.
			drafted, builtBy := "_Drafted by `"+c.agent+"`._", "_Built by `"+c.agent+"`._"
			if proposal.Marker.Agent != c.agent || !strings.Contains(proposal.Comment.Body, drafted) {
				t.Errorf("the proposal's marker names %q; want %s, and a line saying %s:\n%s", proposal.Marker.Agent, c.agent, drafted, proposal.Comment.Body)
			}
			if pr.Marker.Agent != c.agent || !strings.Contains(pr.Comment.Body, builtBy) {
				t.Errorf("the build's marker names %q; want %s, and a line saying %s:\n%s", pr.Marker.Agent, c.agent, builtBy, pr.Comment.Body)
			}
			for _, p := range r.gh.posts(1) {
				if p.Marker.Kind == KindRatified || p.Marker.Kind == KindMerged {
					if p.Marker.Agent != "" || strings.Contains(p.Comment.Body, " by `") {
						t.Errorf("the %s post names an agent, but it ran none (%q):\n%s", p.Marker.Kind, p.Marker.Agent, p.Comment.Body)
					}
				}
			}

			// The pull request.
			body := r.gh.prBody(pr.Marker.PR)
			for _, want := range []string{
				"- **Agents:** drafted by `" + c.agent + "`, built and reviewed by `" + c.agent + "`\n",
				"Coding agent: `" + c.agent + "`. The fingerprint leaves it out.",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("the pull request's body lacks %q:\n%s", want, body)
				}
			}

			// The merge's numbers.
			if n := merged.Marker.Numbers; n == nil || !slices.Equal(n.Agents, []string{c.agent}) {
				t.Errorf("the merge's numbers are %+v; want the agent %s", n, c.agent)
			}
			if want := "Its coding agent was `" + c.agent + "`."; !strings.Contains(merged.Comment.Body, want) {
				t.Errorf("the merge post doesn't say %q:\n%s", want, merged.Comment.Body)
			}
		})
	}
}

// The merge post's numbers name every agent that ran for the issue, each
// once, in the order they first ran. Here the watcher's own agent drafted
// the issue first, then its line picked codex, and a writer's revise had
// codex draft it again. The pull request names codex as the agent that
// drafted what was ratified, as the post that proposed it records. Posts
// that ran no agent, such as CI's failure and the answer to a writer's
// retry, carry the marker forward with no agent, and say none.
func TestTheMergeNamesEveryAgentThatRanForTheIssue(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	agCodex(t, r)
	r.gh.open(1, "gitdek", "Add a bounded buffer", agBody(""))
	r.poll()
	if first := r.expect(1, KindProposal, LabelProposal); first.Marker.Agent != claudeCode {
		t.Fatalf("the first draft's marker names %q; want %s", first.Marker.Agent, claudeCode)
	}
	r.gh.issues[1].Body = agBody("Agent: codex")
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	if second := r.expect(1, KindProposal, LabelProposal); second.Marker.Agent != "codex" || !strings.Contains(second.Comment.Body, "_Drafted by `codex`._") {
		t.Fatalf("the revised draft's marker names %q; want codex:\n%s", second.Marker.Agent, second.Comment.Body)
	}
	agRatify(t, r, 1)
	pr := r.expect(1, KindPR, LabelPR)
	if body := r.gh.prBody(pr.Marker.PR); !strings.Contains(body, "- **Agents:** drafted by `codex`, built by `codex`\n") {
		t.Errorf("the pull request's body should name codex as the agent that drafted and built it:\n%s", body)
	}
	r.gh.ci(pr.Marker.PR, "failure")
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	r.gh.ci(pr.Marker.PR, "success")
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	again := r.expect(1, KindPR, LabelPR)
	for _, p := range []Post{failed, again} {
		if p.Marker.Agent != "" || strings.Contains(p.Comment.Body, "by `") {
			t.Errorf("the %s post ran no agent, but names %q:\n%s", p.Marker.Kind, p.Marker.Agent, p.Comment.Body)
		}
	}
	r.poll()
	merged := r.expect(1, KindMerged, LabelMerged)
	if n := merged.Marker.Numbers; n == nil || !slices.Equal(n.Agents, []string{claudeCode, "codex"}) {
		t.Errorf("the merge's numbers are %+v; want claude-code, then codex", n)
	}
	if want := "Its coding agents were `claude-code` and `codex`."; !strings.Contains(merged.Comment.Body, want) {
		t.Errorf("the merge post doesn't say %q:\n%s", want, merged.Comment.Body)
	}
}

// A build's post names the agent that built it, whether the build passed or
// failed: its final gate failed, its agent stopped with nothing to show, or
// the factory stopped partway through its run. A build that ran no agent,
// because the watcher can't run the issue's, names none.
func TestABuildsPostNamesTheAgentThatBuiltIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(r *rig, codex *fakeBuilder)
		ran   bool
	}{
		{"its gate failed", func(r *rig, codex *fakeBuilder) { codex.pass = false }, true},
		{"its agent stopped", func(r *rig, codex *fakeBuilder) { codex.stop = true }, true},
		{"the factory stopped", func(r *rig, codex *fakeBuilder) { codex.crash = true }, true},
		{"no agent ran", func(r *rig, codex *fakeBuilder) { r.f.Agents = nil }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks = nil
			_, codex, _ := agCodex(t, r)
			r.gh.open(1, "gitdek", "Add a bounded buffer", agBody("Agent: codex"))
			r.poll()
			p := r.expect(1, KindProposal, LabelProposal)
			c.setup(r, codex)
			r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
			if err := r.f.Poll(context.Background()); codex.crash {
				// The factory stops mid-build. The next poll says so.
				if err == nil || !strings.Contains(err.Error(), "partway") {
					t.Fatalf("the crash: %v", err)
				}
				codex.crash = false
				r.poll()
			} else if err != nil {
				t.Fatal(err)
			}
			failed := r.expect(1, KindFailed, LabelHumanReview)
			line := strings.Contains(failed.Comment.Body, "_Built by `codex`._")
			switch {
			case c.ran && (failed.Marker.Agent != "codex" || !line):
				t.Errorf("the failed build's marker names %q; want codex, and a line saying it built it:\n%s", failed.Marker.Agent, failed.Comment.Body)
			case !c.ran && (failed.Marker.Agent != "" || line):
				t.Errorf("no agent ran, but the failed build's post names %q:\n%s", failed.Marker.Agent, failed.Comment.Body)
			}
		})
	}
}

// A draft's post names the agent that drafted it, whether it asks, proposes
// or is stuck. The answer to an issue whose agent the watcher can't run ran
// no agent, and names none.
func TestADraftsPostNamesTheAgentThatDraftedIt(t *testing.T) {
	r := newRig(t)
	codex, _, _ := agCodex(t, r)
	codex.fail = "Design: TLC: invariant WithinCap is violated"
	r.gh.open(1, "gitdek", "Add a bounded buffer", agBody(""))
	r.gh.open(2, "gitdek", "Add a bounded buffer", agBody("Agent: codex"))
	r.gh.open(3, "gitdek", "Add a bounded buffer", agBody("Agent: gemini"))
	r.poll()
	forks := r.expect(1, KindForks, LabelAsking)
	stuck := r.expect(2, KindStuck, LabelHumanReview)
	for _, c := range []struct {
		post  Post
		agent string
	}{{forks, claudeCode}, {stuck, "codex"}} {
		if c.post.Marker.Agent != c.agent || !strings.Contains(c.post.Comment.Body, "_Drafted by `"+c.agent+"`._") {
			t.Errorf("the %s post's marker names %q; want %s, and a line saying it drafted it:\n%s", c.post.Marker.Kind, c.post.Marker.Agent, c.agent, c.post.Comment.Body)
		}
	}
	answer := r.expect(3, KindUnsupported, LabelHumanReview)
	if answer.Marker.Agent != "" || strings.Contains(answer.Comment.Body, "_Drafted by") {
		t.Errorf("nothing drafted the answer, but it names %q:\n%s", answer.Marker.Agent, answer.Comment.Body)
	}
}

// A plan's records name its agent as a project's do: its draft's and its
// build's runs and results, its posts, and its pull request, which names
// the agent that drafted it, and the agent that built and reviewed it.
func TestAPlansRecordsNameItsAgent(t *testing.T) {
	r, _ := plumbingRig(t, "page/page.go")
	agCodex(t, r)
	going := arWatch(t, r, 1)
	r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\nAgent: codex\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	agRatify(t, r, 1)
	pr := r.expect(1, KindPR, LabelPR)
	if want := []string{recordedMark + "a draft for #1 by codex", recordedMark + "the build of #1's plan by codex"}; !slices.Equal(*going, want) {
		t.Errorf("the records said, as their runs started:\n%q\nwant\n%q", *going, want)
	}
	step, rec := arBuild(arResults(t, r, 1))
	if want := "invariant: the result of the build for #1 by codex"; rec[1] != want {
		t.Errorf("the build's result, %s, says %q; want %q", step, rec[1], want)
	}
	if !strings.Contains(plan.Comment.Body, "_Drafted by `codex`._") || !strings.Contains(pr.Comment.Body, "_Built by `codex`._") || pr.Marker.Agent != "codex" {
		t.Errorf("the plan's posts should name codex:\n%s\n\n%s", plan.Comment.Body, pr.Comment.Body)
	}
	if body := r.gh.prBody(pr.Marker.PR); !strings.Contains(body, "- **Agents:** drafted by `codex`, built and reviewed by `codex`\n") {
		t.Errorf("the pull request's body should name codex:\n%s", body)
	}
}

// A post names its marker's agent in a line of its own: the agent that
// drafted it, for a draft's post, and the one that built it, for a build's.
// Any other post, and one whose marker names no agent, has no such line.
func TestAPostNamesItsMarkersAgent(t *testing.T) {
	for kind, want := range map[string]string{
		KindForks: "Drafted by", KindProposal: "Drafted by", KindStuck: "Drafted by", KindUnsupported: "Drafted by",
		KindPR: "Built by", KindFailed: "Built by",
		KindRatified: "", KindMerged: "", KindClosed: "", KindNote: "",
	} {
		body := post("x", "Some text.", Marker{Kind: kind, Agent: "codex"})
		line := "Some text.\n\n_" + want + " `codex`._\n\n<!-- invariant:"
		switch {
		case want != "" && !strings.Contains(body, line):
			t.Errorf("a %s post should end its text with %q:\n%s", kind, "_"+want+" `codex`._", body)
		case want == "" && strings.Contains(body, "`codex`"):
			t.Errorf("a %s post names its agent:\n%s", kind, body)
		}
		if body := post("x", "Some text.", Marker{Kind: kind}); strings.Contains(body, " by `") {
			t.Errorf("a %s post whose marker names no agent names one:\n%s", kind, body)
		}
	}
}

// The numbers name each agent once, in the order the posts first name
// them, and say so in plain words. A carried marker names no agent, since
// it reports no run.
func TestNumbersNameEachAgentOnce(t *testing.T) {
	th := Thread{Posts: []Post{
		{Marker: Marker{Kind: KindForks, Agent: claudeCode}},
		{Marker: Marker{Kind: KindProposal, Agent: "codex"}},
		{Marker: Marker{Kind: KindRatified}},
		{Marker: Marker{Kind: KindPR, Agent: "codex"}},
		{Marker: Marker{Kind: KindPR, Agent: claudeCode}},
	}}
	n := NumbersOf(th, time.Now())
	if !slices.Equal(n.Agents, []string{claudeCode, "codex"}) {
		t.Errorf("agents %q; want claude-code, then codex", n.Agents)
	}
	for agents, want := range map[string]string{
		"":                  "",
		"codex":             " Its coding agent was `codex`.",
		"claude-code codex": " Its coding agents were `claude-code` and `codex`.",
	} {
		s := Numbers{Agents: strings.Fields(agents)}.Sentence()
		if got := s[strings.Index(s, "people.")+len("people."):]; got != want {
			t.Errorf("the sentence for %q ends %q; want %q", agents, got, want)
		}
	}
	if m := (Marker{Kind: KindPR, Agent: "codex", Spend: 0.25}).carried(); m.Agent != "" {
		t.Errorf("a carried marker names %q", m.Agent)
	}
}
