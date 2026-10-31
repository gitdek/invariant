package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/project"
)

// JournalRatification is the decision a lock ratified by hand names, as the
// repository's journal records it: the person whose ratify line ratified
// it, when, and the proposal that line names. Problem says why the decision
// doesn't ratify the lock, when it doesn't: it isn't in the journal, it
// isn't ratified, a person never ratified it on a line of their own, or the
// line names another proposal (D-0204, D-0208).
type JournalRatification struct {
	Decision string `json:"decision"`
	By       string `json:"by,omitempty"`
	At       string `json:"at,omitempty"`
	Proposal string `json:"proposal,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

// ratification records what ratified the lock of the project in dir: a
// comment, or, for a lock ratified by hand, the decision it names in the
// journal of the repository dir is in, if that repository keeps one.
func ratification(ctx context.Context, r *Report, dir string, l project.Lock) {
	switch {
	case l.Ratified != nil:
		r.Ratified, r.Proposal = l.Ratified, project.ProposalHash(l.Bounds, l.Statements)
	case l.Decision != "":
		repo, ok := journalOf(ctx, dir)
		if !ok {
			return
		}
		r.Proposal = project.ProposalHash(l.Bounds, l.Statements)
		j := &JournalRatification{Decision: l.Decision}
		rat, err := decisions.RatifiesLock(repo, l.Decision, r.Proposal)
		if rat != nil {
			j.By, j.At, j.Proposal = rat.By, rat.At, rat.Proposal
		}
		if err != nil {
			j.Problem = err.Error()
		}
		r.Journal = j
	}
}

// journalOf is the repository dir is in, if it's a git checkout that keeps
// a decision journal.
func journalOf(ctx context.Context, dir string) (decisions.Repo, bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return decisions.Repo{}, false
	}
	root := strings.TrimSpace(string(out))
	repo := decisions.Repo{Name: filepath.Base(root), Dir: root}
	if info, err := os.Stat(repo.JournalDir()); err != nil || !info.IsDir() {
		return decisions.Repo{}, false
	}
	return repo, true
}
