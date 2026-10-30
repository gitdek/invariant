package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// Retry copy is a served presentation contract, shared by the public card
// and /act. These tests exercise real marker decoding, lane construction,
// snapshot publication and HTTP responses; they do not inspect JS source.
// The implementation must render this copy on both pages, preserve Takes
// as the authority for offered commands, and send /invariant retry unchanged.
func TestRetryCardsDescribeTheExistingAction(t *testing.T) {
	for _, c := range retry263Cases() {
		check := func(t *testing.T) {
			s, _, _ := retry263Server(t, c, nil)
			is := retry263State(t, s.Handler())
			w := is.Waiting
			if w == nil || w.PR != c.marker.PR || w.Failure != c.failure || !slices.Equal(w.Takes, c.takes) {
				t.Fatalf("card changed the failure or commands: %+v; want PR %d, failure %q, takes %q", w, c.marker.PR, c.failure, c.takes)
			}
			if !slices.Contains(c.takes, factory.Retry) {
				if w.Retry != nil {
					t.Fatalf("card offers retry copy where the factory refuses retry: %+v", w.Retry)
				}
				return
			}
			if w.Retry == nil {
				t.Fatal("card has no retry label, confirmation or guidance in the served snapshot")
			}
			label, confirmation, _ := retry263Copy(c)
			if w.Retry.Label != label || w.Retry.Confirmation != confirmation {
				t.Errorf("retry label/confirmation = %q / %q; want %q / %q", w.Retry.Label, w.Retry.Confirmation, label, confirmation)
			}
			guidance := strings.ToLower(w.Retry.Guidance)
			if c.marker.PR == 0 {
				for _, want := range []string{"build", "ratified proposal", "again"} {
					if !strings.Contains(guidance, want) {
						t.Errorf("no-PR guidance %q does not say %q", w.Retry.Guidance, want)
					}
				}
				if strings.Contains(guidance, "close #") || strings.Contains(guidance, "watch #") {
					t.Errorf("no-PR guidance invents a pull request prerequisite: %q", w.Retry.Guidance)
				}
				return
			}
			for _, want := range []string{fmt.Sprintf("watch #%d again", c.marker.PR), "ci", "gate", "current head", "merge", fmt.Sprintf("won't review #%d again", c.marker.PR), "push", "changes"} {
				if !strings.Contains(guidance, want) {
					t.Errorf("PR guidance %q does not say %q", w.Retry.Guidance, want)
				}
			}
			if retry263Trusted(c) {
				if !strings.Contains(guidance, "a person merges") || !strings.Contains(guidance, "trusted base") {
					t.Errorf("trusted guidance must leave merging to a person: %q", w.Retry.Guidance)
				}
				for _, wrong := range []string{"factory merges", "factory will merge", "review passed", "review approves", "passed ci's gate and the review"} {
					if strings.Contains(guidance, wrong) {
						t.Errorf("trusted guidance makes a false merge/review promise: %q", w.Retry.Guidance)
					}
				}
			} else {
				for _, want := range []string{"ready", "factory", "trusted base", "a person merges"} {
					if !strings.Contains(guidance, want) {
						t.Errorf("guidance must explain ready/merge and the trusted exception: %q", w.Retry.Guidance)
					}
				}
			}
			if !strings.Contains(guidance, "review asked for changes") {
				t.Errorf("guidance must say a rejected review will not prevent merging the current changes: %q", w.Retry.Guidance)
			}
			if c.marker.Proposal != nil && c.marker.Proposal.Plan != nil {
				closeAt := strings.Index(guidance, fmt.Sprintf("close #%d", c.marker.PR))
				draftAt := strings.Index(guidance, "draft the same plan again")
				reviseAt := strings.Index(guidance, "/invariant revise")
				if closeAt < 0 || draftAt <= closeAt || reviseAt <= draftAt || !strings.Contains(guidance[reviseAt+len("/invariant revise"):], "ratif") {
					t.Errorf("plumbing guidance must close the PR, request the same plan, revise, then require ratification: %q", w.Retry.Guidance)
				}
			} else if strings.Contains(guidance, "same plan") || strings.Contains(guidance, "the plan") {
				t.Errorf("a modeled or legacy project is described as a plumbing plan: %q", w.Retry.Guidance)
			}
		}
		if slices.Contains(c.takes, factory.Retry) {
			t.Run(c.name, check)
		} else {
			// Passing eligibility regressions belong to the parent: the plan
			// checker otherwise counts them as passing acceptance tests.
			check(t)
		}
	}
}

