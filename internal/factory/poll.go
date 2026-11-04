package factory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// polling is local to one watcher. Only successful steps acknowledge an
// observation; pending also holds closed work past the discovery watermark.
type polling struct {
	since    string
	observed map[int]string
	pending  map[int]github.Issue
}

func issueVersion(issue github.Issue) string {
	if issue.UpdatedAt == "" {
		return "" // no update timestamp means there is no safe idle shortcut
	}
	b, _ := json.Marshal(issue)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// selectIssues runs with the factory's mu held. Listing an update while a
// step runs leaves that update pending, even if the next list omits it.
func (p *polling) selectIssues(issues []github.Issue, merges map[int]int) []github.Issue {
	if p.observed == nil {
		p.observed = map[int]string{}
		p.pending = map[int]github.Issue{}
	}
	for _, issue := range issues {
		n := issue.Number
		version := issueVersion(issue)
		_, pending := p.pending[n]
		if version == "" || p.observed[n] != version || pending || merges[n] != 0 {
			if issue.State == "" && pending {
				continue // a merge's placeholder must not replace an observed update
			}
			issue.Labels = slices.Clone(issue.Labels)
			p.pending[n] = issue
		}
	}
	work := make([]github.Issue, 0, len(p.pending))
	for _, issue := range p.pending {
		work = append(work, issue)
	}
	return work
}

type pollStepKey struct{}
type pollContinuation struct{ again, gone, unlisted bool }

func (f *Factory) pollStep(ctx context.Context, issue github.Issue) error {
	n := issue.Number
	version := issueVersion(issue)
	startedAt := f.now()
	next := &pollContinuation{again: true, unlisted: issue.State == ""}
	err := f.safeStep(context.WithValue(ctx, pollStepKey{}, next), n)
	if err != nil && !next.gone {
		return err
	}
	// GitHub timestamps have one-second resolution. Recheck recent buckets
	// after a minute's overlap, even if the next poll is much later: a
	// comment edit during this read can leave both timestamp and count alone.
	if updated, err := time.Parse(time.RFC3339, issue.UpdatedAt); err == nil && !startedAt.After(updated.Add(time.Minute)) {
		next.again = true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.poll.observed[n] = version
	if issue, ok := f.poll.pending[n]; ok && !next.again && issueVersion(issue) == version {
		delete(f.poll.pending, n)
	}
	return err
}

// A command or transition can leave more work without another issue
// update. One further read settles its result; external outcomes keep a
// ratified plan or watched pull request eligible on every poll.
func continues(t Thread, state Post) bool {
	if t.Issue.State == "closed" {
		return false
	}
	m := state.Marker
	switch m.Kind {
	case KindPR:
		return true
	case KindRatified:
		if isIssuePlan(m) {
			for _, p := range t.Posts {
				if p.Marker.Kind == KindPlanDone && p.Marker.Hash == m.Proposal.Hash {
					return false
				}
			}
		}
		return true
	case KindFailed:
		if m.PR != 0 {
			return true
		}
	}
	return slices.ContainsFunc(t.Pending(), func(c Command) bool { return c.Verb != Stop })
}
