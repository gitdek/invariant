package factory

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

// Comments is where a ratification's comment is looked up.
type Comments interface {
	Comment(ctx context.Context, id int64) (github.Comment, error)
	Permission(ctx context.Context, login string) (string, error)
}

var commentURL = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/issues/(\d+)#issuecomment-(\d+)$`)

// VerifyRatification checks a factory project's ratification against
// GitHub. The lock must still hold the proposal that was ratified, and the
// comment it names must be in repo, on the recorded issue, by the recorded
// person, who has write access, ratifying exactly that proposal.
func VerifyRatification(ctx context.Context, gh Comments, repo string, lock project.Lock) error {
	r := lock.Ratified
	if r == nil {
		return fmt.Errorf("the lock has no ratification record")
	}
	if got := project.ProposalHash(lock.Bounds, lock.Statements); got != r.Proposal {
		return fmt.Errorf("the lock holds proposal %s, not the ratified %s", short(got), short(r.Proposal))
	}
	return verifyComment(ctx, gh, repo, r.By, r.Issue, r.Comment, r.Proposal)
}

// VerifyPlan checks a plumbing plan's ratification against GitHub, as
// VerifyRatification does a project's (D-0105): the record holds the plan
// that was ratified, and a person with write access ratified exactly it on
// the recorded issue.
func VerifyPlan(ctx context.Context, gh Comments, repo string, lock plumbing.Lock) error {
	r := lock.Ratified
	if got := lock.Plan.Hash(); got != r.Proposal {
		return fmt.Errorf("the record holds plan %s, not the ratified %s", short(got), short(r.Proposal))
	}
	return verifyComment(ctx, gh, repo, r.By, r.Issue, r.Comment, r.Proposal)
}

// verifyComment checks that the comment at url, in repo, on issue, is by,
// a person with write access, ratifying exactly proposal.
func verifyComment(ctx context.Context, gh Comments, repo, by string, issue int, url, proposal string) error {
	m := commentURL.FindStringSubmatch(url)
	if m == nil {
		return fmt.Errorf("the ratification names %q, which isn't an issue comment's URL", url)
	}
	if !strings.EqualFold(m[1], repo) {
		return fmt.Errorf("the ratifying comment is in %s, not %s", m[1], repo)
	}
	id, _ := strconv.ParseInt(m[3], 10, 64)
	c, err := gh.Comment(ctx, id)
	if err != nil {
		return fmt.Errorf("the ratifying comment can't be read: %w", err)
	}
	switch {
	case c.Issue() != issue || m[2] != strconv.Itoa(issue):
		return fmt.Errorf("the ratifying comment is on #%d, not #%d", c.Issue(), issue)
	case c.User.Login != by:
		return fmt.Errorf("the ratifying comment is by @%s, not @%s", c.User.Login, by)
	case c.User.Type == "Bot":
		return fmt.Errorf("the ratifying comment is by a bot, @%s, and only people ratify", c.User.Login)
	}
	if _, factory := DecodeMarker(c.Body); factory {
		return fmt.Errorf("the ratifying comment is the factory's own")
	}
	perm, err := gh.Permission(ctx, by)
	if err != nil {
		return err
	}
	if perm != "admin" && perm != "maintain" && perm != "write" {
		return fmt.Errorf("@%s has %s access, and only people with write access can ratify", by, perm)
	}
	for _, cmd := range ParseCommands(c.Body) {
		if cmd.Verb == Ratify && matches(cmd.Args, proposal) {
			return nil
		}
	}
	return fmt.Errorf("the comment doesn't ratify proposal %s", short(proposal))
}

// Synthesis builds with a coding agent, starting from the model drafted
// with the statements.
type Synthesis struct {
	Options synth.Options
}

func (s Synthesis) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	o := s.Options
	o.Project, o.Out, o.KeepModel, o.KeepCode = dir, out, true, amend
	res, err := synth.Synthesize(ctx, o)
	if err != nil || res.Final == nil || !res.Final.Passed {
		return res, err
	}
	// A second agent reads the driver, with fresh context, and the factory
	// posts what it says with the pull request (D-0082, D-0086). A review
	// that fails to run takes nothing away from the build.
	if review, err := synth.ReviewDriver(ctx, o.Backend, res.Dir, synth.ReviewTranscript(out)); err == nil {
		res.Review = review
	} else {
		res.Review = &synth.Review{Text: "The review didn't run: " + err.Error()}
	}
	return res, nil
}
