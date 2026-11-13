package factory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

// The optional wire representation is proposal_ref:{version:1,repository,
// issue,step,commit,sha256,proposal_hash}. sha256 includes the "sha256:" prefix
// and hashes the exact result.json bytes, not a reserialization or the proposal
// hash. commit is a full immutable Git commit ID; step is draft-issue or
// draft-<positive comment ID>. The only readable path is result.json.
// proposal_hash is the selected proposal's existing hash, or empty for forks.
// A referenced marker omits proposal and carries only {id} entries in forks;
// these identify the questions still displayed. Answers and ReplyTo remain
// the current marker's observations. Resolution restores each remaining fork
// from the complete original question, without modifying preview choices.
// Matching dual inline/reference input may be read; disagreement is an error.
// Encoding a resolved marker always keeps its reference and omits its inline
// proposal and fork snapshots, including after carried(), ratification or a
// failed build. Ordinary new draft publication does not opt into this form.
type saved790Reference struct {
	Version      int    `json:"version"`
	Repository   string `json:"repository"`
	Issue        int    `json:"issue"`
	Step         string `json:"step"`
	Commit       string `json:"commit"`
	SHA256       string `json:"sha256"`
	ProposalHash string `json:"proposal_hash"`
}

// This shared production API is usable by a dashboard reader without a
// Factory or a writable clone. The caller first authenticates the enclosing
// comment under its factory-author rule. The callbacks read only that
// repository. ResolveDraft checks repository/issue/step/commit/digest, validates
// complete result evidence against its saved CheckContext, and returns a
// detached hydrated marker AND the complete result. On failure neither return
// value supplies a usable proposal. Inline markers need no callback reads.
// New records preserve publication metadata before Finish; legacy records
// retain their existing inline recovery path. No agent/tool is run to resolve.
type saved790Resolver interface {
	ResolveDraft(context.Context, string, int,
		func(context.Context, int, string) (RunState, string, error),
		func(context.Context, string, string) ([]byte, error)) (Marker, *formalize.Result, error)
}

func saved790Digest(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

func saved790Result(t *testing.T, large bool) *formalize.Result {
	t.Helper()
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	if large {
		reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", maxMarker+1024)+"\n====", 1))
	}
	p, report := reader603Checked(t, ws, tc)
	return &formalize.Result{Proposal: p, Report: report, CheckContext: &formalize.CheckContext{}, Usage: synth.Usage{Backend: "fixture", CostUSD: 0.125}}
}

func saved790SaveBytes(t *testing.T, r *rig, issue int, step string, payload []byte, hash string) saved790Reference {
	t.Helper()
	dir := t.TempDir()
	reader603Write(t, filepath.Join(dir, "result.json"), string(payload))
	commit, err := r.f.Repo.Save(t.Context(), dir, "complete checked draft reference fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.f.Repo.Record(t.Context(), issue, step, "draft reference fixture"); err != nil {
		t.Fatal(err)
	}
	if err := r.f.Repo.Finish(t.Context(), issue, step, commit); err != nil {
		t.Fatal(err)
	}
	return saved790Reference{1, r.f.Repository, issue, step, commit, saved790Digest(payload), hash}
}

func saved790Save(t *testing.T, r *rig, result *formalize.Result, step string) saved790Reference {
	t.Helper()
	return saved790SaveBytes(t, r, 790, step, reader603JSON(t, result), result.Proposal.Hash)
}

func saved790Wire(t *testing.T, value any) string {
	t.Helper()
	return "◉ **Invariant** · saved draft\n\n<!-- invariant:" + base64.StdEncoding.EncodeToString(reader603JSON(t, value)) + " -->"
}

func saved790Envelope(ref saved790Reference, p *formalize.Proposal) map[string]any {
	kind := KindProposal
	m := map[string]any{"kind": kind, "proposal_ref": ref, "reply_to": []int64{0}}
	if len(p.Forks) != 0 {
		m["kind"] = KindForks
		fs := []map[string]string{}
		for _, f := range p.Forks {
			fs = append(fs, map[string]string{"id": f.ID})
		}
		m["forks"] = fs
	}
	return m
}

func saved790Open(t *testing.T, r *rig) {
	t.Helper()
	r.gh.me = "invariant-code-factory[bot]"
	r.f.Self = r.gh.me
	r.f.writers, r.f.asked = map[string]bool{}, map[string]bool{}
	r.gh.open(790, "gitdek", "Recover a saved draft", "A lifecycle.\n/invariant solve")
}

func saved790Post(t *testing.T, r *rig, ref saved790Reference, p *formalize.Proposal) github.Comment {
	t.Helper()
	return r.gh.say(790, r.gh.me, saved790Wire(t, saved790Envelope(ref, p)))
}

func saved790Fresh(t *testing.T, r *rig) {
	t.Helper()
	old := r.f
	clone := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: r.origin, Name: "Joseph", Email: "7275925+gitdek@users.noreply.github.com"}
	if err := clone.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(old.Work); err != nil {
		t.Fatal(err)
	}
	r.f = &Factory{Repository: old.Repository, Self: old.Self, GitHub: r.gh, Repo: clone,
		Formalizer: old.Formalizer, Builder: old.Builder, Agents: old.Agents, Agent: old.Agent,
		Base: old.Base, Projects: old.Projects, Work: t.TempDir(), Check: old.Check, Log: t.Logf,
		writers: map[string]bool{}, asked: map[string]bool{}}
}

