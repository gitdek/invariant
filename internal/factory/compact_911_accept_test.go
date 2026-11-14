package factory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

const compact911DashboardURL = "https://dashboard.example/install"

// Configure only this acceptance rig; ordinary factories have no dashboard
// address. Reflection lets the base exercise its real overflow refusal before
// the new public configuration field exists.
func compact911Rig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	if field := reflect.ValueOf(r.f).Elem().FieldByName("DashboardURL"); field.IsValid() {
		field.SetString(compact911DashboardURL)
	}
	return r
}

func compact911Noise() string {
	var b strings.Builder
	for i := 0; i < 4096; i++ {
		fmt.Fprintf(&b, "%x", sha256.Sum256([]byte(fmt.Sprintf("911:%d", i))))
	}
	return b.String()
}

func compact911Check(t *testing.T, tc toolchain.Toolchain, mode string, authority formalize.CheckContext) *formalize.Result {
	t.Helper()
	ws := t.TempDir()
	reader603Workspace(t, ws)
	include := ""
	switch mode {
	case "compressed":
		include = strings.Repeat("x", maxMarker+1024)
	case "incompressible", "comparison":
		include = compact911Noise()
	case "limited":
		include = compact911Noise()
		reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"500"`, 1))
	case "statement":
		var d map[string]any
		if err := json.Unmarshal([]byte(reader603Draft), &d); err != nil {
			t.Fatal(err)
		}
		d["statements"].([]any)[1].(map[string]any)["says"] = strings.Repeat("Every one of these promised words must remain readable. ", 1600)
		preview639Write(t, ws, d)
	case "label":
		reader603Write(t, filepath.Join(ws, "view.json"), strings.Replace(reader603View, "Working", compact911Noise(), 1))
	case "topology":
		reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"7"`, 1))
		var states []any
		for i := 0; i < 8; i++ {
			states = append(states, map[string]any{"id": fmt.Sprintf("s%d", i), "when": map[string]any{"eq": []any{map[string]any{"var": "n"}, fmt.Sprint(i)}}, "label": fmt.Sprintf("State %d <&> #quot;", i)})
		}
		view := map[string]any{"version": 1, "state": states, "step": []any{
			map[string]any{"id": "poll", "when": map[string]any{"eq": []any{map[string]any{"action": true}, "Poll"}}, "label": "Hidden poll", "hide": true},
			map[string]any{"id": "reset", "when": map[string]any{"eq": []any{map[string]any{"action": true}, "Reset"}}, "label": "Reset <&> #amp;"},
			map[string]any{"id": "advance", "when": true, "label": "Advance [*] --> forged"},
		}}
		reader603Write(t, filepath.Join(ws, "view.json"), string(reader603JSON(t, view)))
	}
	if include != "" {
		reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+include+"\n====", 1))
	}
	if mode == "comparison" {
		reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"3"`, 1))
		reader603Write(t, filepath.Join(ws, "view.json"), strings.Replace(reader603View, "Working", "Revised <working>", 1))
	}
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil || p == nil || report == nil || !report.Passed {
		t.Fatalf("checked %s fixture: %v", mode, err)
	}
	res := &formalize.Result{Proposal: p, Report: report, CheckContext: &authority}
	if err := validateDraftResult(res); err != nil {
		t.Fatalf("invalid complete %s fixture: %v", mode, err)
	}
	return res
}

func compact911Clone(t *testing.T, res *formalize.Result) *formalize.Result {
	t.Helper()
	var out formalize.Result
	if err := json.Unmarshal(reader603JSON(t, res), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func compact911Fresh(t *testing.T, r *rig) {
	t.Helper()
	// Copy the public configuration without copying Factory's runtime mutexes.
	var base string
	field := reflect.ValueOf(r.f).Elem().FieldByName("DashboardURL")
	if field.IsValid() {
		base = field.String()
	}
	saved790Fresh(t, r)
	field = reflect.ValueOf(r.f).Elem().FieldByName("DashboardURL")
	if field.IsValid() {
		field.SetString(base)
	}
}

func compact911Hydrate(t *testing.T, r *rig, issue int, body string) (Marker, *formalize.Result, DraftReference, []byte) {
	t.Helper()
	if len(body) > maxComment {
		t.Fatalf("posted %d bytes, limit %d", len(body), maxComment)
	}
	m, ok := DecodeMarker(body)
	if !ok || m.ProposalRef == nil {
		t.Fatal("valid evidence overflow was not published with a readable saved-draft reference")
	}
	if m.Proposal != nil {
		t.Fatal("compact transport reembedded the complete proposal")
	}
	var ref DraftReference
	if err := json.Unmarshal(m.ProposalRef, &ref); err != nil {
		t.Fatal(err)
	}
	state, commit, err := r.f.Repo.Run(t.Context(), issue, ref.Step)
	if err != nil || state != RunDone || commit != ref.Commit {
		t.Fatalf("reference does not name its completed run: %v", err)
	}
	payload, err := r.f.Repo.Show(t.Context(), ref.Commit, "result.json")
	if err != nil || draftDigest(payload) != ref.SHA256 {
		t.Fatalf("reference does not pin exact saved bytes: %v", err)
	}
	hydrated, res, err := m.ResolveDraft(t.Context(), r.f.Repository, issue, r.f.Repo.Run, r.f.Repo.Show)
	if err != nil || res == nil || hydrated.Proposal == nil {
		t.Fatalf("compact proposal cannot be recovered: %v", err)
	}
	if len(reader603JSON(t, m)) > maxMarker {
		t.Fatal("reference marker exceeds the expanded limit")
	}
	return hydrated, res, ref, payload
}

func compact911Link(t *testing.T, body, base string, ref DraftReference) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(ref.Repository, "/")
	want := strings.TrimRight(u.EscapedPath(), "/") + "/draft/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/" + strconv.Itoa(ref.Issue) + "/" + url.PathEscape(ref.Commit) + "/" + strings.TrimPrefix(ref.SHA256, "sha256:")
	want = u.Scheme + "://" + u.Host + want
	if !strings.Contains(reader603Visible(body), "]("+want+")") {
		t.Fatalf("missing exact saved-draft link %s", want)
	}
	return want
}

func compact911Contains(t *testing.T, body string, values ...string) {
	t.Helper()
	visible := html.UnescapeString(reader603Visible(body))
	for _, want := range values {
		if !strings.Contains(visible, want) {
			t.Errorf("visible decision omitted %q", want)
		}
	}
}

func compact911Topology(t *testing.T, body string, c *statechart.Result) {
	t.Helper()
	// Check decoded topology, not just counts or a production formatter's output.
	decode := func(s string) string {
		return regexp.MustCompile(`#([0-9]+);`).ReplaceAllStringFunc(s, func(e string) string {
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(e, "#"), ";"))
			return string(rune(n))
		})
	}
	type topology struct{ States, Initial, Arrows map[string]int }
	makeTopology := func() topology { return topology{map[string]int{}, map[string]int{}, map[string]int{}} }
	want := makeTopology()
	labels := map[string]string{}
	for _, s := range c.States {
		labels[s.ID] = s.Label
		want.States[s.Label]++
	}
	for _, id := range c.Initial {
		want.Initial[labels[id]]++
	}
	key := func(a, b, c string) string { return a + "\x00" + b + "\x00" + c }
	for _, a := range c.Arrows {
		want.Arrows[key(labels[a.From], a.Label, labels[a.To])]++
	}
	for _, fence := range regexp.MustCompile("(?s)```mermaid\\n(.*?)\\n```").FindAllStringSubmatch(body, -1) {
		if !strings.HasPrefix(fence[1], "stateDiagram-v2\n") {
			continue
		}
		got, aliases := makeTopology(), map[string]string{}
		for _, s := range regexp.MustCompile(`(?m)^\s*state "([^"\n]*)" as ([A-Za-z][A-Za-z0-9_]*)\s*$`).FindAllStringSubmatch(fence[1], -1) {
			aliases[s[2]] = decode(s[1])
			got.States[aliases[s[2]]]++
		}
		for _, a := range regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_]*|\[\*\])\s*-->\s*([A-Za-z][A-Za-z0-9_]*)(?:: ([^\n]*))?\s*$`).FindAllStringSubmatch(fence[1], -1) {
			if a[1] == "[*]" {
				got.Initial[aliases[a[2]]]++
			} else {
				got.Arrows[key(aliases[a[1]], decode(a[3]), aliases[a[2]])]++
			}
		}
		if reflect.DeepEqual(want, got) {
			return
		}
	}
	t.Fatalf("no complete literal-safe chart matched %d states, %d initials and %d labeled arrows", len(c.States), len(c.Initial), len(c.Arrows))
}

func compact911Decision(t *testing.T, body string, p *formalize.Proposal) {
	t.Helper()
	for _, s := range p.Statements {
		compact911Contains(t, body, s.Name, s.Kind, s.Says)
		if s.Expect != "" {
			compact911Contains(t, body, s.Expect)
		}
	}
	for k, v := range p.Bounds {
		compact911Contains(t, body, k+" = "+v)
	}
	if p.Reader.Chart != nil {
		compact911Topology(t, body, p.Reader.Chart)
	}
	if l := p.Reader.Limitation; l != nil {
		compact911Contains(t, body, "too_large", fmt.Sprint(l.Actual), fmt.Sprint(l.Limit), "not evaluated")
	}
	if c := p.Reader.Comparison; c != nil {
		compact911Contains(t, body, c.Baseline.Name)
		for k, v := range c.Baseline.Lock.Bounds {
			compact911Contains(t, body, k+" = "+v)
		}
		for _, q := range c.Qualifications() {
			compact911Contains(t, body, q)
		}
		if c.Unavailable == nil {
			compact911Contains(t, body, "added", "removed", "green", "red", "dashed")
		}
	}
}

func TestCompact911PublicationAndCompatibility(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, mode := range []string{"small", "incompressible", "compressed", "limited", "topology"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "topology" {
				compact911TopologyTools(t)
			}
			res := compact911Check(t, tc, mode, formalize.CheckContext{})
			before := reader603JSON(t, res)
			p := res.Proposal
			if mode != "small" {
				t.Run("without dashboard", func(t *testing.T) {
					r := newRig(t)
					form := &markerCapacityFormalizer{result: res}
					r.f.Formalizer = form
					r.gh.open(911, "gitdek", "No public dashboard configured", "/invariant solve")
					r.poll()
					post := r.expect(911, KindStuck, LabelHumanReview)
					body := reader603Visible(post.Comment.Body)
					if len(post.Comment.Body) > maxComment || strings.Contains(body, "/invariant ratify ") || strings.Contains(body, "/invariant choose ") || strings.Contains(body, "](") || strings.Contains(body, "```mermaid") {
						t.Fatal("an unconfigured factory must refuse overflow without publishing a decision or evidence link")
					}
					compact911Contains(t, strings.ToLower(body), "size")
					state, commit, err := r.f.Repo.Run(t.Context(), 911, "draft-issue")
					if err != nil || state != RunDone {
						t.Fatalf("unconfigured refusal lost the completed record: %v", err)
					}
					var saved formalize.Result
					if err := r.f.loadResult(t.Context(), commit, &saved); err != nil || !draftEqual(saved.Proposal, p) {
						t.Fatalf("unconfigured refusal changed saved evidence: %v", err)
					}
					compact911Fresh(t, r)
					r.poll()
					if form.calls != 1 || len(r.gh.comments[911]) != 1 || !bytes.Equal(before, reader603JSON(t, res)) {
						t.Fatal("unconfigured restart repeated work or altered evidence")
					}
				})
			}
			if mode == "topology" {
				if len(p.Reader.Chart.States) != 8 || len(p.Reader.Chart.Arrows) != 41 || len(p.Reader.Chart.Markdown()) <= maxComment {
					t.Fatal("topology fixture must overflow on occurrence evidence with 8 states and 41 arrows")
				}
			}
			r := compact911Rig(t)
			form := &markerCapacityFormalizer{result: res}
			r.f.Formalizer = form
			r.gh.open(911, "gitdek", "Publish complete evidence", "/invariant solve")
			r.poll()
			post := r.expect(911, KindProposal, LabelProposal)
			if len(r.gh.comments[911]) != 1 {
				t.Fatal("a decision must publish in one comment")
			}
			if mode == "small" {
				m := Marker{Kind: KindProposal, ReplyTo: []int64{0}, Proposal: p, Agent: "claude-code"}
				pinned, err := p.Pinned()
				if err != nil {
					t.Fatal(err)
				}
				want := header + "proposal for ratification\n\nHere's what I propose must always be true for **reader lifecycle**. Once you ratify it, these statements are pinned by hash, and I can't change them. Then I'll write the code in Go, proved with Gobra.\n\n" +
					"| Statement | Kind | Says |\n| :-- | :-- | :-- |\n| `Spec` | spec | Advance, reset or poll. |\n| `Safe` | invariant | The count stays within its limit. |\n" +
					"\n**Already checked** against a draft model, within " + bounds(p.Bounds) + ": " + checked(res.Report) + "\n" +
					"\n**Reader chart**\n\n" + statechart.BoundsMarkdown(p.Bounds) + p.Reader.Chart.Markdown() +
					"\n<details>\n<summary>The statements in TLA+</summary>\n\n```tla\n" + pinned + "```\n\n</details>" +
					"\n\nTo ratify exactly this, comment `/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")[:12] + "`. To change anything, say what in a comment, then comment `/invariant revise`." +
					"\n\n_Drafted by `claude-code`._\n\n" + m.encode() + "\n"
				if post.Comment.Body != want {
					t.Fatal("small draft changed its existing complete text, evidence, byline, marker or commands")
				}
			} else {
				_, saved, ref, payload := compact911Hydrate(t, r, 911, post.Comment.Body)
				if ref.ProposalHash != p.Hash || !draftEqual(saved.Proposal, p) {
					t.Fatal("externalization changed proposal identity or complete saved evidence")
				}
				if mode == "compressed" && len(payload) <= maxMarker {
					t.Fatal("fixture must exceed the expanded-marker limit")
				}
				if mode == "incompressible" && len((Marker{Kind: KindProposal, Proposal: p}).encode()) <= maxComment {
					t.Fatal("fixture must exceed the compressed-comment limit")
				}
				compact911Link(t, post.Comment.Body, compact911DashboardURL, ref)
				compact911Decision(t, post.Comment.Body, p)
				compact911Contains(t, post.Comment.Body, checked(res.Report), "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:")[:12], "/invariant revise", "_Drafted by `claude-code`._")
				lower := strings.ToLower(reader603Visible(post.Comment.Body))
				for _, w := range []string{"complete", "evidence", "hidden", "provenance"} {
					if !strings.Contains(lower, w) {
						t.Errorf("compact publication does not explain linked %s", w)
					}
				}
				if strings.Contains(post.Comment.Body, "Some detail is left out") || strings.Contains(post.Comment.Body, "Model step occurrences |") {
					t.Fatal("compact decision clipped content or retained per-occurrence tables")
				}
			}
			if !bytes.Equal(before, reader603JSON(t, res)) {
				t.Fatal("publication mutated original sources, evidence or hash")
			}
			r.poll()
			if form.calls != 1 || len(r.gh.comments[911]) != 1 {
				t.Fatal("publication was repeated or redrafted")
			}
		})
	}
}