// A command's immediate feedback tells the same truth as its card, on the
// authenticated page and through the existing agent door. GitHub receives
// the same command; before a refresh sees it, every public state read shows
// that command, its actual effect and the exact posted-comment link.
func TestPostedRetriesSayWhetherTheyWatchOrBuild(t *testing.T) {
	team := newAccessTeam(t)
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	for _, c := range retry263Cases() {
		if !slices.Contains(c.takes, factory.Retry) {
			continue
		}
		for _, door := range []string{"owner", "agent"} {
			t.Run(c.name+"/"+door, func(t *testing.T) {
				s, original, posted := retry263Server(t, c, team.access())
				h := s.Handler()
				body := atOnceCommand(t, 263, "/invariant retry")
				var rec *httptest.ResponseRecorder
				if door == "owner" {
					rec = togetherSend(h, owner, "/act/api/comment", body, nil)
				} else {
					req := httptest.NewRequest(http.MethodPost, "/act/api/comment", strings.NewReader(body))
					req.Header.Set("Authorization", "Bearer retry-263-token")
					rec = httptest.NewRecorder()
					s.AgentHandler("retry-263-token").ServeHTTP(rec, req)
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("retry HTTP %d: %s", rec.Code, rec.Body.String())
				}
				if len(*posted) != 1 || (*posted)[0].Repo != "o/r" || (*posted)[0].Issue != 263 {
					t.Fatalf("unexpected GitHub posts: %+v", *posted)
				}
				commands := factory.ParseCommands((*posted)[0].Body)
				if len(commands) != 1 || commands[0].Verb != factory.Retry || len(commands[0].Args) != 0 {
					t.Errorf("retry label changed the posted command: %q", (*posted)[0].Body)
				}
				if door == "owner" && (*posted)[0].Body != "/invariant retry" {
					t.Errorf("owner's command changed: %q", (*posted)[0].Body)
				}
				_, _, text := retry263Copy(c)
				for _, when := range []string{"immediately", "another page read", "refresh before GitHub lists the command"} {
					if strings.HasPrefix(when, "refresh") {
						if _, err := s.publish(original); err != nil {
							t.Fatal(err)
						}
					}
					is := retry263State(t, h)
					if is.Waiting != nil || is.Stage != StageQueued || is.Acted == nil {
						t.Fatalf("%s: command taken but issue was not queued and shown acted on: %+v", when, is)
					}
					if is.Acted.Command != "/invariant retry" || is.Acted.Text != text || is.Acted.URL != retry263URL {
						t.Errorf("%s: feedback %+v; want retry, %q and %s", when, is.Acted, text, retry263URL)
					}
				}
				// A stale tab cannot post another retry once this one answered
				// the failure, regardless of its more precise visible label.
				if again := togetherSend(h, owner, "/act/api/comment", body, nil); again.Code != http.StatusBadRequest || len(*posted) != 1 {
					t.Errorf("stale retry was accepted or posted: HTTP %d, posts %+v", again.Code, *posted)
				}
			})
		}
	}
}

