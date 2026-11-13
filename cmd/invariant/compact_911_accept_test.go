package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// watchWithFactory is the command's shared flag parsing and configuration
// entry. watchCmd delegates to it with nil for ordinary startup; an injected
// start receives the configured Factory after validation and before external
// setup. This lets the real flags feed a real publication without credentials,
// a downloaded toolchain, an agent or a running dashboard.
func TestCompact911WatchDashboardURL(t *testing.T) {
	code, help := agentCLI(t, "watch", "-h")
	flagHelp := regexp.MustCompile(`(?m)^  -dashboard-url string\n[^\n]*`).FindString(help)
	if code != 0 || flagHelp == "" || strings.Contains(flagHelp, "(default ") || strings.Contains(flagHelp, "https://invariant.puglisij.com") {
		t.Fatalf("watch must document its optional public dashboard URL with no deployment default (exit %d):\n%s", code, help)
	}
	bin := t.TempDir()
	const reached = "compact911-first-external-call"
	script := "#!/bin/sh\nprintf '%s\\n' '" + reached + "' >&2\nexit 97\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "fixture-911")
	for _, base := range []string{"/relative", "dashboard.example", "//dashboard.example/path", "ftp://dashboard.example", "https:///path", "https://user:secret@dashboard.example", "https://user@dashboard.example", "https://dashboard.example?q=1", "https://dashboard.example?", "https://dashboard.example/#fragment", "https://dashboard.example/#", "https://dashboard.example/%zz"} {
		code, out := agentCLI(t, "watch", "-repo", "owner/saved", "-dashboard-url", base, "-work", t.TempDir())
		if code != 2 || strings.Contains(out, reached) || !strings.Contains(out, "dashboard-url") || strings.Contains(out, "flag provided but not defined") {
			t.Errorf("invalid dashboard base %q did not fail validation before work: exit=%d, %s", base, code, out)
		}
	}
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", (4<<20)+1024)+"\n====", 1))
	p, report := reader603Checked(t, ws, tc)
	result := &formalize.Result{Proposal: p, Report: report, CheckContext: &formalize.CheckContext{}}
	var record map[string]any
	if err := json.Unmarshal(reader603JSON(t, result), &record); err != nil {
		t.Fatal(err)
	}
	record["publication"] = map[string]any{"agent": "codex"}
	payload := reader603JSON(t, record)
	commit := strings.Repeat("b", 40)
	// The link builder is pure and escapes each path component independently.
	// It must not treat repository text as a URL or a relative path to clean.
	refForURL := factory.DraftReference{Version: 1, Repository: "owner & team/repo?#%", Issue: 911, Commit: commit, SHA256: "sha256:" + strings.Repeat("d", 64), ProposalHash: p.Hash}
	linker, ok := any(refForURL).(interface{ URL(string) (string, error) })
	if !ok {
		t.Fatal("saved references need a deterministic URL(base) link builder")
	}
	for _, base := range []string{"https://example.test/prefix", "https://example.test/prefix/"} {
		want := "https://example.test/prefix/draft/owner%20&%20team/repo%3F%23%25/911/" + commit + "/" + strings.Repeat("d", 64)
		got, err := linker.URL(base)
		if err != nil || got != want {
			t.Fatalf("escaped evidence URL=%q, want %q: %v", got, want, err)
		}
		again, err := linker.URL(base)
		if err != nil || again != got {
			t.Fatal("evidence URL generation is not deterministic")
		}
	}
	for _, c := range []struct {
		name, base string
		explicit   bool
	}{
		{name: "unset"},
		{name: "explicitly empty", explicit: true},
		{name: "explicit deployment", base: "https://invariant.puglisij.com", explicit: true},
		{name: "escaped path prefix", base: "https://dashboard.example/install%20one/nested/", explicit: true},
		{name: "local HTTP prefix", base: "http://127.0.0.1:9999/prefix", explicit: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			base := c.base
			gh := &compact911WatchGitHub{issue: github.Issue{Number: 911, State: "open", Title: "Recovered modeled draft", Body: "/invariant solve", User: github.User{Login: "owner"}, CreatedAt: "2026-11-12T12:00:00Z"}}
			repo := compact911WatchRepo{commit: commit, payload: payload}
			args := []string{"-repo", "owner/saved", "-once", "-lease", "0", "-work", t.TempDir()}
			if c.explicit {
				args = append(args, "-dashboard-url", base)
			}
			starts := 0
			code := watchWithFactory(t.Context(), args, func(ctx context.Context, f *factory.Factory) int {
				starts++
				if f.Repository != "owner/saved" {
					t.Fatal("prepared watcher lost its repository flag")
				}
				f.GitHub, f.Repo, f.Self = gh, repo, "factory[bot]"
				f.Parallel = 0
				if err := f.Poll(ctx); err != nil {
					t.Fatal(err)
				}
				return 0
			})
			if code != 0 || starts != 1 || len(gh.comments) != 1 {
				t.Fatalf("watch did not publish one recovered draft: exit %d starts %d comments %d", code, starts, len(gh.comments))
			}
			body := gh.comments[0].Body
			m, ok := factory.DecodeMarker(body)
			if base == "" {
				if !ok || m.Kind != factory.KindStuck || len(body) > 60000 || strings.Contains(body, "/invariant ratify ") || strings.Contains(body, "/invariant choose ") || strings.Contains(body, "](") || strings.Contains(body, "```mermaid") || !strings.Contains(strings.ToLower(body), "size") {
					t.Fatal("watch without a dashboard must keep overflow refused without an actionable decision or evidence link")
				}
				return
			}
			if !ok || m.Kind != factory.KindProposal || m.ProposalRef == nil || len(body) > 60000 {
				t.Fatal("watcher did not publish a bounded compact modeled comment")
			}
			var ref factory.DraftReference
			if err := json.Unmarshal(m.ProposalRef, &ref); err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(payload))
			want := strings.TrimRight(base, "/") + "/draft/owner/saved/911/" + commit + "/" + digest
			if !strings.Contains(body, "]("+want+")") || ref.SHA256 != "sha256:"+digest || ref.ProposalHash != p.Hash {
				t.Fatalf("actual watcher flags did not produce the exact deterministic saved-record link %s", want)
			}
			_, got, err := m.ResolveDraft(t.Context(), "owner/saved", 911, repo.Run, repo.Show)
			if err != nil || got == nil || string(reader603JSON(t, got.Proposal)) != string(reader603JSON(t, p)) {
				t.Fatalf("watcher changed the complete checked record: %v", err)
			}
		})
	}
}