type compact911FailPost struct {
	GitHub
	failures int
}

func (g *compact911FailPost) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if g.failures > 0 {
		g.failures--
		return github.Comment{}, errors.New("temporary publication failure")
	}
	return g.GitHub.PostComment(ctx, n, body)
}

func TestCompact911RecoveryRatificationAndBuild(t *testing.T) {
	tc, _ := reader603Tools(t)
	res := compact911Check(t, tc, "compressed", formalize.CheckContext{})
	r := compact911Rig(t)
	form := &markerCapacityFormalizer{result: res}
	r.f.Formalizer = form
	r.gh.open(911, "gitdek", "Recover evidence", "/invariant solve")
	r.f.GitHub = &compact911FailPost{GitHub: r.gh, failures: 1}
	if err := r.f.Poll(t.Context()); err == nil {
		t.Fatal("publication failure was not reported")
	}
	state, commit, err := r.f.Repo.Run(t.Context(), 911, "draft-issue")
	if err != nil || state != RunDone || form.calls != 1 || len(r.gh.comments[911]) != 0 {
		t.Fatalf("failure did not leave one complete unpublished run: %v", err)
	}
	payload, err := r.f.Repo.Show(t.Context(), commit, "result.json")
	if err != nil {
		t.Fatal(err)
	}
	compact911Fresh(t, r)
	r.poll()
	post := r.expect(911, KindProposal, LabelProposal)
	_, saved, ref, got := compact911Hydrate(t, r, 911, post.Comment.Body)
	if ref.Commit != commit || !bytes.Equal(got, payload) || form.calls != 1 {
		t.Fatal("restart changed the saved record or reran drafting")
	}
	compact911Decision(t, post.Comment.Body, saved.Proposal)
	// A ratification of another identity must not start work.
	r.gh.say(911, "gitdek", "/invariant ratify sha256:"+strings.Repeat("0", 64))
	r.poll()
	if len(r.build.built) != 0 {
		t.Fatal("wrong hash started a build")
	}
	r.build.stop = true
	command := r.gh.say(911, "gitdek", "/invariant ratify "+saved.Proposal.Hash)
	originalRepo := r.f.Repo
	beforeFailure := len(r.gh.posts(911))
	for _, broken := range []*saved790ReadRepo{{Repo: originalRepo, fail: errors.New("saved bytes temporarily unavailable")}, {Repo: originalRepo, bytes: []byte(`{"proposal":"corrupt"}`)}} {
		r.f.Repo = broken
		if err := r.f.Poll(t.Context()); err == nil {
			t.Fatal("unresolved or corrupt compact reference was not diagnostic")
		}
		if len(r.build.built) != 0 || form.calls != 1 || len(r.gh.posts(911)) != beforeFailure {
			t.Fatal("invalid reference fell back, consumed ratification or started work")
		}
	}
	r.f.Repo = originalRepo
	r.poll()
	failed := r.expect(911, KindFailed, LabelHumanReview)
	if len(r.build.built) != 1 || form.calls != 1 {
		t.Fatal("exact ratification did not hand off the saved proposal once")
	}
	for _, p := range r.gh.posts(911) {
		if p.Marker.Kind == KindRatified || p.Marker.Kind == KindFailed {
			_, restored, carried, _ := compact911Hydrate(t, r, 911, p.Comment.Body)
			if carried != ref || !draftEqual(restored.Proposal, saved.Proposal) {
				t.Fatal("later post changed the reference or selected proposal")
			}
		}
	}
	var lock project.Lock
	if err := json.Unmarshal([]byte(git(t, r.origin, "show", failed.Marker.Branch+":"+failed.Marker.Project+"/.invariant/ratified.lock")), &lock); err != nil {
		t.Fatal(err)
	}
	if !draftEqual(lock.Bounds, saved.Proposal.Bounds) || !draftEqual(lock.Statements, saved.Proposal.Statements) || lock.Ratified == nil || lock.Ratified.Proposal != saved.Proposal.Hash || lock.Ratified.Comment != command.URL {
		t.Fatal("ratification changed exact statements, bounds, identity or human authority")
	}
	for file, want := range map[string]string{".invariant/view.json": saved.Proposal.View, ".invariant/specs/Reader603.tla": saved.Proposal.ModuleText, ".invariant/specs/Helper603.tla": saved.Proposal.Includes["Helper603.tla"]} {
		got := git(t, r.origin, "show", failed.Marker.Branch+":"+failed.Marker.Project+"/"+file)
		if strings.TrimSpace(want) != got {
			t.Fatalf("ratification changed %s", file)
		}
	}
	compact911Fresh(t, r)
	r.poll()
	if len(r.build.built) != 1 {
		t.Fatal("restart rebuilt without authorization")
	}
	r.build.stop = false
	r.gh.say(911, "gitdek", "/invariant retry")
	r.poll()
	post = r.expect(911, KindPR, LabelPR)
	_, restored, carried, _ := compact911Hydrate(t, r, 911, post.Comment.Body)
	if carried != ref || !draftEqual(restored.Proposal, saved.Proposal) || len(r.build.built) != 2 || form.calls != 1 {
		t.Fatal("resumed build lost its saved selection")
	}
}