func saved790Read(t *testing.T, r *rig, want *formalize.Proposal) Post {
	t.Helper()
	thread, err := r.f.read(t.Context(), *r.gh.issues[790])
	if err != nil {
		t.Fatalf("resolve authenticated draft: %v", err)
	}
	p, ok := thread.State()
	if !ok || p.Marker.Proposal == nil {
		t.Fatal("authenticated saved draft reference did not recover a usable proposal")
	}
	if !bytes.Equal(reader603JSON(t, p.Marker.Proposal), reader603JSON(t, want)) {
		t.Fatal("reference recovery changed the complete proposal")
	}
	return p
}

func saved790Compact(t *testing.T, body string, ref saved790Reference) Marker {
	t.Helper()
	if len(body) > maxComment {
		t.Fatalf("carried post exceeds comment limit: %d", len(body))
	}
	m, ok := DecodeMarker(body)
	if !ok {
		t.Fatal("carried reference marker is not decodable")
	}
	raw := reader603Object(t, m)
	if _, ok := raw["proposal"]; ok {
		t.Fatal("carried reference reembedded the proposal")
	}
	if !reflect.DeepEqual(raw["proposal_ref"], reader603Object(t, ref)) {
		t.Fatal("carried marker dropped or changed its saved-record reference")
	}
	for _, f := range m.Forks {
		if f.ID == "" || f.Question != "" || len(f.Options) != 0 {
			t.Fatal("carried fork must contain only its ID, not checked snapshots")
		}
	}
	return m
}

func TestSavedDraft790SharedResolution(t *testing.T) {
	result := saved790Result(t, true)
	r := newRig(t)
	saved790Open(t, r)
	// Retain insignificant whitespace too: a digest of reencoded JSON is wrong.
	payload := append(reader603JSON(t, result), []byte("\n \t")...)
	if len(payload) <= maxMarker {
		t.Fatal("fixture is not over 4 MiB")
	}
	ref := saved790SaveBytes(t, r, 790, "draft-issue", payload, result.Proposal.Hash)
	posted := saved790Post(t, r, ref, result.Proposal)
	saved790Fresh(t, r)
	m, ok := DecodeMarker(posted.Body)
	if !ok {
		t.Fatal("reference marker cannot be decoded")
	}
	resolver, ok := any(m).(saved790Resolver)
	if !ok {
		t.Fatal("Marker needs the shared ResolveDraft contract for saved-record readers")
	}
	runs, reads := 0, 0
	run := func(ctx context.Context, n int, step string) (RunState, string, error) {
		runs++
		if n != 790 || step != "draft-issue" {
			t.Fatal("reference escaped its run")
		}
		return r.f.Repo.Run(ctx, n, step)
	}
	show := func(ctx context.Context, commit, path string) ([]byte, error) {
		reads++
		if commit != ref.Commit || path != "result.json" {
			t.Fatal("reference read a moving ref or a caller-selected path")
		}
		return r.f.Repo.Show(ctx, commit, path)
	}
	hydrated, got, err := resolver.ResolveDraft(t.Context(), "o/r", 790, run, show)
	if err != nil || got == nil || hydrated.Proposal == nil {
		t.Fatalf("shared resolution failed: %v", err)
	}
	if runs == 0 || reads == 0 || !bytes.Equal(reader603JSON(t, got), reader603JSON(t, result)) {
		t.Fatal("resolution did not return the exact complete saved result and evidence")
	}
	if !bytes.Equal(reader603JSON(t, hydrated.Proposal), reader603JSON(t, result.Proposal)) {
		t.Fatal("hydrated marker changed proposal identity or source bytes")
	}
	saved790Compact(t, hydrated.encode(), ref)
	got.Proposal.Includes["Helper603.tla"] = "a caller's mutation"
	again, _, err := resolver.ResolveDraft(t.Context(), "o/r", 790, run, show)
	if err != nil || again.Proposal.Includes["Helper603.tla"] != result.Proposal.Includes["Helper603.tla"] {
		t.Fatal("a previous reader's mutations became authority")
	}
	if _, ok := DecodeMarker((Marker{Kind: KindProposal, Proposal: result.Proposal}).encode()); ok {
		t.Fatal("reference support removed the hostile-marker decompression bound")
	}
	legacy := Marker{Kind: KindProposal, Proposal: result.Proposal}
	lr, ok := any(legacy).(saved790Resolver)
	if !ok {
		t.Fatal("missing resolver")
	}
	_, _, err = lr.ResolveDraft(t.Context(), "o/r", 790,
		func(context.Context, int, string) (RunState, string, error) {
			t.Fatal("inline marker read a run")
			return RunNone, "", nil
		},
		func(context.Context, string, string) ([]byte, error) {
			t.Fatal("inline marker read a blob")
			return nil, nil
		})
	if err != nil {
		t.Fatalf("legacy inline marker no longer resolves: %v", err)
	}
}

