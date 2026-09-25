package factory

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/github"
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
	m := commentURL.FindStringSubmatch(r.Comment)
	if m == nil {
		return fmt.Errorf("the ratification names %q, which isn't an issue comment's URL", r.Comment)
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
	case c.Issue() != r.Issue || m[2] != strconv.Itoa(r.Issue):
		return fmt.Errorf("the ratifying comment is on #%d, not #%d", c.Issue(), r.Issue)
	case c.User.Login != r.By:
		return fmt.Errorf("the ratifying comment is by @%s, not @%s", c.User.Login, r.By)
	case c.User.Type == "Bot":
		return fmt.Errorf("the ratifying comment is by a bot, @%s, and only people ratify", c.User.Login)
	}
	if _, factory := DecodeMarker(c.Body); factory {
		return fmt.Errorf("the ratifying comment is the factory's own")
	}
	perm, err := gh.Permission(ctx, r.By)
	if err != nil {
		return err
	}
	if perm != "admin" && perm != "maintain" && perm != "write" {
		return fmt.Errorf("@%s has %s access, and only people with write access can ratify", r.By, perm)
	}
	for _, cmd := range ParseCommands(c.Body) {
		if cmd.Verb == Ratify && matches(cmd.Args, r.Proposal) {
			return nil
		}
	}
	return fmt.Errorf("the comment doesn't ratify proposal %s", short(r.Proposal))
}

// Synthesis builds with a coding agent, starting from the model drafted
// with the statements.
type Synthesis struct {
	Options synth.Options
}

func (s Synthesis) Build(ctx context.Context, dir, out string) (*synth.Result, error) {
	o := s.Options
	o.Project, o.Out, o.KeepModel = dir, out, true
	return synth.Synthesize(ctx, o)
}