func TestCompact911QuestionsAndComparisons(t *testing.T) {
	for _, finish := range []string{"choose", "revise"} {
		t.Run(finish, func(t *testing.T) {
			tc, _ := reader603Tools(t)
			compact911TopologyTools(t)
			r := compact911Rig(t)
			var requests []formalize.Request
			var results []*formalize.Result
			agent := formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				if len(requests) == 2 {
					d := preview639Draft(t, 2, nil)
					d["forks"].([]any)[0].(map[string]any)["question"] = "Which <first>  behavior & exact wording?"
					preview639Option(d, 1, 1)["says"] = "Keep the second option's exact  wording <&>."
					preview639Input(d, 1, 0)["model"].(map[string]any)["includes"] = map[string]string{"Helper603.tla": strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", maxMarker+1024)+"\n====", 1)}
					preview639Write(t, j.Workspace, d)
				} else if len(requests) > 2 {
					reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"3"`, 1))
					reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.Replace(reader603View, "Working", "Chosen working", 1))
				}
				return synth.Usage{}, nil
			}}}
			r.f.Formalizer = reader603Formalizer{run: func(ctx context.Context, q formalize.Request, out string) (*formalize.Result, error) {
				requests = append(requests, q)
				res, err := agent.Formalize(ctx, q, out)
				results = append(results, res)
				return res, err
			}}
			r.gh.open(911, "gitdek", "Choose complete alternatives", "/invariant solve")
			r.poll()
			first := r.expect(911, KindProposal, LabelProposal)
			_, old, _, _ := compact911Hydrate(t, r, 911, first.Comment.Body)
			r.gh.say(911, "gitdek", "Compare two independent choices.\n/invariant revise")
			r.poll()
			question := r.expect(911, KindForks, LabelAsking)
			m, saved, ref, payload := compact911Hydrate(t, r, 911, question.Comment.Body)
			if len(saved.Proposal.Forks) != 2 || len(m.Forks) != 2 || saved.Proposal.Hash != "" || saved.Proposal.Ratifiable() {
				t.Fatal("question is incomplete or became ratifiable")
			}
			if !draftEqual(saved.CheckContext.Baseline, (formalize.Request{Previous: old.Proposal}).CheckContext().Baseline) {
				t.Fatal("question did not capture the selected baseline")
			}
			assertForks := func(body string, forks []formalize.Fork) {
				t.Helper()
				compact911Contains(t, body, "unratified", "model-only", "/invariant choose "+forks[0].ID+" "+forks[0].Options[0].ID, "/invariant revise")
				for _, f := range forks {
					compact911Contains(t, body, f.Question)
					for _, o := range f.Options {
						compact911Contains(t, body, o.Says, checked(o.Preview.Receipt))
						compact911Decision(t, body, o.Preview.Model)
					}
				}
				if strings.Contains(reader603Visible(body), "/invariant ratify") {
					t.Fatal("choosing an alternative must ratify nothing")
				}
				compact911Link(t, body, compact911DashboardURL, ref)
			}
			assertForks(question.Comment.Body, m.Forks)
			choice := r.gh.say(911, "gitdek", "/invariant choose F1 B")
			r.poll()
			partial := r.expect(911, KindForks, LabelAsking)
			m, again, partialRef, partialBytes := compact911Hydrate(t, r, 911, partial.Comment.Body)
			if partialRef != ref || !bytes.Equal(partialBytes, payload) || !draftEqual(again.Proposal, saved.Proposal) || len(m.Forks) != 1 || m.Forks[0].ID != "F2" {
				t.Fatal("partial choice edited original evidence or the remaining options")
			}
			if len(m.Answers) != 1 || m.Answers[0].Comment != choice.URL || m.Answers[0].Option != "B" {
				t.Fatal("partial choice lost human authority")
			}
			assertForks(partial.Comment.Body, m.Forks)
			compact911Contains(t, partial.Comment.Body, saved.Proposal.Forks[0].Question, saved.Proposal.Forks[0].Options[1].Says, "gitdek")
			if len(requests) != 2 || len(r.build.built) != 0 {
				t.Fatal("partial choice drafted or built")
			}
			compact911Fresh(t, r)
			before := len(r.gh.posts(911))
			r.poll()
			if len(requests) != 2 || len(r.gh.posts(911)) != before {
				t.Fatal("restart repeated publication or drafting")
			}
			command := "/invariant choose F2 A"
			if finish == "revise" {
				command = "/invariant revise"
			}
			r.gh.say(911, "gitdek", command)
			r.poll()
			selected := r.expect(911, KindProposal, LabelProposal)
			_, final, _, _ := compact911Hydrate(t, r, 911, selected.Comment.Body)
			compact911Decision(t, selected.Comment.Body, final.Proposal)
			if len(requests) != 3 || len(r.build.built) != 0 {
				t.Fatal("final choice or revision must draft once without ratification or build")
			}
			q := requests[2]
			wantPrevious, wantAnswers := saved.Proposal, 1
			if finish == "choose" {
				wantPrevious, wantAnswers = saved.Proposal.Carrier(), 2
			}
			if !draftEqual(q.Previous, wantPrevious) || len(q.Answers) != wantAnswers || !draftEqual(q.CheckContext().Baseline, saved.CheckContext.Baseline) {
				t.Fatal("next draft lost original previews, baseline or actual choices")
			}
			if !draftEqual(results[1].Proposal, saved.Proposal) {
				t.Fatal("publication mutated the original multi-fork proposal")
			}
		})
	}
}

func TestCompact911AmendmentWarningsAndBaseline(t *testing.T) {
	t.Run("all statement kinds", compact911AmendKinds)
	tc, _ := reader603Tools(t)
	old := compact911Check(t, tc, "small", formalize.CheckContext{}).Proposal
	r := compact911Rig(t)
	dir, work := "examples/reader-lifecycle", t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 571, By: "gitdek", Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed ratified baseline")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	var requests []formalize.Request
	agent := formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		reader603Write(t, filepath.Join(j.Workspace, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+compact911Noise()+"\n====", 1))
		reader603Write(t, filepath.Join(j.Workspace, "proposal.json"), strings.Replace(strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"3"`, 1), "The count stays within its limit.", "The amended count remains within its limit.", 1))
		reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.Replace(reader603View, "Working", "Amended working", 1))
		return synth.Usage{}, nil
	}}}
	r.f.Formalizer = reader603Formalizer{run: func(ctx context.Context, q formalize.Request, out string) (*formalize.Result, error) {
		requests = append(requests, q)
		return agent.Formalize(ctx, q, out)
	}}
	r.gh.open(911, "gitdek", "Amend a checked model", "Project: "+dir+"\n/invariant solve")
	r.poll()
	post := r.expect(911, KindProposal, LabelProposal)
	_, saved, _, _ := compact911Hydrate(t, r, 911, post.Comment.Body)
	compact911Decision(t, post.Comment.Body, saved.Proposal)
	compact911Contains(t, post.Comment.Body, "This changes the invariant `Safe`", old.Statements[1].Says, old.Statements[0].Says, "from `2` to `3`", checked(saved.Report), "/invariant ratify "+strings.TrimPrefix(saved.Proposal.Hash, "sha256:")[:12])
	baseline := reader603JSON(t, saved.Proposal.Target.Baseline)
	// Move the branch's view without ratifying another lock. Recovery/revision
	// must still compare against the original captured ratification.
	reader603Write(t, filepath.Join(work, dir, ".invariant/view.json"), strings.Replace(reader603View, "Working", "Unrelated branch movement", 1))
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "move view after capture")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	compact911Fresh(t, r)
	r.gh.say(911, "gitdek", "/invariant revise")
	r.poll()
	post = r.expect(911, KindProposal, LabelProposal)
	_, again, _, _ := compact911Hydrate(t, r, 911, post.Comment.Body)
	if !bytes.Equal(baseline, reader603JSON(t, again.Proposal.Target.Baseline)) || len(requests) != 2 {
		t.Fatal("revision replaced the captured ratification")
	}
	compact911Decision(t, post.Comment.Body, again.Proposal)
}

