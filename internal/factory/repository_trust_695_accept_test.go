package factory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/scope"
	"github.com/gitdek/invariant/internal/setup"
)

// These fixtures start below external issue intake. They use real committed
// contracts, diffs, locks, publication and merge checks; only GitHub is fake.
// They neither run repository commands nor require Docker or network access.
type trust695 struct {
	r        *rig
	c        Clone
	g        *draftGitHub
	config   repository.Config
	snapshot *repository.Snapshot
	base     string
}

func writeTrust695(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsonTrust695(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newTrust695(t *testing.T) *trust695 {
	t.Helper()
	r := newRig(t)
	g := &draftGitHub{fakeGitHub: r.gh}
	r.f.GitHub = g
	r.f.writers = map[string]bool{"gitdek": true}
	c := r.f.Repo.(Clone)
	commands := map[string]repository.Command{}
	for _, phase := range []string{"install", "format", "lint", "build"} {
		commands[phase] = repository.Command{NotApplicable: "No commands run in this trust fixture."}
	}
	commands["test"] = repository.Command{Argv: []string{"go", "test", "./..."}}
	commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}", "-count=1"}}
	config := repository.Config{Version: 1,
		Parts: []repository.Part{{Name: "app", Directory: ".", Language: "go", Image: "golang@sha256:" + strings.Repeat("a", 64),
			Lockfiles: []string{"go.sum"}, Inputs: []string{"go.mod"}, Commands: commands}},
		AgentRules: []string{"guidance.md"}, CurrentState: []string{"README.md"},
		TrustedPaths: []string{"policy/"}, SharedPaths: []repository.SharedPath{},
	}
	workflow, err := setup.Workflow(strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		repository.Path: jsonTrust695(t, config), "go.mod": "module example.invalid/app\n\ngo 1.27.1\n", "go.sum": "",
		"app/main.go": "package app\nconst Value = 1\n", "guidance.md": "Repository guidance.\n",
		"AGENTS.md": "Root rules.\n", "app/AGENTS.md": "Nested rules.\n", "app/CLAUDE.md": "Nested instructions.\n",
		"app/.codex/config.toml": "model = 'fixture'\n", "app/.claude/settings.json": "{}\n", "app/.mcp.json": "{}\n",
		"policy/guard.txt": "Trusted policy.\n", ".github/workflows/invariant.yml": workflow,
		".github/CODEOWNERS": "* @owner\n", ".gitlab-ci.yml": "stages: [test]\n", ".circleci/config.yml": "version: 2.1\n",
	} {
		writeTrust695(t, c.Dir, file, body)
	}
	base, err := c.Commit(t.Context(), c.Dir, ".", "configured baseline")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Push(t.Context(), c.Dir, "main"); err != nil {
		t.Fatal(err)
	}
	if err := c.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Load(t.Context(), c.Dir, base)
	if err != nil {
		t.Fatal(err)
	}
	r.gh.open(1, "gitdek", "Hold repository authority", "Kind: plumbing", LabelBuilding)
	return &trust695{r: r, c: c, g: g, config: config, snapshot: snapshot, base: base}
}

const sourceTrust695 = "package app\nimport \"testing\"\nfunc TestChange695(t *testing.T) { if Value != 2 { t.Fatal(Value) } }\n"

func (x *trust695) draft(t *testing.T, files ...string) *plumbing.Plan {
	t.Helper()
	ws := t.TempDir()
	draft := map[string]any{"name": "repository trust", "summary": "Change only the named files.", "files": files,
		"tests": []plumbing.Test{{Name: "changed value", Part: "app", Framework: "go", File: "app/change_695_accept_test.go", Selector: "TestChange695", Says: "The value changes."}}}
	writeTrust695(t, ws, "plan.json", jsonTrust695(t, draft))
	writeTrust695(t, ws, "tests/app/change_695_accept_test.go", sourceTrust695)
	p, err := plumbing.ReadRepositoryDraft(t.Context(), ws, x.snapshot)
	if err != nil {
		t.Fatalf("configured draft must be admitted: %v", err)
	}
	return p
}

// The ratified plan deliberately lies in its display-only Trusted metadata.
// Scope and publication must derive trust from the actual diff and base rules.
func (x *trust695) candidate(t *testing.T, files []string, changes map[string]string, forged []string) (*plumbing.Plan, string, string, Post) {
	t.Helper()
	p := x.draft(t, files...)
	branch := "invariant/issue-1-repository-trust"
	wt, err := x.c.Worktree(t.Context(), branch, x.base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = x.c.RemoveWorktree(context.Background(), wt) })
	lock := plumbing.Lock{Plan: *p, Ratified: plumbing.Ratification{By: "gitdek", Issue: 1, Proposal: p.Hash(), Comment: "https://github.com/o/r/issues/1#issuecomment-1"}}
	if err := lock.Write(wt); err != nil {
		t.Fatal(err)
	}
	lock.Plan.Trusted = forged
	writeTrust695(t, wt, plumbing.LockPath(1), jsonTrust695(t, lock))
	if _, err := x.c.Commit(t.Context(), wt, ".", "ratification"); err != nil {
		t.Fatal(err)
	}
	if err := x.c.Push(t.Context(), wt, branch); err != nil {
		t.Fatal(err)
	}
	for file, body := range changes {
		writeTrust695(t, wt, file, body)
	}
	code, err := x.c.Commit(t.Context(), wt, ".", "candidate")
	if err != nil {
		t.Fatal(err)
	}
	p.Trusted = forged
	proposal := &formalize.Proposal{Plan: p, Hash: p.Hash()}
	m := Marker{Kind: KindRatified, Project: plumbing.LockPath(1), Branch: branch, Hash: p.Hash(), Proposal: proposal}
	comment := x.r.gh.say(1, "gitdek", post("ratified", "The fixture's plan was ratified.", m))
	return p, code, branch, Post{Comment: comment, Marker: m}
}

