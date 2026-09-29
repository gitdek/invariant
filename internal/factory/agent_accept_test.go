package factory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

// These tests show an issue picking its coding agent (#173): with an Agent:
// line of its own, else through its project's manifest, else the watcher's
// own agent. Every agent run for the issue is that agent's: the draft of its
// statements, its plan or its plan of issues, and its build, whose review
// the builder has the same agent make.

// agBody is an issue that asks for a bounded buffer, with lines of its own
// before its /invariant solve.
func agBody(lines string) string {
	return "Producers put log lines in a buffer; a shipper takes them out in order.\n\n" + lines + "\n\n/invariant solve"
}

// agCodex has the rig's watcher run codex too, as it will once Codex comes,
// beside its own agent, claude-code, which the rig's formalizer and builders
// run. Codex's formalizer, builder and plan builder are its own, and record
// what they're asked to do.
func agCodex(t *testing.T, r *rig) (*scriptedFormalizer, *fakeBuilder, *fakePlanBuilder) {
	t.Helper()
	form := &scriptedFormalizer{t: t, amend: r.form.amend, plan: r.form.plan}
	build := &fakeBuilder{pass: true}
	plan := &fakePlanBuilder{pass: true, approve: true}
	r.f.Agents = map[string]Runners{"codex": {Formalizer: form, Builder: build, Plumbing: plan}}
	return form, build, plan
}

// agRuns is how many times the watcher's own agent and codex each ran, when
// only codex should have run once, or only the watcher's own.
func agRuns(codex bool) [2]int {
	if codex {
		return [2]int{0, 1}
	}
	return [2]int{1, 0}
}

// agRatify ratifies the proposal the factory last posted on an issue, and
// polls.
func agRatify(t *testing.T, r *rig, issue int) {
	t.Helper()
	p := r.gh.last(issue).Marker.Proposal
	if p == nil {
		t.Fatalf("#%d has no proposal to ratify", issue)
	}
	r.gh.say(issue, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	r.poll()
}

// agManifest is the manifest of the project a pull request builds, as its
// branch holds it.
func agManifest(t *testing.T, r *rig, pr Post) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(git(t, r.origin, "show", pr.Marker.Branch+":"+pr.Marker.Project+"/.invariant/invariant.json")), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// agPick moves the project at dir on the origin's main to another agent, as
// a person does by changing its manifest.
func agPick(t *testing.T, r *rig, dir, agent string) {
	t.Helper()
	work := t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	file := filepath.Join(work, filepath.FromSlash(dir), ".invariant", "invariant.json")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["agent"] = agent
	if b, err = json.MarshalIndent(m, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "Move "+dir+" to "+agent)
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
}

// An issue's own Agent: line picks the coding agent that drafts its
// statements and builds its code, and the manifest of the project it creates
// records it. The line is read as a Project: line is, outside code blocks,
// the first one counting. With no line, the watcher's own agent does both,
// and the manifest records none, so the project goes on following the
// watcher.
func TestAnIssuesAgentLinePicksTheAgentThatDraftsAndBuilds(t *testing.T) {
	for _, c := range []struct {
		name     string
		lines    string
		codex    bool
		recorded string
	}{
		{"codex", "Agent: codex", true, "codex"},
		{"claude-code", "Agent: claude-code", false, "claude-code"},
		{"no line", "", false, ""},
		{"the first outside code", "```\nAgent: claude-code\n```\n\nagent: `Codex`\nAgent: claude-code", true, "codex"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks = nil
			form, build, _ := agCodex(t, r)
			r.gh.open(1, "gitdek", "Add a bounded buffer", agBody(c.lines))
			r.poll()
			r.expect(1, KindProposal, LabelProposal)
			drafted := [2]int{len(r.form.requests), len(form.requests)}
			if drafted != agRuns(c.codex) {
				t.Fatalf("the watcher's own agent and codex drafted %v times; want %v", drafted, agRuns(c.codex))
			}
			agRatify(t, r, 1)
			pr := r.expect(1, KindPR, LabelPR)
			built := [2]int{len(r.build.built), len(build.built)}
			if built != agRuns(c.codex) {
				t.Fatalf("the watcher's own agent and codex built %v times; want %v", built, agRuns(c.codex))
			}
			got, has := agManifest(t, r, pr)["agent"]
			switch {
			case c.recorded == "" && has:
				t.Errorf("the new project's manifest names the agent %v, but its issue picked none", got)
			case c.recorded != "" && got != c.recorded:
				t.Errorf("the new project's manifest names the agent %v; want %q, which its issue picked", got, c.recorded)
			}
		})
	}
}