// The buffer formalizer rig supplies typed model outcomes; this adds the full
// receipt binding required by saved references and keeps its real chart
// computation and pinning. No rendering code supplies or truncates evidence.
func compact911Buffer(t *testing.T, req formalize.Request, amended bool) *formalize.Result {
	t.Helper()
	p := bufferProposal(t)
	p.ModuleText = strings.Replace(p.ModuleText, "====", `Live == []<>(Len(buf) = 0)
FairTake == WF_vars(Take)
RemovedSafe == Len(buf) <= Cap
RemovedLive == []<>(Len(buf) <= Cap)
RemovedFair == WF_vars(Take)
====`, 1)
	p.Statements = append(p.Statements,
		project.Statement{Name: "Live", Kind: project.Property, Says: "Every buffer eventually empties."},
		project.Statement{Name: "FairTake", Kind: project.Fairness, Says: "A continuously enabled take eventually runs."},
		project.Statement{Name: "RemovedSafe", Kind: project.Invariant, Says: "The removed invariant bounds the buffer."},
		project.Statement{Name: "RemovedLive", Kind: project.Property, Says: "The removed property eventually bounds the buffer."},
		project.Statement{Name: "RemovedFair", Kind: project.Fairness, Says: "The removed fairness assumes takes run."})
	if amended {
		var keep []project.Statement
		for _, s := range p.Statements {
			if strings.HasPrefix(s.Name, "Removed") || s.Name == "CanFill" {
				continue
			}
			if s.Name == "WithinCap" {
				s.Says = "The buffer's amended bound still applies."
			}
			if s.Name == "Live" {
				s.Says = "The amended property eventually empties the buffer."
			}
			if s.Name == "FairTake" {
				s.Says = "The amended fairness assumes enabled takes run."
			}
			keep = append(keep, s)
		}
		p.Statements = keep
		p.Includes = map[string]string{"Helper.tla": "---- MODULE Helper ----\n\\* " + compact911Noise() + "\n====\n"}
	}
	if err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	checkedBufferReader(t, p)
	for _, s := range p.Statements {
		if s.Kind == project.Invariant {
			p.Reader.Chart.Report.Source.Config.Invariants = append(p.Reader.Chart.Report.Source.Config.Invariants, s.Name)
		}
	}
	sealComparisonReader(t, p)
	checkedBufferComparison(t, p, req)
	if req.Current != nil {
		if err := p.Amend(req.Current); err != nil {
			t.Fatal(err)
		}
	}
	rr := &verify.Reader{Version: 1, Chart: p.Reader.Chart, Inputs: verify.ReaderInputs{Module: p.Module, Model: draftDigest([]byte(p.ModuleText)), View: draftDigest([]byte(p.View)), Includes: map[string]string{}}}
	for name, source := range p.Includes {
		rr.Inputs.Includes[name] = draftDigest([]byte(source))
	}
	rr.Fingerprint = draftDigest(reader603JSON(t, struct {
		Reader verify.Reader `json:"reader"`
		Lock   project.Lock  `json:"lock"`
	}{*rr, project.Lock{Bounds: p.Bounds, Statements: p.Statements}}))
	r := &verify.Report{Project: p.Name, ModelOnly: true, Passed: true, Proposal: p.Hash, Bounds: p.Bounds, Reader: rr,
		Toolchain: verify.Toolchain{TLC: "TLC fixture", JavaImage: toolchain.JavaImage, TLCJarSHA256: toolchain.TLCJarSHA256},
		Design:    verify.Design{Spec: "Spec", Passed: true, Outcome: "passed", DistinctStates: 7, StatesGenerated: 13, Invariants: p.Reader.Chart.Report.Source.Config.Invariants}}
	for _, s := range p.Statements {
		r.Pins = append(r.Pins, verify.Pin{Name: s.Name, Kind: s.Kind, Says: s.Says, Want: s.SHA256, Got: s.SHA256, Match: true})
		switch s.Kind {
		case project.Witness:
			r.Witnesses = append(r.Witnesses, verify.Witness{Name: s.Name, Says: s.Says, Reached: true})
		case project.Bug:
			r.Bugs = append(r.Bugs, verify.Bug{Name: s.Name, Says: s.Says, Expect: s.Expect, Caught: true, Violated: s.Expect, Outcome: "violated"})
		case project.Property:
			r.Properties = append(r.Properties, verify.Property{Name: s.Name, Says: s.Says, Holds: true, Outcome: "passed"})
		case project.Fairness:
			r.Fairness = append(r.Fairness, verify.Fair{Name: s.Name, Says: s.Says, Action: "Take", InNext: true})
		}
	}
	r.Fingerprint = verify.Fingerprint(*r)
	authority := req.CheckContext()
	res := &formalize.Result{Proposal: p, Report: r, CheckContext: &authority}
	if req.Current != nil {
		res.Changes = formalize.Diff(req.Current, p)
	}
	if err := validateDraftResult(res); err != nil {
		t.Fatalf("buffer fixture is not a complete checked result: %v", err)
	}
	return res
}