func (x *trust695) thread(t *testing.T) Thread {
	t.Helper()
	thread, err := x.r.f.read(t.Context(), *x.r.gh.issues[1])
	if err != nil {
		t.Fatal(err)
	}
	return thread
}

func (x *trust695) advance(t *testing.T, change func(*repository.Config), remove bool) {
	t.Helper()
	wt, err := x.c.Worktree(t.Context(), "advance-base", "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	defer x.c.RemoveWorktree(context.Background(), wt)
	config := x.config
	change(&config)
	if remove {
		if err := os.Remove(filepath.Join(wt, repository.Path)); err != nil {
			t.Fatal(err)
		}
	} else {
		writeTrust695(t, wt, repository.Path, jsonTrust695(t, config))
	}
	writeTrust695(t, wt, "README.md", "A later base commit.\n")
	if _, err := x.c.Commit(t.Context(), wt, ".", "advance base"); err != nil {
		t.Fatal(err)
	}
	if err := x.c.Push(t.Context(), wt, "main"); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryImplicitTrust695(t *testing.T) {
	x := newTrust695(t)
	for _, file := range []string{repository.Path, ".github/workflows/invariant.yml", ".github/CODEOWNERS", ".github/workflows/new.yml",
		".gitlab-ci.yml", ".circleci/config.yml", "AGENTS.md", "app/AGENTS.md", "new/nested/AGENTS.md", "app/CLAUDE.md",
		"app/.codex/config.toml", "app/.claude/settings.json", "app/.mcp.json", "guidance.md", "go.mod", "go.sum", "policy/guard.txt", "policy/future.txt"} {
		got, err := x.snapshot.IsTrusted(file)
		if err != nil || !got {
			t.Errorf("%s must be trusted even without an explicit trusted_paths entry: %v, %v", file, got, err)
		}
	}
	for _, file := range []string{"app/main.go", "README.md", "docs/notes.md", "policyish/value.txt", "app/AGENTS.md.bak", "app/.mcp.json.example", "internal/factory/ordinary.go"} {
		got, err := x.snapshot.IsTrusted(file)
		if err != nil || got {
			t.Errorf("ordinary external file %s acquired Invariant's trust policy: %v, %v", file, got, err)
		}
	}
	x.advance(t, func(c *repository.Config) { c.TrustedPaths = []string{}; c.AgentRules = []string{} }, false)
	for _, file := range []string{"policy/guard.txt", "guidance.md", "app/AGENTS.md"} {
		got, err := x.snapshot.IsTrusted(file)
		if err != nil || !got {
			t.Errorf("captured authority changed after base moved for %s: %v, %v", file, got, err)
		}
	}
}

func TestConfiguredProposalTrustAndCI695(t *testing.T) {
	x := newTrust695(t)
	p := x.draft(t, ".github/workflows/invariant.yml", "policy/guard.txt", "app/AGENTS.md", "app/main.go")
	want := []string{".github/workflows/invariant.yml", "app/AGENTS.md", "policy/guard.txt"}
	if !reflect.DeepEqual(p.Trusted, want) {
		t.Errorf("proposal trust = %v; want %v", p.Trusted, want)
	}
	body := planComment(&formalize.Proposal{Plan: p, Hash: p.Hash()}, Marker{})
	if !strings.Contains(body, "a person merges it") || strings.Contains(body, "I merge it.") {
		t.Errorf("proposal promises an automatic merge:\n%s", body)
	}
	hash := p.Hash()
	kept := slices.Clone(p.Trusted)
	p.Trusted = []string{"app/main.go"}
	if p.Hash() != hash {
		t.Fatal("display metadata changed ratification identity")
	}
	if err := p.ValidateRepository(t.Context(), x.snapshot); err != nil {
		t.Fatal(err)
	}
	// Verification remains safe for concurrent callers. The supplied display
	// list is not execution or merge authority; scope tests below forge it.
	p.Trusted = kept
	lock := plumbing.Lock{Plan: *p, Ratified: plumbing.Ratification{Issue: 1, Proposal: hash}}
	read, err := plumbing.ReadLock([]byte(jsonTrust695(t, lock)))
	if err != nil {
		t.Fatalf("configured CI lock must round trip: %v", err)
	}
	if err := read.Plan.ValidateRepository(t.Context(), x.snapshot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read.Plan.Trusted, want) {
		t.Errorf("lock lost captured trust: %v", read.Plan.Trusted)
	}
	legacy := &plumbing.Plan{Name: "legacy", Summary: "Invariant keeps refusing CI edits.", Files: []string{".github/workflows/invariant.yml"},
		Tests: []plumbing.Test{{Name: "TestChange695", File: "app/change_695_accept_test.go", Says: "Changes value."}}, Sources: map[string]string{"app/change_695_accept_test.go": sourceTrust695}}
	if err := legacy.Validate(); err == nil {
		t.Error("Invariant's legacy CI restriction was removed")
	}
}

func TestConfiguredScopeTrustCannotBeForged695(t *testing.T) {
	for _, mode := range []string{"custom", "nested rules", "workflow", "remove declaration", "delete declaration", "delete trusted file", "implicit regression", "ordinary with forged trust", "unplanned CI"} {
		t.Run(mode, func(t *testing.T) {
			x := newTrust695(t)
			files, changes, want := []string{"policy/guard.txt", "app/main.go"}, map[string]string{"policy/guard.txt": "changed\n"}, []string{"policy/guard.txt"}
			var forged []string
			switch mode {
			case "nested rules":
				files, changes, want = []string{"app/AGENTS.md"}, map[string]string{"app/AGENTS.md": "changed\n"}, []string{"app/AGENTS.md"}
			case "workflow":
				files, changes, want = []string{".github/workflows/invariant.yml"}, map[string]string{".github/workflows/invariant.yml": "name: changed\n"}, []string{".github/workflows/invariant.yml"}
			case "remove declaration", "delete declaration":
				files = append(files, repository.Path)
				config := x.config
				config.TrustedPaths = []string{}
				changes[repository.Path] = jsonTrust695(t, config)
				want = []string{repository.Path, "policy/guard.txt"}
			case "implicit regression":
				files = []string{"policy/value.go"}
				changes = map[string]string{"policy/value.go": "package policy\n", "policy/added_test.go": "package policy\n"}
				want = []string{"policy/added_test.go", "policy/value.go"}
			case "ordinary with forged trust":
				files, changes, want, forged = []string{"app/main.go", "policy/guard.txt"}, map[string]string{"app/main.go": "package app\nconst Value = 2\n"}, nil, []string{"app/main.go"}
			case "unplanned CI":
				files, changes, want = []string{"app/main.go"}, map[string]string{"app/main.go": "package app\nconst Value = 2\n", ".github/workflows/invariant.yml": "name: changed\n"}, []string{".github/workflows/invariant.yml"}
			}
			_, code, branch, _ := x.candidate(t, files, changes, forged)
			if mode == "delete declaration" || mode == "delete trusted file" {
				wt, err := x.c.Worktree(t.Context(), "delete-path", code)
				if err != nil {
					t.Fatal(err)
				}
				defer x.c.RemoveWorktree(context.Background(), wt)
				file := repository.Path
				if mode == "delete trusted file" {
					file = "policy/guard.txt"
				}
				if err := os.Remove(filepath.Join(wt, file)); err != nil {
					t.Fatal(err)
				}
				code, err = x.c.Commit(t.Context(), wt, ".", "delete trusted path")
				if err != nil {
					t.Fatal(err)
				}
			}
			_ = branch
			r, err := x.c.Scope(t.Context(), x.base, code, 1)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK() != (mode != "unplanned CI") {
				t.Errorf("unexpected scope verdict: %+v", r)
			}
			if !slices.Equal(r.Trusted, want) {
				t.Errorf("actual trusted diff = %v; want %v", r.Trusted, want)
			}
		})
	}
}

func TestConfiguredScopeRevalidatesCurrentBase695(t *testing.T) {
	for _, mode := range []string{"compatible movement", "remove trust", "add trust", "change checks", "remove contract"} {
		t.Run(mode, func(t *testing.T) {
			x := newTrust695(t)
			_, code, _, _ := x.candidate(t, []string{"app/main.go"}, map[string]string{"app/main.go": "package app\nconst Value = 2\n"}, nil)
			x.advance(t, func(c *repository.Config) {
				switch mode {
				case "remove trust":
					c.TrustedPaths = []string{}
				case "add trust":
					c.TrustedPaths = []string{"policy/", "app/main.go"}
				case "change checks":
					c.Parts = slices.Clone(c.Parts)
					c.Parts[0].Image = "golang@sha256:" + strings.Repeat("b", 64)
				}
			}, mode == "remove contract")
			if err := x.c.Fetch(t.Context()); err != nil {
				t.Fatal(err)
			}
			r, err := x.c.Scope(t.Context(), "origin/main", code, 1)
			if mode == "compatible movement" {
				if err != nil || !r.OK() || len(r.Trusted) != 0 {
					t.Errorf("compatible movement blocked ordinary change: %+v, %v", r, err)
				}
			} else if err == nil && r.OK() {
				t.Errorf("%s on the current base authorized a stale candidate: %+v", mode, r)
			}
		})
	}
}

func TestConfiguredPublicationKeepsTrustedDrafts695(t *testing.T) {
	for _, file := range []string{"policy/guard.txt", "AGENTS.md", "app/AGENTS.md", "app/.mcp.json", repository.Path, ".github/workflows/invariant.yml", "app/main.go"} {
		for _, recovered := range []bool{false, true} {
			t.Run(file+map[bool]string{false: "/new", true: "/recovered"}[recovered], func(t *testing.T) {
				x := newTrust695(t)
				body := "changed\n"
				if file == repository.Path {
					c := x.config
					c.TrustedPaths = []string{}
					body = jsonTrust695(t, c)
				}
				if file == "app/main.go" {
					body = "package app\nconst Value = 2\n"
				}
				_, code, branch, ratified := x.candidate(t, []string{file}, map[string]string{file: body}, nil)
				if recovered {
					if err := x.c.PushCommit(t.Context(), code, branch); err != nil {
						t.Fatal(err)
					}
					if _, err := x.g.CreatePullRequest(t.Context(), github.NewPullRequest{Head: branch, Base: "main", Draft: true}); err != nil {
						t.Fatal(err)
					}
				}
				res := &plumbing.BuildResult{Passed: true, Approved: true, Tests: 1, Summary: "Checks passed.", Review: "VERDICT: approve"}
				built := builtResult{Plumbing: res}
				result, err := x.r.f.saveResult(t.Context(), built, "build result", code)
				if err != nil {
					t.Fatal(err)
				}
				next := ratified.Marker.carried()
				next.Kind = KindFailed
				if err := x.r.f.publishPlan(t.Context(), x.thread(t), ratified, result, built, next, 0); err != nil {
					t.Fatal(err)
				}
				if len(x.g.prs) != 1 {
					t.Fatalf("publication created %d PRs", len(x.g.prs))
				}
				for n, pr := range x.g.prs {
					trusted := file != "app/main.go"
					if pr.Draft != trusted {
						t.Errorf("trusted=%v, draft=%v after publication", trusted, pr.Draft)
					}
					if trusted && (len(x.g.calls) != 0 || !strings.Contains(x.g.prBody(n), file) || !strings.Contains(x.r.gh.last(1).Comment.Body, "person")) {
						t.Errorf("trusted publication did not preserve human handoff: calls=%v\n%s", x.g.calls, x.r.gh.last(1).Comment.Body)
					}
				}
			})
		}
	}
}

func (x *trust695) watched(t *testing.T, file string, forged []string) (int, Post) {
	t.Helper()
	body := "changed\n"
	if file == "app/main.go" {
		body = "package app\nconst Value = 2\n"
	}
	if file == repository.Path {
		c := x.config
		c.TrustedPaths = []string{}
		body = jsonTrust695(t, c)
	}
	_, code, branch, ratified := x.candidate(t, []string{file}, map[string]string{file: body}, forged)
	if err := x.c.PushCommit(t.Context(), code, branch); err != nil {
		t.Fatal(err)
	}
	pr, err := x.g.CreatePullRequest(t.Context(), github.NewPullRequest{Head: branch, Base: "main", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	m := ratified.Marker.carried()
	m.Kind, m.Failure, m.PR = KindFailed, FailCI, pr.Number
	x.r.gh.say(1, "gitdek", post("CI failed", "Retry after CI passes.", m))
	x.r.gh.say(1, "gitdek", "/invariant retry")
	x.r.poll()
	answer := x.r.expect(1, KindPR, LabelPR)
	if answer.Marker.Watched != WatchedDraft {
		t.Fatal("retry did not watch the draft")
	}
	x.r.gh.ci(pr.Number, "success")
	return pr.Number, answer
}

func TestConfiguredRetryRecomputesTrust695(t *testing.T) {
	for _, file := range []string{"policy/guard.txt", "app/AGENTS.md", repository.Path, ".github/workflows/invariant.yml", "app/main.go"} {
		t.Run(file, func(t *testing.T) {
			x := newTrust695(t)
			forged := []string(nil)
			if file == "app/main.go" {
				forged = []string{file}
			}
			n, answer := x.watched(t, file, forged)
			trusted := file != "app/main.go"
			visible := retry263Visible(answer.Comment.Body)
			if trusted && (!strings.Contains(visible, "a person merges it") || mergePromise.MatchString(visible)) {
				t.Errorf("retry promised a trusted merge: %s", visible)
			}
			x.r.poll()
			if trusted {
				last := x.r.expect(1, KindFailed, LabelHumanReview)
				if last.Marker.Why() != FailTrusted || !x.g.prs[n].Draft || len(x.g.calls) != 0 || len(x.g.merged) != 0 {
					t.Errorf("retry escaped human hold: %+v, calls=%v", last.Marker, x.g.calls)
				}
			} else {
				x.r.expect(1, KindMerged, LabelMerged)
				if len(x.g.merged) != 1 {
					t.Error("forged Trusted metadata blocked ordinary merge")
				}
			}
		})
	}
}

type moveBaseTrust695 struct {
	Repo
	move  func()
	moved bool
}

func (r *moveBaseTrust695) Scope(ctx context.Context, base, head string, issue int) (scope.Result, error) {
	s, err := r.Repo.Scope(ctx, base, head, issue)
	if err == nil && !r.moved {
		r.moved = true
		r.move()
	}
	return s, err
}

func TestConfiguredLastMergeCheckRefreshesBase695(t *testing.T) {
	for _, draft := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "draft"}[draft], func(t *testing.T) {
			x := newTrust695(t)
			n, _ := x.watched(t, "app/main.go", nil)
			x.g.prs[n].Draft = draft
			race := &moveBaseTrust695{Repo: x.c, move: func() {
				x.advance(t, func(c *repository.Config) { c.TrustedPaths = []string{"policy/", "app/main.go"} }, false)
			}}
			x.r.f.Repo = race
			x.r.poll()
			if !race.moved {
				t.Fatal("did not reach scope")
			}
			if len(x.g.calls) != 0 || len(x.g.merged) != 0 || x.g.prs[n].Draft != draft {
				t.Errorf("stale base permitted readiness or merge: %v", x.g.calls)
			}
			x.r.poll()
			if len(x.g.merged) != 0 || !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
				t.Error("incompatible base did not stop for a person")
			}
		})
	}
}

