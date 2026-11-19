package dashboard

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
)

// Which issues are the factory's, as the page shows them (#948). It takes
// an issue a label or a line of its text hands over, as before. It also
// takes an open one once a writer's command hands it over, or once it has
// posted on it or runs a step on it, before it has posted, and a closed one
// it built whose change a pull request merged, which a person finished.

// closedReads is the most closed issues whose comments one refresh reads to
// learn whether the factory built them: issues a merged pull request
// closed. A refresh with no kept reads, as the first after this change,
// reads the rest over the refreshes after it, rather than all at once,
// which GitHub's abuse detection could take for an attack.
const closedReads = 10

// readIssues reads r's issues and pull requests in one list, the comments
// of the issues the page may show, and which of them are the factory's.
// What /act offers to hand over is a writer's open issue that isn't.
func (s *Server) readIssues(ctx context.Context, r *Repo, fail func(string, error)) {
	all, pulls, err := r.GitHub.IssuesAndPulls(ctx)
	if err != nil {
		fail("issues", err)
		return
	}
	login := s.factoryOf(r)
	stepping := s.stepping(r)
	planned := s.plannedIn(r.Name)
	closes := closingPulls(r.Name, pulls)
	var taken, open, closed []github.Issue
	for _, is := range all {
		switch {
		case takes(is):
			taken = append(taken, is)
		case is.State == "open":
			open = append(open, is)
		case is.Comments > 0 && len(closes[is.Number]) > 0:
			closed = append(closed, is)
		}
	}
	fresh := func(is github.Issue) bool {
		c, ok := r.src.comments[is.Number]
		return ok && c.updated == is.UpdatedAt
	}
	read := func(is github.Issue) {
		comments, err := r.GitHub.Comments(ctx, is.Number)
		if err != nil {
			fail(fmt.Sprintf("comments on #%d", is.Number), err)
			return
		}
		// Start makes this cache before its first refresh. A server a test
		// built by hand and didn't prepare gets it here, as file, gateIn and
		// graph make theirs.
		if r.src.comments == nil {
			r.src.comments = map[int]cachedComments{}
		}
		r.src.comments[is.Number] = cachedComments{updated: is.UpdatedAt, comments: comments}
		r.src.changed = true
	}
	for _, is := range append(append([]github.Issue{}, taken...), open...) {
		if !fresh(is) {
			read(is)
		}
	}
	budget := closedReads
	for _, is := range closed {
		if !fresh(is) && budget > 0 {
			budget--
			read(is)
		}
	}
	issues := taken
	r.src.untaken = nil
	for _, is := range open {
		c, ok := r.src.comments[is.Number]
		switch {
		case commanded(c.comments) || took(c.comments, login) || stepping[is.Number] || planned[is.Number]:
			// Handed over, by a writer's command or a label, or worked on:
			// the page shows it from then on, before the factory posts.
			issues = append(issues, is)
		case ok && c.updated == is.UpdatedAt && writer(is.AuthorAssociation):
			r.src.untaken = append(r.src.untaken, is)
		}
	}
	for _, is := range closed {
		if built(r.src.comments[is.Number].comments, login) {
			issues = append(issues, is)
		}
	}
	r.src.issues, r.src.hand = issues, map[int]int{}
	for _, is := range issues {
		if is.State != "closed" || !built(r.src.comments[is.Number].comments, login) {
			continue
		}
		if pr, ok := handMerge(is, closes[is.Number]); ok {
			r.src.hand[is.Number] = pr
		}
	}
}

// stepping is the issues r's followed watcher is running a step on.
func (s *Server) stepping(r *Repo) map[int]bool {
	on := map[int]bool{}
	if st, _ := s.watcherOf(r); st.Running() {
		for _, step := range st.steps() {
			if step.Ended == nil && step.Issue > 0 {
				on[step.Issue] = true
			}
		}
	}
	return on
}

// plannedIn is the issues of repo that /act handed over a moment ago, which
// a read of GitHub may not show the command on yet (#201).
func (s *Server) plannedIn(repo string) map[int]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[int]bool{}
	for k := range s.planned {
		if n, err := strconv.Atoi(strings.TrimPrefix(k, repo+"#")); err == nil && strings.HasPrefix(k, repo+"#") {
			out[n] = true
		}
	}
	return out
}