func compact911AmendKinds(t *testing.T) {
	old := compact911Buffer(t, formalize.Request{}, false).Proposal
	r := compact911Rig(t)
	dir, work := "examples/buffer", t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 571, By: "gitdek", Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed all statement kinds")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
		return compact911Buffer(t, q, true), nil
	}}
	r.gh.open(911, "gitdek", "Preserve amendment warnings", "Project: "+dir+"\n/invariant solve")
	r.poll()
	post := r.expect(911, KindProposal, LabelProposal)
	_, saved, ref, _ := compact911Hydrate(t, r, 911, post.Comment.Body)
	compact911Decision(t, post.Comment.Body, saved.Proposal)
	compact911Link(t, post.Comment.Body, compact911DashboardURL, ref)
	compact911Contains(t, post.Comment.Body, "This removes", "This changes the invariant", "This changes the property", "Fairness is an assumption", checked(saved.Report), "PutWhenFull", "WithinCap")
	for _, s := range old.Statements {
		compact911Contains(t, post.Comment.Body, s.Says)
	}
	if !strings.Contains(post.Comment.Body, "/invariant ratify "+strings.TrimPrefix(saved.Proposal.Hash, "sha256:")[:12]) {
		t.Fatal("amendment lost its exact ratification command")
	}
}