func TestSavedDraft790RatificationAndCarriedPosts(t *testing.T) {
	result := saved790Result(t, true)
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopped=%v", stop), func(t *testing.T) {
			r := newRig(t)
			saved790Open(t, r)
			ref := saved790Save(t, r, result, "draft-issue")
			saved790Post(t, r, ref, result.Proposal)
			saved790Fresh(t, r)
			saved790Read(t, r, result.Proposal)
			r.build.stop = stop
			ratify := r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
			r.poll()
			if len(r.form.requests) != 0 || len(r.build.built) != 1 {
				t.Fatal("reference ratification reran drafting or failed to build exactly once")
			}
			last := r.gh.last(790)
			want := KindPR
			if stop {
				want = KindFailed
			}
			if last.Marker.Kind != want {
				t.Fatalf("last state = %s, want %s", last.Marker.Kind, want)
			}
			seen := map[string]bool{}
			for _, p := range r.gh.posts(790) {
				seen[p.Marker.Kind] = true
				saved790Compact(t, p.Comment.Body, ref)
			}
			if !seen[KindRatified] {
				t.Fatal("ratification was not recorded")
			}
			m := last.Marker
			var lock project.Lock
			if err := json.Unmarshal([]byte(git(t, r.origin, "show", m.Branch+":"+m.Project+"/.invariant/ratified.lock")), &lock); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(lock.Bounds, result.Proposal.Bounds) || !reflect.DeepEqual(lock.Statements, result.Proposal.Statements) || lock.Ratified == nil || lock.Ratified.Proposal != result.Proposal.Hash || lock.Ratified.Comment != ratify.URL {
				t.Fatal("ratification committed different pins, bounds or human authority")
			}
			if err := r.f.Repo.Fetch(t.Context()); err != nil {
				t.Fatal(err)
			}
			for path, text := range map[string]string{
				".invariant/view.json":           result.Proposal.View,
				".invariant/specs/Reader603.tla": result.Proposal.ModuleText,
				".invariant/specs/Helper603.tla": result.Proposal.Includes["Helper603.tla"],
			} {
				b, err := r.f.Repo.Show(t.Context(), "origin/"+m.Branch, m.Project+"/"+path)
				if err != nil || string(b) != text {
					t.Fatalf("ratification changed %s: %v", path, err)
				}
			}
			saved790Fresh(t, r)
			saved790Read(t, r, result.Proposal)
			before := len(r.gh.posts(790))
			r.poll()
			if len(r.gh.posts(790)) != before || len(r.build.built) != 1 {
				t.Fatal("restart repeated a completed effect")
			}
			if !stop {
				r.gh.ci(m.PR, "failure")
				r.poll()
				saved790Compact(t, r.expect(790, KindFailed, LabelHumanReview).Comment.Body, ref)
			}
		})
	}
}