type rebuildTrust695 struct{ builds int }

func (b *rebuildTrust695) Build(ctx context.Context, root string, issue int, out string) (*plumbing.BuildResult, error) {
	b.builds++
	if err := os.WriteFile(filepath.Join(root, "policy/guard.txt"), []byte("rebuilt trusted change\n"), 0o644); err != nil {
		return nil, err
	}
	return &plumbing.BuildResult{Changes: plumbing.Changes{Written: []string{"policy/guard.txt"}}, Passed: true, Approved: true, Tests: 1, Summary: "Passed.", Review: "VERDICT: approve"}, nil
}

func TestConfiguredRebuildRetainsHumanHold695(t *testing.T) {
	x := newTrust695(t)
	_, code, branch, ratified := x.candidate(t, []string{"policy/guard.txt"}, map[string]string{"policy/guard.txt": "first attempt\n"}, nil)
	if err := x.c.PushCommit(t.Context(), code, branch); err != nil {
		t.Fatal(err)
	}
	pr, err := x.g.CreatePullRequest(t.Context(), github.NewPullRequest{Head: branch, Base: "main", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	m := ratified.Marker.carried()
	m.Kind, m.Failure, m.PR = KindFailed, FailGate, pr.Number
	x.r.gh.say(1, "gitdek", post("build failed", "Checks failed.", m))
	builder := &rebuildTrust695{}
	x.r.f.Plumbing = builder
	x.r.gh.say(1, "gitdek", "/invariant retry")
	x.r.poll()
	x.r.poll()
	if builder.builds != 1 || x.g.prs[pr.Number].State != "closed" || len(x.g.prs) != 2 {
		t.Fatalf("retry must replace the failed draft exactly once: builds=%d, old=%+v, PRs=%d", builder.builds, x.g.prs[pr.Number], len(x.g.prs))
	}
	for n, successor := range x.g.prs {
		if n == pr.Number {
			continue
		}
		if !successor.Draft || successor.Merged || len(x.g.calls) != 0 {
			t.Errorf("passing rebuild escaped the human hold: %+v, calls=%v", successor, x.g.calls)
		}
		if !strings.Contains(x.g.prBody(n), "policy/guard.txt") || !strings.Contains(x.r.gh.last(1).Comment.Body, "person") {
			t.Error("rebuild publication lost actual trusted paths or handoff")
		}
	}
}

type workflowDeniedTrust695 struct {
	Repo
	pushes int
}

func (r *workflowDeniedTrust695) PushCommit(context.Context, string, string) error {
	r.pushes++
	return errors.New("remote: refusing to allow a GitHub App to create or update workflow `.github/workflows/invariant.yml` without `workflows` permission")
}

func TestWorkflowPermissionRefusalHandsOff695(t *testing.T) {
	x := newTrust695(t)
	_, code, _, ratified := x.candidate(t, []string{".github/workflows/invariant.yml"}, map[string]string{".github/workflows/invariant.yml": "name: changed\n"}, nil)
	res := &plumbing.BuildResult{Passed: true, Approved: true, Tests: 1, Summary: "Checks passed.", Review: "VERDICT: approve"}
	built := builtResult{Plumbing: res}
	result, err := x.r.f.saveResult(t.Context(), built, "build result", code)
	if err != nil {
		t.Fatal(err)
	}
	step := buildStepName(ratified)
	if err := x.c.Record(t.Context(), 1, step, "build started"); err != nil {
		t.Fatal(err)
	}
	if err := x.c.Finish(t.Context(), 1, step, result); err != nil {
		t.Fatal(err)
	}
	denied := &workflowDeniedTrust695{Repo: x.c}
	x.r.f.Repo = denied
	// Poll applies the normal durable refused-effect handling around publication.
	_ = x.r.f.Poll(t.Context())
	if denied.pushes != 1 {
		t.Fatalf("publication did not attempt the workflow push: %d", denied.pushes)
	}
	if len(x.g.prs) != 0 || len(x.g.calls) != 0 || len(x.g.merged) != 0 {
		t.Fatal("denied publication reported ready or merged work")
	}
	if !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
		t.Error("workflow permission failure was not handed to a person")
	}
	visible := strings.ToLower(retry263Visible(x.r.gh.last(1).Comment.Body))
	for _, word := range []string{"workflow", "permission", "credential", "retry"} {
		if !strings.Contains(visible, word) {
			t.Errorf("handoff lacks actionable %q guidance: %s", word, visible)
		}
	}
	_ = x.r.f.Poll(t.Context())
	if denied.pushes != 1 {
		t.Error("permission failure was retried without a person's command")
	}
}