type compact911FailSave struct {
	Repo
	stage string
}

func (r compact911FailSave) Save(ctx context.Context, dir, message, parent string) (string, error) {
	if r.stage == "save" {
		return "", errors.New("saved result unavailable")
	}
	return r.Repo.Save(ctx, dir, message, parent)
}
func (r compact911FailSave) Finish(ctx context.Context, n int, step, commit string) error {
	if r.stage == "finish" {
		return errors.New("cannot finish saved result")
	}
	return r.Repo.Finish(ctx, n, step, commit)
}

func TestCompact911RefusalsAndSaveOrdering(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, mode := range []string{"incompressible", "statement", "label", "stale", "missing", "receipt", "save", "finish"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "stale" || mode == "missing" || mode == "receipt" || mode == "save" || mode == "finish" {
				fixtureMode = "incompressible"
			}
			res := compact911Check(t, tc, fixtureMode, formalize.CheckContext{})
			switch mode {
			case "stale":
				res.Proposal.Reader.Chart.States[0].Label = "substituted label"
			case "missing":
				res.Proposal.Reader = nil
			case "receipt":
				res.Report.Passed = false
			}
			r := compact911Rig(t)
			form := &markerCapacityFormalizer{result: res}
			r.f.Formalizer = form
			r.gh.open(911, "gitdek", "Preserve publication boundaries", "/invariant solve")
			if mode == "save" || mode == "finish" {
				r.f.Repo = compact911FailSave{Repo: r.f.Repo, stage: mode}
				if err := r.f.Poll(t.Context()); err == nil {
					t.Fatal("save failure was ignored")
				}
				if len(r.gh.comments[911]) != 0 {
					t.Fatal("a failed save or completion exposed a partially usable proposal")
				}
				return
			}
			r.poll()
			if mode == "incompressible" {
				p := r.expect(911, KindProposal, LabelProposal)
				compact911Hydrate(t, r, 911, p.Comment.Body)
				return
			}
			post := r.expect(911, KindStuck, LabelHumanReview)
			body := reader603Visible(post.Comment.Body)
			if len(post.Comment.Body) > maxComment || strings.Contains(body, "/invariant ratify ") || strings.Contains(body, "/invariant choose ") || strings.Contains(body, "```mermaid") || strings.Contains(body, "Some detail is left out") {
				t.Fatal("refusal exposed a clipped or actionable decision")
			}
			if mode == "statement" || mode == "label" {
				compact911Contains(t, strings.ToLower(body), "size")
			}
			state, commit, err := r.f.Repo.Run(t.Context(), 911, "draft-issue")
			if err != nil || state != RunDone {
				t.Fatalf("refusal lost the completed record: %v", err)
			}
			var saved formalize.Result
			if err := r.f.loadResult(t.Context(), commit, &saved); err != nil {
				t.Fatal(err)
			}
			if !draftEqual(saved.Proposal, res.Proposal) {
				t.Fatal("refusal erased evidence from the saved record")
			}
			r.poll()
			if form.calls != 1 || len(r.gh.comments[911]) != 1 {
				t.Fatal("refusal caused automatic redrafting")
			}
		})
	}
	for _, damage := range []string{"missing-preview", "swapped-previews", "stale-preview", "missing-comparison", "stale-comparison"} {
		t.Run(damage, func(t *testing.T) {
			r := compact911Rig(t)
			calls := 0
			r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
				calls++
				if calls == 1 {
					return compact911Check(t, tc, "small", q.CheckContext()), nil
				}
				if strings.Contains(damage, "comparison") {
					res := compact911Check(t, tc, "comparison", q.CheckContext())
					if damage == "missing-comparison" {
						res.Proposal.Reader.Comparison = nil
					} else {
						res.Proposal.Reader.Comparison.Baseline.View = "substituted view"
					}
					return res, nil
				}
				ws := t.TempDir()
				d := preview639Draft(t, 2, nil)
				preview639Input(d, 0, 0)["model"].(map[string]any)["includes"] = map[string]string{"Helper603.tla": strings.Replace(reader603Include, "====", "\\* "+compact911Noise()+"\n====", 1)}
				preview639Write(t, ws, d)
				p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, q.CheckContext())
				if err != nil {
					t.Fatal(err)
				}
				if err := p.ValidatePreviewContext(q.CheckContext()); err != nil {
					t.Fatal(err)
				}
				switch damage {
				case "missing-preview":
					p.Forks[0].Options[0].Preview = nil
				case "swapped-previews":
					p.Forks[0].Options[0].Preview, p.Forks[0].Options[1].Preview = p.Forks[0].Options[1].Preview, p.Forks[0].Options[0].Preview
				case "stale-preview":
					p.Forks[0].Options[0].Says = "A substituted alternative."
				}
				return &formalize.Result{Proposal: p}, nil
			}}
			r.gh.open(911, "gitdek", "Refuse invalid overflow", "/invariant solve")
			r.poll()
			r.expect(911, KindProposal, LabelProposal)
			r.gh.say(911, "gitdek", "/invariant revise")
			r.poll()
			post := r.expect(911, KindStuck, LabelHumanReview)
			body := reader603Visible(post.Comment.Body)
			if len(post.Comment.Body) > maxComment || strings.Contains(body, "/invariant ratify ") || strings.Contains(body, "/invariant choose ") || strings.Contains(body, "```mermaid") {
				t.Fatal("invalid evidence escaped as a usable compact decision")
			}
			r.poll()
			if calls != 2 {
				t.Fatal("invalid evidence caused automatic redrafting")
			}
		})
	}
}