func TestSavedDraft790ChoicesAndBaselines(t *testing.T) {
	for _, amend := range []bool{false, true} {
		for _, finish := range []string{"answer", "revise"} {
			t.Run(fmt.Sprintf("amend=%v/%s", amend, finish), func(t *testing.T) {
				tc, _ := reader603Tools(t)
				ws := t.TempDir()
				reader603Workspace(t, ws)
				old, _ := reader603Checked(t, ws, tc)
				r := newRig(t)
				saved790Open(t, r)
				req := formalize.Request{Previous: old}
				dir, work := "examples/saved-reader", ""
				if amend {
					work = t.TempDir()
					git(t, work, "clone", "--quiet", r.origin, ".")
					if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 600, By: "gitdek", Proposal: old.Hash}); err != nil {
						t.Fatal(err)
					}
					git(t, work, "add", "-A")
					git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed baseline")
					git(t, work, "push", "--quiet", "origin", "HEAD:main")
					var err error
					req.Current, err = r.f.current(t.Context(), dir)
					if err != nil || req.Current == nil {
						t.Fatalf("capture original amendment: %v", err)
					}
					r.gh.issues[790].Body = "Project: " + dir + "\n/invariant solve"
				}
				authority := req.CheckContext()
				draft := preview639Draft(t, 2, nil)
				preview639Input(draft, 0, 0)["model"].(map[string]any)["includes"] = map[string]string{
					"Helper603.tla": strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", maxMarker+1024)+"\n====", 1),
				}
				preview639Write(t, ws, draft)
				p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
				if err != nil || p == nil {
					t.Fatalf("check preview fixture: %v", err)
				}
				if amend {
					if err := p.Amend(req.Current); err != nil {
						t.Fatal(err)
					}
				}
				result := &formalize.Result{Proposal: p, CheckContext: &authority}
				if len(reader603JSON(t, result)) <= maxMarker {
					t.Fatal("preview fixture is not oversized")
				}
				ref := saved790Save(t, r, result, "draft-issue")
				saved790Post(t, r, ref, p)
				saved790Fresh(t, r)
				saved790Read(t, r, p)
				choice := r.gh.say(790, "gitdek", "/invariant choose F1 B")
				r.poll()
				partial := r.expect(790, KindForks, LabelAsking)
				m := saved790Compact(t, partial.Comment.Body, ref)
				if len(m.Forks) != 1 || m.Forks[0].ID != "F2" || len(m.Answers) != 1 || m.Answers[0].Option != "B" || m.Answers[0].By != "gitdek" || m.Answers[0].Comment != choice.URL {
					t.Fatal("partial choice lost the remaining fork or the person's answer")
				}
				if len(r.form.requests) != 0 || len(r.build.built) != 0 {
					t.Fatal("partial choice ran an agent")
				}
				saved790Fresh(t, r)
				state := saved790Read(t, r, p)
				if len(state.Marker.Forks) != 1 || state.Marker.Forks[0].Question != p.Forks[1].Question {
					t.Fatal("remaining fork was not hydrated from the original snapshot")
				}
				if err := state.Marker.Proposal.ValidatePreviewContext(authority); err != nil {
					t.Fatalf("partial answer rewrote original checked choices: %v", err)
				}
				before := len(r.gh.posts(790))
				r.poll()
				if len(r.gh.posts(790)) != before {
					t.Fatal("restart repeated the partial-choice post")
				}
				if amend {
					// Sources move without another ratification: the original capture stands.
					reader603Write(t, filepath.Join(work, dir, ".invariant/view.json"), strings.ReplaceAll(reader603View, "Working", "Moved branch"))
					reader603Write(t, filepath.Join(work, dir, old.Manifest().Module), old.ModuleText+"\\* later implementation\n")
					git(t, work, "add", "-A")
					git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "move branch after capture")
					git(t, work, "push", "--quiet", "origin", "HEAD:main")
				}
				var received []formalize.Request
				r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
					received = append(received, q)
					return &formalize.Result{Problem: "fixture stops after inspecting the complete request"}, nil
				}}
				command := "/invariant revise"
				if finish == "answer" {
					command = "/invariant choose F2 A"
				}
				r.gh.say(790, "gitdek", command)
				r.poll()
				if len(received) != 1 || len(r.build.built) != 0 {
					t.Fatal("answer or revision did not draft once without building")
				}
				q := received[0]
				wantPrevious, wantAnswers := p, 1
				if finish == "answer" {
					wantPrevious, wantAnswers = p.Carrier(), 2
				}
				if !bytes.Equal(reader603JSON(t, q.Previous), reader603JSON(t, wantPrevious)) || len(q.Answers) != wantAnswers || q.Answers[0].Comment != choice.URL {
					t.Fatal("draft lost the complete previous proposal, baseline carrier or actual answers")
				}
				if !bytes.Equal(reader603JSON(t, q.CheckContext().Baseline), reader603JSON(t, authority.Baseline)) {
					t.Fatal("draft replaced the captured comparison baseline")
				}
				if amend && !bytes.Equal(reader603JSON(t, q.CapturedCurrent()), reader603JSON(t, req.CapturedCurrent())) {
					// Current.Reader is workspace context and may be absent after a move;
					// the authoritative model, includes, view, lock and commit must match.
					a, b := q.CapturedCurrent(), req.CapturedCurrent()
					a.Reader, b.Reader = nil, nil
					if !bytes.Equal(reader603JSON(t, a), reader603JSON(t, b)) {
						t.Fatal("revision recaptured the moving amendment target")
					}
				}
				if finish == "revise" {
					saved790Compact(t, r.expect(790, KindStuck, LabelHumanReview).Comment.Body, ref)
					saved790Fresh(t, r)
					saved790Read(t, r, p)
				}
			})
		}
	}
}