// A project's manifest picks the agent for an issue that changes it and
// picks none itself, and an issue's own line picks for that issue alone. An
// amendment keeps its project's manifest as it is (D-0046), so the project
// still names its agent on the amendment's branch either way.
func TestAProjectsManifestPicksTheAgentUnlessItsIssueDoes(t *testing.T) {
	const dir = "examples/03-bounded-buffer"
	for _, c := range []struct {
		name  string
		lines string
		codex bool
	}{
		{"the manifest's", "Project: " + dir, true},
		{"the issue's own", "Agent: claude-code\nProject: " + dir, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks, r.form.amend = nil, dropCanFill
			form, build, _ := agCodex(t, r)
			seed(t, r, dir, 1)
			agPick(t, r, dir, "codex")
			r.gh.open(5, "gitdek", "Stop promising the buffer can fill", agBody(c.lines))
			r.poll()
			r.expect(5, KindProposal, LabelProposal)
			drafted := [2]int{len(r.form.requests), len(form.requests)}
			if drafted != agRuns(c.codex) {
				t.Fatalf("the watcher's own agent and codex drafted %v times; want %v", drafted, agRuns(c.codex))
			}
			agRatify(t, r, 5)
			pr := r.expect(5, KindPR, LabelPR)
			built := [2]int{len(r.build.built), len(build.built)}
			if built != agRuns(c.codex) || pr.Marker.Project != dir {
				t.Fatalf("the watcher's own agent and codex built %v times, the project %s; want %v, and %s", built, pr.Marker.Project, agRuns(c.codex), dir)
			}
			if !slices.Equal(append(r.build.amended, build.amended...), []bool{true}) {
				t.Errorf("the build should change the project's code: amended %v and %v", r.build.amended, build.amended)
			}
			if got := agManifest(t, r, pr)["agent"]; got != "codex" {
				t.Errorf("the amendment's branch has the project's manifest name the agent %v; want codex, as it was", got)
			}
		})
	}
}

// An issue that picks an agent the watcher can't run, or one it doesn't
// know, gets one answer before anything's drafted, as a plumbing issue on
// another repository does (D-0121). The answer names the agent the issue
// picked and every agent the watcher can run, and the issue is left for a
// person: nothing is drafted, no agent run is recorded, and no branch is
// pushed. An issue whose project's manifest picks such an agent, and that
// picks none itself, gets the same.
func TestAnAgentTheWatcherCantRunGetsOneAnswer(t *testing.T) {
	const dir = "examples/03-bounded-buffer"
	for _, c := range []struct {
		name   string
		lines  string
		codex  bool
		picked string
		can    []string
	}{
		{"codex, which it can't run yet", "Agent: codex", false, "codex", []string{"claude-code"}},
		{"one it doesn't know", "Agent: gemini", true, "gemini", []string{"claude-code", "codex"}},
		{"its project's", "Project: " + dir, false, "codex", []string{"claude-code"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks, r.form.amend = nil, dropCanFill
			codex := &scriptedFormalizer{t: t}
			if c.codex {
				codex, _, _ = agCodex(t, r)
			}
			seed(t, r, dir, 1)
			agPick(t, r, dir, "codex")
			r.gh.open(7, "gitdek", "Add a bounded buffer", agBody(c.lines))
			r.poll()
			answer := r.expect(7, KindUnsupported, LabelHumanReview)
			if !slices.Contains(answer.Marker.ReplyTo, 0) {
				t.Errorf("the answer should reply to the issue's /invariant solve: %v", answer.Marker.ReplyTo)
			}
			for _, name := range append([]string{c.picked}, c.can...) {
				if !strings.Contains(answer.Comment.Body, name) {
					t.Errorf("the answer doesn't name %s:\n%s", name, answer.Comment.Body)
				}
			}
			if len(r.form.requests) != 0 || len(codex.requests) != 0 {
				t.Errorf("the issue was drafted: %d drafts by the watcher's own agent, %d by codex", len(r.form.requests), len(codex.requests))
			}
			if runs := git(t, r.origin, "for-each-ref", "refs/invariant/runs/"); runs != "" {
				t.Errorf("an agent run was recorded:\n%s", runs)
			}
			if heads := git(t, r.origin, "for-each-ref", "--format=%(refname)", "refs/heads/"); heads != "refs/heads/main" {
				t.Errorf("branches = %q; want only main", heads)
			}
			// It answers once: later polls say nothing more, and draft and
			// build nothing.
			before := len(r.gh.comments[7])
			r.poll()
			r.poll()
			if len(r.gh.comments[7]) != before || len(r.form.requests) != 0 || len(codex.requests) != 0 || len(r.build.built) != 0 {
				t.Errorf("the factory went on after its answer: %d comments, not %d; %d and %d drafts; built %v",
					len(r.gh.comments[7]), before, len(r.form.requests), len(codex.requests), r.build.built)
			}
		})
	}
}