// This extends #603's strict Docker double with a small chart whose hidden
// occurrence evidence is large. The TLC flags, staged-source checks, replay,
// message framing and final generated-state accounting remain enforced.
func init() {
	if os.Getenv("INVARIANT_COMPACT911_DOCKER") == "1" {
		os.Exit(compact911Docker(os.Args[1:]))
	}
}
func compact911TopologyTools(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nunset INVARIANT_READER603_DOCKER\nexport INVARIANT_COMPACT911_DOCKER=1\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func compact911Docker(args []string) int {
	fail := func(s string) int { fmt.Fprintln(os.Stderr, "compact911 Docker double: "+s); return 91 }
	if len(args) == 0 {
		return fail("no command")
	}
	switch args[0] {
	case "info":
		fmt.Println(`{"NCPU":2,"MemTotal":4294967296}`)
		return 0
	case "image", "inspect", "pull", "rm":
		return 0
	case "run":
	default:
		return fail("unexpected command " + args[0])
	}
	var work, cfg, dump, name string
	for i, a := range args {
		if i+1 >= len(args) {
			continue
		}
		switch a {
		case "-v", "--volume":
			if strings.HasSuffix(args[i+1], ":/work") {
				work = strings.TrimSuffix(args[i+1], ":/work")
			}
		case "-config":
			cfg = args[i+1]
		case "-dump":
			if i+2 < len(args) && args[i+1] == "dot,actionlabels" {
				dump = args[i+2]
			}
		case "--name":
			name = args[i+1]
		}
	}
	joined := " " + strings.Join(args, " ") + " "
	if work == "" || cfg == "" || !strings.Contains(joined, " tlc2.TLC ") || !strings.Contains(joined, " --network none ") || !strings.Contains(joined, " -workers 1 ") || strings.Contains(joined, " -deadlock ") {
		return fail("lost TLC invocation contract: " + joined)
	}
	local := func(p string) string { return filepath.Join(work, strings.TrimPrefix(p, "/work/")) }
	config, err := os.ReadFile(local(cfg))
	if err != nil {
		return fail(err.Error())
	}
	module := strings.TrimSuffix(args[len(args)-1], ".tla")
	src, err := os.ReadFile(filepath.Join(work, module+".tla"))
	if err != nil {
		return fail(err.Error())
	}
	main, err := os.ReadFile(filepath.Join(work, "Reader603.tla"))
	if err != nil {
		return fail("main model absent: " + err.Error())
	}
	helper, err := os.ReadFile(filepath.Join(work, "Helper603.tla"))
	if err != nil {
		return fail("include absent: " + err.Error())
	}
	match := regexp.MustCompile(`(?m)^CONSTANT Limit = ([0-9]+)$`).FindStringSubmatch(string(config))
	if len(match) != 2 {
		return fail("bounds absent: " + string(config))
	}
	limit, _ := strconv.Atoi(match[1])
	n := limit + 1
	if n < 1 || n > 502 {
		return fail("unexpected fixture size")
	}
	record, _ := json.Marshal(map[string]string{"module": module, "config": string(config), "main": string(main), "include": string(helper), "dump": dump, "args": joined})
	if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_READER603_LOG"), name+".json"), record, 0600); err != nil {
		return fail(err.Error())
	}
	initial, steps := 1, 5*n+1+2048*n
	if dump != "" {
		var b strings.Builder
		b.WriteString("strict digraph DiskGraph {\nsubgraph cluster_graph {\n")
		replay := module != "Reader603"
		var idVar, rulesVar string
		if replay {
			m := regexp.MustCompile(`(?m)^VARIABLES ([A-Za-z0-9_]+), ([A-Za-z0-9_]+)$`).FindStringSubmatch(string(src))
			if len(m) != 3 {
				return fail("missing rule replay variables")
			}
			idVar, rulesVar = m[1], m[2]
			initial, steps = n, n
			if !strings.Contains(string(src), "Safe |-> Safe") || !strings.Contains(string(src), "UNCHANGED") {
				return fail("invariant replay does not evaluate Safe")
			}
		}
		quote := func(s string) string {
			return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
		}
		for i := 0; i < n; i++ {
			label := fmt.Sprintf("n = %d", i)
			style := ""
			if i == 0 || replay {
				style = ",style=filled"
			}
			if replay {
				label = fmt.Sprintf("/\\ n = %d\n/\\ %s = \"%d\"\n/\\ %s = [Safe |-> TRUE]", i, idVar, i+1, rulesVar)
			}
			fmt.Fprintf(&b, "%d [label=%s%s];\n", i+1, quote(label), style)
		}
		for i := 0; i < n; i++ {
			if replay {
				fmt.Fprintf(&b, "%d -> %d [label=\"Stay\"];\n", i+1, i+1)
				continue
			}
			for jump := 1; jump <= 5; jump++ {
				fmt.Fprintf(&b, "%d -> %d [label=\"Advance\"];\n", i+1, (i+jump)%n+1)
			}
			if i == n-1 {
				fmt.Fprintf(&b, "%d -> 1 [label=\"Reset\"];\n", i+1)
			}
			for occurrence := 0; occurrence < 2048; occurrence++ {
				fmt.Fprintf(&b, "%d -> %d [label=\"Poll\"];\n", i+1, i+1)
			}
		}
		b.WriteString("}\n}\n")
		if err := os.WriteFile(local(dump)+".dot", []byte(b.String()), 0600); err != nil {
			return fail(err.Error())
		}
	}
	frame := func(code int, body string) {
		fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
	}
	frame(2262, "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)")
	frame(2190, fmt.Sprintf("Finished computing initial states: %d distinct states generated at 2026-11-05 00:00:00.", initial))
	frame(2193, "Model checking completed. No error has been found.")
	frame(2199, fmt.Sprintf("%d states generated, %d distinct states found, 0 states left on queue.", initial+steps, n))
	frame(2194, fmt.Sprintf("The depth of the complete state graph search is %d.", n))
	frame(2186, "Finished in 10ms at (2026-11-05 00:00:00)")
	return 0
}