type saved790ReadRepo struct {
	Repo
	fail  error
	bytes []byte
	reads int
}

func (r *saved790ReadRepo) Show(ctx context.Context, commit, path string) ([]byte, error) {
	if path == "result.json" {
		r.reads++
		if r.fail != nil {
			return nil, r.fail
		}
		if r.bytes != nil {
			return append([]byte(nil), r.bytes...), nil
		}
	}
	return r.Repo.Show(ctx, commit, path)
}

func (r *saved790ReadRepo) Load(context.Context, string, string) error {
	return errors.New("reference must read the single pinned result.json blob, not export the commit")
}

func TestSavedDraft790InvalidReferencesStayPending(t *testing.T) {
	result := saved790Result(t, false)
	r := newRig(t)
	saved790Open(t, r)
	// An earlier valid proposal must not become a fallback when the newest
	// authenticated marker has an unreadable reference, even at the same hash.
	r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: result.Proposal, ReplyTo: []int64{0}}).encode())
	ref := saved790Save(t, r, result, "draft-issue")
	posted := saved790Post(t, r, ref, result.Proposal)
	index := len(r.gh.comments[790]) - 1
	saved790Fresh(t, r)
	saved790Read(t, r, result.Proposal)
	r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
	if err := r.f.Repo.Record(t.Context(), 790, "draft-18", "unfinished draft"); err != nil {
		t.Fatal(err)
	}
	refs := git(t, r.origin, "for-each-ref", "--format=%(refname) %(objectname)", runsRef)
	store := &saved790ReadRepo{Repo: r.f.Repo}
	r.f.Repo = store
	cases := []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"unsupported version", func(m, x map[string]any) { x["version"] = 2 }},
		{"malformed version", func(m, x map[string]any) { x["version"] = "one" }},
		{"missing version", func(m, x map[string]any) { delete(x, "version") }},
		{"other repository", func(m, x map[string]any) { x["repository"] = "other/repository" }},
		{"other issue", func(m, x map[string]any) { x["issue"] = 791 }},
		{"zero issue", func(m, x map[string]any) { x["issue"] = 0 }},
		{"moving ref", func(m, x map[string]any) { x["commit"] = "refs/invariant/runs/790/draft-issue" }},
		{"abbreviated commit", func(m, x map[string]any) { x["commit"] = ref.Commit[:12] }},
		{"other commit", func(m, x map[string]any) { x["commit"] = strings.Repeat("a", 40) }},
		{"missing run", func(m, x map[string]any) { x["step"] = "draft-17" }},
		{"unfinished run", func(m, x map[string]any) { x["step"] = "draft-18" }},
		{"non-draft run", func(m, x map[string]any) { x["step"] = "build-790" }},
		{"unsafe step", func(m, x map[string]any) { x["step"] = "../791/draft-issue" }},
		{"bad digest", func(m, x map[string]any) { x["sha256"] = "sha256:" + strings.Repeat("0", 64) }},
		{"wrong proposal identity", func(m, x map[string]any) { x["proposal_hash"] = "sha256:" + strings.Repeat("0", 64) }},
		{"wrong ratified identity", func(m, x map[string]any) { m["hash"] = "sha256:" + strings.Repeat("0", 64) }},
		{"caller path", func(m, x map[string]any) { x["path"] = "../../result.json" }},
		{"caller URL", func(m, x map[string]any) { x["url"] = "https://untrusted.invalid/result.json" }},
		{"conflicting inline proposal", func(m, x map[string]any) {
			p := reader603Object(t, result.Proposal)
			p["dir"] = "examples/substituted"
			m["proposal"] = p
		}},
		{"reference is null", func(m, x map[string]any) { m["proposal_ref"] = nil }},
		{"reference is a string", func(m, x map[string]any) { m["proposal_ref"] = "https://untrusted.invalid" }},
		{"record unavailable", func(m, x map[string]any) { store.fail = errors.New("saved record temporarily unavailable") }},
		{"record truncated", func(m, x map[string]any) { b := reader603JSON(t, result); store.bytes = b[:len(b)/2] }},
		{"record substituted", func(m, x map[string]any) { store.bytes = append(reader603JSON(t, result), '\n') }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store.fail, store.bytes = nil, nil
			m := saved790Envelope(ref, result.Proposal)
			x := reader603Object(t, ref)
			m["proposal_ref"] = x
			c.edit(m, x)
			r.gh.comments[790][index].Body = saved790Wire(t, m)
			before := len(r.gh.comments[790])
			if _, err := r.f.read(t.Context(), *r.gh.issues[790]); err == nil {
				t.Fatal("invalid reference became a readable thread or fell back to an older proposal")
			}
			if _, err := r.f.postsOn(t.Context(), 790); err == nil {
				t.Fatal("parent-step reader ignored an invalid reference")
			}
			if _, err := r.f.superseded(t.Context(), 790, Post{Comment: posted}); err == nil {
				t.Fatal("build recovery reader ignored an invalid reference")
			}
			err := r.f.Poll(t.Context())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "reference") {
				t.Fatalf("failed reference has no actionable diagnostic: %v", err)
			}
			if len(r.gh.comments[790]) != before || len(r.form.requests) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatal("unreadable reference consumed a command, published a proposal, ran an agent or started a build")
			}
			if got := git(t, r.origin, "for-each-ref", "--format=%(refname) %(objectname)", runsRef); got != refs {
				t.Fatal("reference failure changed durable run records")
			}
		})
	}
	// A byte-for-byte matching inline proposal is not a conflict, and the
	// reference still wins on the next encoding; no oversized inline is needed.
	store.fail, store.bytes = nil, nil
	m := saved790Envelope(ref, result.Proposal)
	m["proposal"] = result.Proposal
	r.gh.comments[790][index].Body = saved790Wire(t, m)
	state := saved790Read(t, r, result.Proposal)
	saved790Compact(t, state.Marker.encode(), ref)
	// Restore the record without adding another command. The pending ratify
	// must still run exactly once, even on this watcher's already-polled issue.
	r.gh.comments[790][index].Body = posted.Body
	r.f.Repo = store.Repo
	r.poll()
	if len(r.build.built) != 1 || len(r.form.requests) != 0 {
		t.Fatal("repaired reference did not retry the original pending ratification")
	}
	r.poll()
	if len(r.build.built) != 1 {
		t.Fatal("successful retry built twice")
	}
}