// took says the factory's own comments on an issue hold one of its posts
// that works on it: anything but a note or a refusal to take it, or a post
// it can't read, on which it has stopped (#896).
func took(comments []github.Comment, login string) bool {
	if login == "" {
		return false
	}
	for _, c := range comments {
		if c.User.Login != login {
			continue
		}
		m, ok, bad := factory.ReadPost(c.Body, true)
		if bad != nil || ok && m.Kind != factory.KindNote && m.Kind != factory.KindUnsupported {
			return true
		}
	}
	return false
}

// built says the factory took an issue and built it: its own comments hold
// a ratification it committed, other than a plan of issues', which builds
// nothing itself.
func built(comments []github.Comment, login string) bool {
	if login == "" {
		return false
	}
	for _, c := range comments {
		if c.User.Login != login {
			continue
		}
		if m, ok, _ := factory.ReadPost(c.Body, true); ok && m.Kind == factory.KindRatified && (m.Proposal == nil || m.Proposal.IssuePlan == nil) {
			return true
		}
	}
	return false
}

// closingWords are GitHub's words for a pull request that closes an issue
// once it merges, as "Closes #8", naming the issue by number, as
// owner/name#8, or by its address.
var closingWords = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?[ \t]+(?:([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#|#|https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/)(\d+)\b`)

// closingPulls is, for each of repo's issues, the merged pull requests whose
// text says they close it.
func closingPulls(repo string, pulls []github.Issue) map[int][]github.Issue {
	out := map[int][]github.Issue{}
	for _, p := range pulls {
		if p.PullRequest == nil || p.PullRequest.MergedAt == "" {
			continue
		}
		seen := map[int]bool{}
		for _, m := range closingWords.FindAllStringSubmatch(p.Body, -1) {
			if other := m[1] + m[2]; other != "" && !strings.EqualFold(other, repo) {
				continue
			}
			n, err := strconv.Atoi(m[3])
			if err != nil || n <= 0 || seen[n] {
				continue
			}
			seen[n] = true
			out[n] = append(out[n], p)
		}
	}
	return out
}

// handMerge is the pull request that carried a closed issue's change: one
// that says it closes the issue and merged as the issue closed, as GitHub
// closes an issue when such a pull request merges into its default branch.
// Of several, it's the one that merged nearest the issue's close.
func handMerge(is github.Issue, closes []github.Issue) (int, bool) {
	closed := parseTime(is.ClosedAt)
	if closed.IsZero() {
		return 0, false
	}
	best, gap := 0, time.Duration(-1)
	for _, p := range closes {
		d := closed.Sub(parseTime(p.PullRequest.MergedAt))
		if d < -time.Minute || d > 5*time.Minute {
			continue
		}
		if d < 0 {
			d = -d
		}
		if gap < 0 || d < gap {
			best, gap = p.Number, d
		}
	}
	return best, gap >= 0
}

// handOver marks a closed issue's lane merged, with the pull request that
// carried its change, once a person has finished what the factory built
// and merged it (#948). The factory's own merges stay as it recorded them,
// and a pull request that merged into another branch than the factory's
// isn't one.
func (s *Server) handOver(r *Repo, l *Issue) {
	pr, ok := r.src.hand[l.Number]
	if !ok || l.Open || l.Stage == StageMerged && l.PR != 0 {
		return
	}
	if m, read := r.src.merges[pr]; read && m.base != "" && m.base != s.Branch {
		return
	}
	l.Stage, l.PR, l.ByHand = StageMerged, pr, true
}

// planSteps says, on each plan's issue among one repository's lanes, how
// its open step stands: that step's issue's own stage, and what it waits on
// a person for. A plan's issue waits on its step, not in a queue (#948).
func planSteps(lanes []Issue) {
	byNumber := map[int]*Issue{}
	for i := range lanes {
		byNumber[lanes[i].Number] = &lanes[i]
	}
	for i := range lanes {
		p := lanes[i].Plan
		if p == nil || p.Issue == 0 {
			continue
		}
		if step := byNumber[p.Issue]; step != nil {
			p.Stage = step.Stage
			if w := step.Waiting; w != nil {
				p.Waiting, p.Failure = w.Kind, w.Failure
			}
		}
	}
}