// Once GitHub lists a retry, its history entry still says what that command
// requested, using the failure it answered rather than a later PR's state.
func TestRetryHistoryExplainsMergeWithoutAnotherReview(t *testing.T) {
	for _, c := range retry263Cases() {
		if !slices.Contains(c.takes, factory.Retry) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
			is := github.Issue{Number: 263, State: "open", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)}
			comments := []github.Comment{
				{ID: 2629, User: github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}, CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339), Body: atOnceMarker(t, c.marker)},
				{ID: 2630, User: github.User{Login: "gitdek"}, CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339), Body: "/invariant retry"},
				{ID: 2631, User: github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}, CreatedAt: now.Add(-time.Minute).Format(time.RFC3339), Body: atOnceMarker(t, factory.Marker{Kind: factory.KindPR, PR: 900})},
			}
			lane := Lane(is, comments, "invariant-code-factory[bot]", now)
			var text string
			for _, e := range lane.Events {
				if e.Kind == factory.Retry && e.Who == WhoPerson {
					text = strings.ToLower(e.Text)
				}
			}
			if c.marker.PR == 0 {
				if !strings.Contains(text, "build ratified proposal again") || strings.Contains(text, "#900") {
					t.Errorf("no-PR retry history must say build again: %q", text)
				}
				return
			}
			for _, want := range []string{fmt.Sprintf("#%d", c.marker.PR), "merge", "ci", "passes", fmt.Sprintf("won't review #%d again", c.marker.PR)} {
				if !strings.Contains(text, want) {
					t.Errorf("retry history must explain merge without another review (%q): %q", want, text)
				}
			}
			if strings.Contains(text, "#900") {
				t.Errorf("history describes a later PR instead of the failure retried: %q", text)
			}
			if retry263Trusted(c) && !strings.Contains(text, "person") {
				t.Errorf("trusted retry history must say a person merges: %q", text)
			}
		})
	}
}

type retry263Case struct {
	name    string
	marker  factory.Marker
	failure string
	takes   []string
}

func retry263Cases() []retry263Case {
	plan := &formalize.Proposal{Draft: formalize.Draft{Name: "Retry guidance"}, Hash: "sha256:11bff3f218d7aa", Plan: &plumbing.Plan{Name: "Retry guidance"}}
	trusted := &formalize.Proposal{Draft: formalize.Draft{Name: "Retry guidance"}, Hash: "sha256:33bff3f218d7aa", Plan: &plumbing.Plan{Name: "Retry guidance", Files: []string{"internal/factory/page.go"}, Trusted: []string{"internal/factory/page.go"}}}
	project := &formalize.Proposal{Draft: formalize.Draft{Name: "Leases"}, Hash: "sha256:22bff3f218d7aa"}
	failed := func(name, failure string, pr int, p *formalize.Proposal) retry263Case {
		why := failure
		if why == "" {
			why = factory.FailCI
			if pr == 0 {
				why = factory.FailStopped
			}
		}
		takes := []string{factory.Retry}
		if why == factory.FailStopped || why == factory.FailLimit {
			takes = append(takes, factory.Revise)
		}
		return retry263Case{name, factory.Marker{Kind: factory.KindFailed, Failure: failure, PR: pr, Proposal: p}, why, takes}
	}
	return []retry263Case{
		failed("agent error left a draft", factory.FailGate, 217, plan),
		failed("failed tests left a draft", factory.FailGate, 218, plan),
		failed("review asked for changes", factory.FailGate, 219, plan),
		failed("CI failed", factory.FailCI, 220, plan),
		failed("build stopped without a PR", factory.FailStopped, 0, plan),
		failed("stop limit without a PR", factory.FailLimit, 0, plan),
		failed("legacy failure with a PR", "", 221, nil),
		failed("legacy failure without a PR", "", 0, nil),
		failed("trusted base PR", factory.FailTrusted, 222, plan),
		failed("trusted agent error left a draft", factory.FailGate, 227, trusted),
		failed("trusted failed tests left a draft", factory.FailGate, 228, trusted),
		failed("trusted review asked for changes", factory.FailGate, 229, trusted),
		failed("trusted CI failed", factory.FailCI, 230, trusted),
		failed("legacy trusted base PR", factory.FailTrusted, 231, nil),
		failed("unmergeable PR", factory.FailUnmergeable, 223, project),
		failed("GitHub refused the merge", factory.FailMerge, 224, project),
		failed("modeled project failed its gate", factory.FailGate, 225, project),
		{"closed PR", factory.Marker{Kind: factory.KindClosed, PR: 226}, "", []string{factory.Revise}},
		{"draft could not be made", factory.Marker{Kind: factory.KindStuck}, "", []string{factory.Revise}},
		{"proposal awaits ratification", factory.Marker{Kind: factory.KindProposal, Proposal: plan}, "", []string{factory.Ratify, factory.Revise}},
	}
}