func TestSavedDraft790EvidenceCannotBeSubstituted(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, oldReport := reader603Checked(t, ws, tc)
	authority := formalize.Request{Previous: old}.CheckContext()
	// The new chart deliberately has the same proposal hash, so hash-only
	// validation cannot stand in for complete checked comparison identity.
	reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised working"))
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil || p == nil || p.Reader.Comparison == nil {
		t.Fatalf("comparison fixture: %v", err)
	}
	good := &formalize.Result{Proposal: p, Report: report, CheckContext: &authority,
		Changes: formalize.Diff(&formalize.Current{ModuleText: old.ModuleText, Lock: project.Lock{Bounds: old.Bounds, Statements: old.Statements}}, p)}
	preview639Write(t, ws, preview639Draft(t, 1, nil))
	forks, _, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil {
		t.Fatal(err)
	}
	options := &formalize.Result{Proposal: forks, CheckContext: &authority}
	r := newRig(t)
	saved790Open(t, r)
	ref := saved790Save(t, r, good, "draft-issue")
	saved790Post(t, r, ref, good.Proposal)
	saved790Read(t, r, good.Proposal)
	// Verify that the shared contract also returns the report, changes and
	// original caller context, rather than rebuilding an abbreviated result.
	marker, _ := DecodeMarker(r.gh.comments[790][0].Body)
	resolver, ok := any(marker).(saved790Resolver)
	if !ok {
		t.Fatal("missing shared saved-result resolver")
	}
	_, loaded, err := resolver.ResolveDraft(t.Context(), "o/r", 790, r.f.Repo.Run, r.f.Repo.Show)
	if err != nil || !bytes.Equal(reader603JSON(t, loaded), reader603JSON(t, good)) {
		t.Fatalf("complete comparison result changed on load: %v", err)
	}
	cases := []struct {
		name     string
		previews bool
		edit     func(map[string]any)
	}{
		{"missing proposal", false, func(x map[string]any) { delete(x, "proposal") }},
		{"missing check context", false, func(x map[string]any) { delete(x, "check_context") }},
		{"missing report", false, func(x map[string]any) { delete(x, "report") }},
		{"failed result", false, func(x map[string]any) { x["problem"] = "draft checks failed" }},
		{"failed report", false, func(x map[string]any) { x["report"].(map[string]any)["passed"] = false }},
		{"missing reader", false, func(x map[string]any) { delete(x["proposal"].(map[string]any), "reader") }},
		{"changed source", false, func(x map[string]any) {
			x["proposal"].(map[string]any)["module_text"] = reader603Model + "\\* substituted\n"
		}},
		{"changed include", false, func(x map[string]any) {
			x["proposal"].(map[string]any)["includes"].(map[string]any)["Helper603.tla"] = reader603Include + "\\* substituted\n"
		}},
		{"changed bounds", false, func(x map[string]any) { x["proposal"].(map[string]any)["bounds"].(map[string]any)["Limit"] = "1" }},
		{"changed pins", false, func(x map[string]any) {
			x["proposal"].(map[string]any)["statements"].([]any)[1].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("0", 64)
		}},
		{"changed reader", false, func(x map[string]any) {
			x["proposal"].(map[string]any)["reader"].(map[string]any)["fingerprint"] = "sha256:" + strings.Repeat("0", 64)
		}},
		{"changed comparison", false, func(x map[string]any) {
			x["proposal"].(map[string]any)["reader"].(map[string]any)["comparison"].(map[string]any)["baseline"].(map[string]any)["view"] = "substituted"
		}},
		{"wrong captured baseline", false, func(x map[string]any) {
			x["check_context"].(map[string]any)["baseline"].(map[string]any)["view"] = "substituted"
		}},
		{"required comparison absent", false, func(x map[string]any) {
			// A valid standalone reader still cannot satisfy this saved context.
			x["proposal"], x["report"] = reader603Object(t, old), reader603Object(t, oldReport)
		}},
		{"missing option", true, func(x map[string]any) { delete(preview639Option(x["proposal"].(map[string]any), 0, 0), "preview") }},
		{"swapped options", true, func(x map[string]any) {
			a, b := preview639Option(x["proposal"].(map[string]any), 0, 0), preview639Option(x["proposal"].(map[string]any), 0, 1)
			a["preview"], b["preview"] = b["preview"], a["preview"]
		}},
		{"failed preview receipt", true, func(x map[string]any) {
			preview639Input(x["proposal"].(map[string]any), 0, 0)["receipt"].(map[string]any)["passed"] = false
		}},
		{"missing preview receipt", true, func(x map[string]any) { delete(preview639Input(x["proposal"].(map[string]any), 0, 0), "receipt") }},
		{"invented preview choices", true, func(x map[string]any) {
			preview639Input(x["proposal"].(map[string]any), 0, 0)["choices"] = map[string]string{"F0": "A"}
		}},
		{"wrong caller choices", true, func(x map[string]any) { x["check_context"].(map[string]any)["choices"] = map[string]string{"F0": "A"} }},
		{"planning previews", true, func(x map[string]any) { x["check_context"].(map[string]any)["planning"] = "plumbing" }},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := good
			if c.previews {
				base = options
			}
			x := reader603Object(t, base)
			c.edit(x)
			// These are actual corrupt saved records, with their own correct
			// digests and completed runs; a transport hash alone cannot admit them.
			bad := saved790SaveBytes(t, r, 790, fmt.Sprintf("draft-%d", i+1), reader603JSON(t, x), base.Proposal.Hash)
			r.gh.comments[790][0].Body = saved790Wire(t, saved790Envelope(bad, base.Proposal))
			if _, err := r.f.read(t.Context(), *r.gh.issues[790]); err == nil {
				t.Fatal("corrupt checked evidence became a usable proposal")
			}
			before := len(r.gh.comments[790])
			if err := r.f.Poll(t.Context()); err == nil {
				t.Fatal("corrupt saved result was not diagnosed")
			}
			if len(r.gh.comments[790]) != before || len(r.form.requests) != 0 || len(r.build.built) != 0 {
				t.Fatal("invalid evidence was published, redrafted or built")
			}
		})
	}
	// The original checked result remains readable after every rejected record.
	r.gh.comments[790][0].Body = saved790Wire(t, saved790Envelope(ref, good.Proposal))
	saved790Read(t, r, good.Proposal)
}