type compact911WatchRepo struct {
	factory.Repo
	commit  string
	payload []byte
}

func (r compact911WatchRepo) Run(_ context.Context, n int, step string) (factory.RunState, string, error) {
	if n == 911 && step == "draft-issue" {
		return factory.RunDone, r.commit, nil
	}
	return factory.RunNone, "", nil
}
func (r compact911WatchRepo) Show(_ context.Context, ref, path string) ([]byte, error) {
	if ref != r.commit || path != "result.json" {
		return nil, fmt.Errorf("unexpected saved-record read %s:%s", ref, path)
	}
	return append([]byte(nil), r.payload...), nil
}

type compact911WatchGitHub struct {
	factory.GitHub
	issue    github.Issue
	comments []github.Comment
}

func (g *compact911WatchGitHub) OpenIssues(context.Context) ([]github.Issue, error) {
	return []github.Issue{g.issue}, nil
}
func (g *compact911WatchGitHub) ClosedIssues(context.Context, string) ([]github.Issue, error) {
	return nil, nil
}
func (g *compact911WatchGitHub) MergedPullRequests(context.Context) ([]github.PullRequest, error) {
	return nil, nil
}
func (g *compact911WatchGitHub) Issue(context.Context, int) (github.Issue, error) {
	return g.issue, nil
}
func (g *compact911WatchGitHub) Comments(context.Context, int) ([]github.Comment, error) {
	return g.comments, nil
}
func (g *compact911WatchGitHub) Events(context.Context, int) ([]github.Event, error) { return nil, nil }
func (g *compact911WatchGitHub) Permission(context.Context, string) (string, error) {
	return "admin", nil
}
func (g *compact911WatchGitHub) AddLabels(_ context.Context, _ int, labels ...string) error {
	for _, l := range labels {
		g.issue.Labels = append(g.issue.Labels, github.Label{Name: l})
	}
	return nil
}
func (g *compact911WatchGitHub) RemoveLabel(_ context.Context, _ int, label string) error {
	var keep []github.Label
	for _, l := range g.issue.Labels {
		if l.Name != label {
			keep = append(keep, l)
		}
	}
	g.issue.Labels = keep
	return nil
}
func (g *compact911WatchGitHub) PostComment(_ context.Context, n int, body string) (github.Comment, error) {
	c := github.Comment{ID: 911, User: github.User{Login: "factory[bot]", Type: "Bot"}, Body: body, URL: fmt.Sprintf("https://github.com/owner/saved/issues/%d#issuecomment-911", n), CreatedAt: "2026-11-12T12:00:01Z"}
	g.comments = append(g.comments, c)
	return c, nil
}
