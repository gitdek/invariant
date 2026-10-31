package decisions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

// A lock ratified by hand names the decision that ratified it, in place of
// a comment on an issue (D-0034, D-0204). It holds only when a person
// ratified that decision with a ratify line of their own that names exactly
// the lock's proposal, and nothing has superseded it since, as a comment
// holds only for the hash it names (D-0208). The journal is the record:
// it says that a person ratified, as the journal records anything, and
// nothing here asks GitHub.

// RatifiesLock checks decision id in the repository's journal against a
// lock ratified by hand, whose ProposalHash is proposal. It returns the
// ratify line whenever the decision has one, even one naming another
// proposal, and an error saying why the decision doesn't ratify the lock,
// if it doesn't.
func RatifiesLock(repo Repo, id, proposal string) (*Ratification, error) {
	if id == "" {
		return nil, errors.New("the lock has no ratification record: no ratifying comment, and no decision")
	}
	d, err := Read(repo, id)
	r := d.Ratification
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("decision %s, which the lock names, isn't in the repository's journal", id)
	case err != nil:
		return nil, fmt.Errorf("decision %s, which the lock names, can't be read: %v", id, err)
	case d.Status == "superseded":
		return r, fmt.Errorf("decision %s, which the lock names, is superseded by %s, so it no longer ratifies the lock", id, d.SupersededBy)
	case d.Status != "ratified":
		return nil, fmt.Errorf("decision %s, which the lock names, is %s: only a decision a person ratified ratifies a lock", id, d.Status)
	case r == nil:
		return nil, fmt.Errorf("decision %s, which the lock names, was recorded as ratified by %s, and no ratify line of theirs names the lock's proposal, %s", id, d.Who, proposal)
	case !strings.HasPrefix(r.By, "@") || r.By != d.Who:
		return nil, fmt.Errorf("decision %s, which the lock names, was ratified by %s, not a person", id, r.By)
	case r.Proposal == "":
		return r, fmt.Errorf("decision %s, which the lock names, records %s's ratification of no proposal in particular, so it doesn't ratify the lock's, %s", id, r.By, proposal)
	case r.Proposal != proposal:
		return r, fmt.Errorf("decision %s, which the lock names, records %s's ratification of %s, and the lock holds %s, so it isn't what %s ratified", id, r.By, r.Proposal, proposal, r.By)
	}
	return r, nil
}

// A HandLock is a project's lock ratified by hand, as a checkout tracks it:
// its path in the checkout, the decision it names and its ProposalHash.
type HandLock struct {
	Path     string
	Decision string
	Proposal string
}

// HandLocks lists the locks ratified by hand that a checkout tracks: every
// project's .invariant/ratified.lock with no ratifying comment.
func HandLocks(ctx context.Context, repo Repo) ([]HandLock, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repo.Dir, "ls-files", "-z", "--", ".invariant/ratified.lock", "*/.invariant/ratified.lock").Output()
	if err != nil {
		return nil, fmt.Errorf("listing %s's locks: %w", repo.Name, err)
	}
	var locks []HandLock
	for _, path := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repo.Dir, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		var l project.Lock
		if err := json.Unmarshal(b, &l); err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		if l.Ratified == nil {
			locks = append(locks, HandLock{Path: path, Decision: l.Decision, Proposal: project.ProposalHash(l.Bounds, l.Statements)})
		}
	}
	return locks, nil
}

// CheckLocks checks every lock ratified by hand that a checkout tracks
// against its journal, as RatifiesLock does, and says what's wrong. n is
// how many it checked.
func CheckLocks(ctx context.Context, repo Repo) (n int, problems []string, err error) {
	locks, err := HandLocks(ctx, repo)
	if err != nil {
		return 0, nil, err
	}
	for _, l := range locks {
		if _, err := RatifiesLock(repo, l.Decision, l.Proposal); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", l.Path, err))
		}
	}
	return len(locks), problems, nil
}