func retry263Trusted(c retry263Case) bool {
	return c.failure == factory.FailTrusted || (c.marker.Proposal != nil && c.marker.Proposal.Plan != nil && len(c.marker.Proposal.Plan.Trusted) != 0)
}

func retry263Copy(c retry263Case) (label, confirmation, posted string) {
	pr := c.marker.PR
	if pr != 0 {
		if retry263Trusted(c) {
			return fmt.Sprintf("Have a person merge #%d once CI passes", pr),
				fmt.Sprintf("Have the factory watch #%d again until CI's gate passes on its current head, as you? A person merges #%d because it changes the trusted base. It won't review #%d again.", pr, pr, pr),
				fmt.Sprintf("Asked for a retry. The factory watches #%d again until CI's gate passes on its current head, then waits for a person to merge it. It won't review #%d again.", pr, pr)
		}
		// The marker cannot establish the scope of a later pushed head, so
		// even ordinary/legacy copy retains the human-merge exception.
		const exception = " If it changes the trusted base, a person merges it."
		return fmt.Sprintf("Merge #%d once CI passes", pr),
			fmt.Sprintf("Have the factory merge #%d once CI's gate passes on its current head, as you? It won't review #%d again.", pr, pr) + exception,
			fmt.Sprintf("Asked for a retry. The factory watches #%d again, then marks it ready and merges it once CI's gate passes on its current head. It won't review #%d again.", pr, pr) + exception
	}
	return "Build ratified proposal again", "Have the factory build the ratified proposal again, as you?", "Asked for a retry. The factory builds the ratified proposal again next."
}

const retry263URL = "https://github.com/o/r/issues/263#issuecomment-2630"

func retry263Server(t *testing.T, c retry263Case, access *Access) (*Server, Snapshot, *[]act) {
	t.Helper()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	opened, posted := now.Add(-time.Hour).Format(time.RFC3339), now.Add(-time.Minute).Format(time.RFC3339)
	is := github.Issue{Number: 263, Title: c.name, State: "open", CreatedAt: opened, Labels: []github.Label{{Name: factory.LabelHumanReview}}}
	comments := []github.Comment{{ID: 2629, User: github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}, CreatedAt: posted, Body: atOnceMarker(t, c.marker)}}
	lane := Lane(is, comments, "invariant-code-factory[bot]", now)
	lane.Repo = "o/r"
	snapshot := Snapshot{Repo: "o/r", GeneratedAt: now, Issues: []Issue{lane}}
	var commands []act
	s := &Server{Repos: []*Repo{{Name: "o/r"}}, Access: access, Log: t.Logf,
		Post: func(_ context.Context, repo string, issue int, body string) (string, error) {
			commands = append(commands, act{Repo: repo, Issue: issue, Body: body})
			return retry263URL, nil
		}}
	if _, err := s.publish(snapshot); err != nil {
		t.Fatal(err)
	}
	return s, snapshot, &commands
}

type retry263Issue struct {
	Number  int    `json:"number"`
	Stage   string `json:"stage"`
	Waiting *struct {
		PR      int      `json:"pr"`
		Failure string   `json:"failure"`
		Takes   []string `json:"takes"`
		Retry   *struct {
			Label        string `json:"label"`
			Confirmation string `json:"confirmation"`
			Guidance     string `json:"guidance"`
		} `json:"retry"`
	} `json:"waiting"`
	Acted *Acted `json:"acted"`
}

func retry263State(t *testing.T, h http.Handler) retry263Issue {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/state.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET state HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var snapshot struct {
		Issues []retry263Issue `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Issues) != 1 || snapshot.Issues[0].Number != 263 {
		t.Fatalf("unexpected issues in state: %+v", snapshot.Issues)
	}
	return snapshot.Issues[0]
}