func TestSavedDraft790OnlyFactoryCommentsEstablishReferences(t *testing.T) {
	result := saved790Result(t, false)
	r := newRig(t)
	saved790Open(t, r)
	ref := saved790Save(t, r, result, "draft-issue")
	saved790Post(t, r, ref, result.Proposal)
	saved790Read(t, r, result.Proposal)
	for _, author := range []string{"mallory", "gitdek", "another-app[bot]"} {
		// A foreign reference with a conflicting proposal must never be
		// resolved merely because its envelope looks like a factory post.
		foreign := ref
		foreign.Repository, foreign.Issue, foreign.Step = "other/repository", 791, "draft-999"
		m := saved790Envelope(foreign, result.Proposal)
		r.gh.say(790, author, saved790Wire(t, m))
		m["proposal_ref"] = "malformed reference"
		r.gh.say(790, author, saved790Wire(t, m))
	}
	reads := &saved790ReadRepo{Repo: r.f.Repo}
	r.f.Repo = reads
	thread, err := r.f.read(t.Context(), *r.gh.issues[790])
	if err != nil {
		t.Fatalf("non-factory comment blocked the legitimate reference: %v", err)
	}
	if len(thread.Posts) != 1 || thread.Posts[0].Marker.Proposal == nil || reads.reads == 0 {
		t.Fatal("reference authority did not follow the existing factory-author rule")
	}
	state, _ := thread.State()
	if state.Marker.Proposal.Hash != result.Proposal.Hash {
		t.Fatal("a person or unrelated bot replaced the working proposal")
	}
	r.poll()
	if len(r.form.requests) != 0 || len(r.build.built) != 0 {
		t.Fatal("a non-factory reference caused an agent action")
	}
}