// Until an issue picks an agent the watcher can run, a writer's revise gets
// the same answer, and nothing is drafted. Once its line names one, a revise
// drafts it with that agent.
func TestARevisedIssueIsDraftedOnceItPicksAnAgentTheWatcherCanRun(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	r.gh.open(1, "gitdek", "Add a bounded buffer", agBody("Agent: codex"))
	r.poll()
	r.expect(1, KindUnsupported, LabelHumanReview)
	revise := r.gh.say(1, "gitdek", "Draft it anyway.\n\n/invariant revise")
	r.poll()
	again := r.expect(1, KindUnsupported, LabelHumanReview)
	if !slices.Contains(again.Marker.ReplyTo, revise.ID) || len(r.form.requests) != 0 {
		t.Fatalf("the revise should get the same answer, and no draft: the answer replies to %v, and %d drafts", again.Marker.ReplyTo, len(r.form.requests))
	}
	r.gh.issues[1].Body = agBody("Agent: claude-code")
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	r.expect(1, KindProposal, LabelProposal)
	if len(r.form.requests) != 1 {
		t.Errorf("the watcher's own agent drafted %d times; want once, now that the issue picks it", len(r.form.requests))
	}
}

// A plumbing issue's line picks the agent that plans it and builds its
// plan, and a PRD's picks the agent that drafts its plan of issues.
func TestAPlanAndAPlanOfIssuesAreTheIssuesAgents(t *testing.T) {
	t.Run("plumbing", func(t *testing.T) {
		r, b := plumbingRig(t, "page/page.go")
		form, _, plan := agCodex(t, r)
		r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\nAgent: codex\n\n/invariant solve")
		r.poll()
		r.expect(1, KindProposal, LabelProposal)
		if len(form.requests) != 1 || form.requests[0].Plumbing == "" || len(r.form.requests) != 0 {
			t.Fatalf("codex should plan the issue: it drafted %d times, and the watcher's own agent %d", len(form.requests), len(r.form.requests))
		}
		agRatify(t, r, 1)
		r.expect(1, KindPR, LabelPR)
		if !slices.Equal(plan.built, []int{1}) || len(b.built) != 0 {
			t.Errorf("codex should build the plan: it built %v, and the watcher's own agent %v", plan.built, b.built)
		}
	})
	t.Run("a PRD", func(t *testing.T) {
		r := newRig(t)
		planner := &prdPlanner{t: t, plans: []*formalize.IssuePlan{graphPlan()}}
		r.f.Agents = map[string]Runners{"codex": {Formalizer: planner}}
		r.gh.open(40, "gitdek", "Draw the decision graph", prdBody+"\n\nAgent: codex")
		r.gh.say(40, "gitdek", "/invariant plan")
		r.poll()
		r.expect(40, KindProposal, LabelProposal)
		if len(planner.requests) != 1 || len(r.form.requests) != 0 {
			t.Errorf("codex should draft the plan of issues: it drafted %d times, and the watcher's own agent %d", len(planner.requests), len(r.form.requests))
		}
	})
}