type saved790FinishRepo struct {
	Repo
	t       *testing.T
	payload []byte
	commit  string
}

func (r *saved790FinishRepo) Finish(ctx context.Context, issue int, step, result string) error {
	if issue == 790 && step == "draft-issue" {
		b, err := r.Repo.Show(ctx, result, "result.json")
		if err != nil {
			return err
		}
		var saved formalize.Result
		if err := json.Unmarshal(b, &saved); err != nil {
			return err
		}
		if saved.Proposal == nil || saved.Proposal.Dir != "examples/chosen-reader" || saved.Proposal.Agent != "codex" || saved.CheckContext == nil {
			r.t.Fatal("Finish exposed a result that had not yet saved its effective project, agent and check context")
		}
		r.payload, r.commit = b, result
	}
	return r.Repo.Finish(ctx, issue, step, result)
}

func TestSavedDraft790EffectivePayloadPrecedesCompletion(t *testing.T) {
	result := saved790Result(t, false)
	r := newRig(t)
	saved790Open(t, r)
	calls := 0
	form := reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) {
		calls++
		var fresh formalize.Result
		if err := json.Unmarshal(reader603JSON(t, result), &fresh); err != nil {
			t.Fatal(err)
		}
		return &fresh, nil
	}}
	r.f.Agent, r.f.Formalizer = "codex", form
	r.f.Agents = map[string]Runners{claudeCode: {Formalizer: form, Builder: r.build}}
	r.gh.issues[790].Body = "Project: examples/chosen-reader\nAgent: codex\n/invariant solve"
	save := &saved790FinishRepo{Repo: r.f.Repo, t: t}
	r.f.Repo = save
	// Crash after Finish, before any publication, exactly as the recovery rig
	// does. The watcher must later publish the already saved effective result.
	r.f.GitHub = crashGitHub{GitHub: r.gh, c: &crasher{at: 1}}
	if err := r.f.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), errCrash.Error()) {
		t.Fatalf("fixture did not stop before publication: %v", err)
	}
	if calls != 1 || save.commit == "" || len(r.gh.comments[790]) != 0 {
		t.Fatal("crash did not leave exactly one completed unpublished draft")
	}
	r.gh.issues[790].Body = "Project: examples/edited-after-check\nAgent: claude-code\n/invariant solve"
	saved790Fresh(t, r)
	r.poll()
	post := r.expect(790, KindProposal, LabelProposal)
	var saved formalize.Result
	if err := json.Unmarshal(save.payload, &saved); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || post.Marker.Agent != "codex" || !bytes.Equal(reader603JSON(t, post.Marker.Proposal), reader603JSON(t, saved.Proposal)) {
		t.Fatal("completed-run recovery reran the agent or reconstructed different publication authority")
	}
	// Publication is still inline in this step. Explicit reference transport
	// of those same bytes recovers identically on another fresh clone.
	if _, exists := reader603Object(t, post.Marker)["proposal_ref"]; exists {
		t.Fatal("ordinary publication was switched to references in this step")
	}
	ref := saved790Reference{1, "o/r", 790, "draft-issue", save.commit, saved790Digest(save.payload), saved.Proposal.Hash}
	r.gh.comments[790][0].Body = saved790Wire(t, saved790Envelope(ref, saved.Proposal))
	saved790Fresh(t, r)
	saved790Read(t, r, saved.Proposal)
	r.poll()
	if calls != 1 || len(r.gh.comments[790]) != 1 {
		t.Fatal("reference recovery repeated completed drafting or publication")
	}
}