// The watcher's own agent is the one an issue gets when neither the issue
// nor its project picks one, whichever agent that is. With codex as the
// watcher's own, an issue that picks none is drafted and built by codex, and
// its new project's manifest names no agent, while an issue that picks
// claude-code gets claude-code, when the watcher can run it too. A watcher
// that can run codex alone answers that issue instead, naming codex.
func TestTheWatchersOwnAgentIsTheDefault(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	r.f.Agent = "codex"
	claude := &scriptedFormalizer{t: t}
	claudeBuild := &fakeBuilder{pass: true}
	r.f.Agents = map[string]Runners{"claude-code": {Formalizer: claude, Builder: claudeBuild}}
	r.gh.open(1, "gitdek", "Add a bounded buffer", agBody(""))
	r.gh.open(2, "gitdek", "Add a bounded buffer", agBody("Agent: claude-code"))
	r.poll()
	r.expect(1, KindProposal, LabelProposal)
	r.expect(2, KindProposal, LabelProposal)
	if len(r.form.requests) != 1 || r.form.requests[0].Issue != 1 || len(claude.requests) != 1 || claude.requests[0].Issue != 2 {
		t.Fatalf("codex, the watcher's own, should draft #1, and claude-code #2: codex drafted %d times, claude-code %d", len(r.form.requests), len(claude.requests))
	}
	agRatify(t, r, 1)
	first := r.expect(1, KindPR, LabelPR)
	agRatify(t, r, 2)
	second := r.expect(2, KindPR, LabelPR)
	if len(r.build.built) != 1 || len(claudeBuild.built) != 1 {
		t.Fatalf("codex should build #1, and claude-code #2: codex built %v, claude-code %v", r.build.built, claudeBuild.built)
	}
	if got, has := agManifest(t, r, first)["agent"]; has {
		t.Errorf("#1 picked no agent, but its project's manifest names %v", got)
	}
	if got := agManifest(t, r, second)["agent"]; got != "claude-code" {
		t.Errorf("#2 picked claude-code, but its project's manifest names %v", got)
	}

	alone := newRig(t)
	alone.form.forks = nil
	alone.f.Agent = "codex"
	alone.gh.open(3, "gitdek", "Add a bounded buffer", agBody("Agent: claude-code"))
	alone.poll()
	answer := alone.expect(3, KindUnsupported, LabelHumanReview)
	if !strings.Contains(answer.Comment.Body, "codex") || !strings.Contains(answer.Comment.Body, "claude-code") || len(alone.form.requests) != 0 {
		t.Errorf("a watcher that runs codex alone should answer an issue that picks claude-code, naming both, and draft nothing (%d drafts):\n%s", len(alone.form.requests), answer.Comment.Body)
	}
}

// A build runs its issue's agent and no other. When the watcher can no
// longer run that agent, as after it restarts without it, the build says
// so, naming the agent, and fails before any agent runs, and no build run is
// recorded. Once the watcher can run it again, a writer's retry builds it
// with that agent. A plan's build does the same.
func TestABuildRunsOnlyItsIssuesAgent(t *testing.T) {
	t.Run("a project", func(t *testing.T) {
		r := newRig(t)
		r.form.forks = nil
		_, build, _ := agCodex(t, r)
		codex := r.f.Agents
		r.gh.open(1, "gitdek", "Add a bounded buffer", agBody("Agent: codex"))
		r.poll()
		r.expect(1, KindProposal, LabelProposal)
		r.f.Agents = nil
		agRatify(t, r, 1)
		stopped := r.expect(1, KindFailed, LabelHumanReview)
		if !strings.Contains(stopped.Comment.Body, "codex") || len(build.built) != 0 || len(r.build.built) != 0 {
			t.Fatalf("the build should fail, naming codex, with no agent building: codex built %v, the watcher's own agent %v:\n%s", build.built, r.build.built, stopped.Comment.Body)
		}
		if runs := git(t, r.origin, "for-each-ref", "--format=%(refname)", "refs/invariant/runs/1/"); strings.Contains(runs, "/build-") {
			t.Fatalf("a build run was recorded:\n%s", runs)
		}
		r.f.Agents = codex
		r.gh.say(1, "gitdek", "/invariant retry")
		r.poll()
		r.poll()
		r.expect(1, KindPR, LabelPR)
		if len(build.built) != 1 || len(r.build.built) != 0 {
			t.Errorf("once the watcher can run codex again, the retry should build with it: codex built %v, the watcher's own agent %v", build.built, r.build.built)
		}
	})
	t.Run("a plan", func(t *testing.T) {
		r, b := plumbingRig(t, "page/page.go")
		_, _, plan := agCodex(t, r)
		r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\nAgent: codex\n\n/invariant solve")
		r.poll()
		r.expect(1, KindProposal, LabelProposal)
		r.f.Agents = nil
		agRatify(t, r, 1)
		stopped := r.expect(1, KindFailed, LabelHumanReview)
		if !strings.Contains(stopped.Comment.Body, "codex") || len(plan.built) != 0 || len(b.built) != 0 {
			t.Errorf("the plan's build should fail, naming codex, with no agent building: codex built %v, the watcher's own agent %v:\n%s", plan.built, b.built, stopped.Comment.Body)
		}
	})
}
