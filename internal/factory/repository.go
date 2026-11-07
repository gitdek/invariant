package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

// repositoryAuthority is a setup or compatibility refusal, distinct from a
// failed read that a later poll can retry. It never authorizes an agent run.
type repositoryAuthority struct{ error }

func (e repositoryAuthority) Unwrap() error { return e.error }

type committedGates map[string]string

func (g committedGates) Workflows(context.Context, string) (map[string]string, error) {
	return g, nil
}

// Watch's setup already requires merge commits. Per-proposal checks use the
// clone's committed workflow bytes rather than optional GitHub client methods.
func (committedGates) MergeCommits(context.Context) (bool, error) { return true, nil }

func (f *Factory) repositoryGates(ctx context.Context, base string) (map[string]string, error) {
	workflows, err := f.Repo.Workflows(ctx, base)
	if err != nil {
		return nil, err
	}
	if err := setup.CheckRepository(ctx, committedGates(workflows), f.Repository, f.Base, true); err != nil {
		return nil, repositoryAuthority{err}
	}
	return setup.RepositoryGates(workflows), nil
}

func (f *Factory) repositoryAt(ctx context.Context, base string) (*repository.Snapshot, error) {
	snapshot, err := f.Repo.Repository(ctx, base)
	if errors.Is(err, repository.ErrMissing) || errors.Is(err, repository.ErrInvalid) {
		return nil, repositoryAuthority{fmt.Errorf("%s on base %s is missing or invalid: %w. Review and commit the corrected contract on the base branch", repository.Path, base, err)}
	}
	return snapshot, err
}

// planRepository reconstructs the proposal's original authority from its
// execution base, including on revision or restart. Compatible base movement
// never replaces captured guidance, commands, dependencies or native identity.
// nativeAuthority is the proposal whose native plan holds the repository
// authority a draft keeps: the previous proposal, when it has a native plan,
// or else the latest native plan the factory posted on the issue. A
// question, or a failed draft, carries no plan, and nor does an answered
// question's carrier, so without this the next draft would capture the
// current base and adopt a gate or guidance change made meanwhile. The
// issue's posts outlast a restart, so the original authority does too.
func nativeAuthority(t Thread, previous *formalize.Proposal) *formalize.Proposal {
	if previous != nil && previous.Plan != nil && previous.Plan.Native() {
		return previous
	}
	for i := len(t.Posts) - 1; i >= 0; i-- {
		if p := t.Posts[i].Marker.Proposal; p != nil && p.Plan != nil && p.Plan.Native() {
			return p
		}
	}
	return nil
}

func (f *Factory) planRepository(ctx context.Context, plan *plumbing.Plan) (*repository.Snapshot, error) {
	if plan == nil || !plan.Native() {
		return nil, nil
	}
	if err := plan.Validate(); err != nil {
		return nil, repositoryAuthority{err}
	}
	if plan.Execution == nil {
		return nil, repositoryAuthority{errors.New("the native plan is missing captured repository execution authority")}
	}
	if err := f.Repo.Fetch(ctx); err != nil {
		return nil, err
	}
	captured, err := f.repositoryAt(ctx, plan.Execution.Base)
	if err != nil {
		return nil, err
	}
	if err := plan.ValidateRepository(ctx, captured); err != nil {
		return nil, repositoryAuthority{fmt.Errorf("captured %s changed or differs from the plan: %w", repository.Path, err)}
	}
	gates, err := f.repositoryGates(ctx, captured.Commit())
	if err != nil {
		return nil, err
	}
	current, err := f.repositoryAt(ctx, "origin/"+f.Base)
	if err != nil {
		return nil, err
	}
	if err := plan.ValidateRepository(ctx, current); err != nil {
		return nil, repositoryAuthority{fmt.Errorf("%s on the current base changed or differs from the captured contract: %w", repository.Path, err)}
	}
	now, err := f.repositoryGates(ctx, current.Commit())
	if err != nil {
		return nil, err
	}
	if !maps.Equal(gates, now) {
		return nil, repositoryAuthority{errors.New("the committed invariant/gate authority on the current base changed, including its workflow or Invariant commit pin")}
	}
	return captured, nil
}

// Execution.Base is provenance outside the plan hash. A candidate lock must
// not substitute another base to hide a changed gate or replace captured rules.
func (f *Factory) lockedRepository(ctx context.Context, m Marker, plan *plumbing.Plan) (*repository.Snapshot, error) {
	if m.Proposal != nil && m.Proposal.Plan != nil && m.Proposal.Plan.Native() {
		original := m.Proposal.Plan
		if original.Execution == nil || plan.Execution == nil || original.Execution.Base != plan.Execution.Base || plan.Hash() != m.Hash {
			return nil, repositoryAuthority{errors.New("the plan's captured repository base or identity changed in its committed lock")}
		}
	}
	return f.planRepository(ctx, plan)
}

func (f *Factory) publicationRepository(ctx context.Context, m Marker, lock *plumbing.Lock) error {
	if lock == nil {
		return nil // The protocol's lock check reports an unreadable lock.
	}
	_, err := f.lockedRepository(ctx, m, &lock.Plan)
	var authority repositoryAuthority
	if errors.As(err, &authority) {
		return repositoryAuthority{fmt.Errorf("%w: %v", errOutsideProtocol, err)}
	}
	return err
}

// repositoryHold is an observation around the proved protocol, like a refused
// publication effect. It preserves the original working state and run record.
// A finished build is never loaded while the hold waits for a writer's retry.
type repositoryHold struct {
	Reason string `json:"reason"`
	Label  string `json:"label"`
}

func repositoryHoldName(n int) string { return fmt.Sprintf("repository-hold-%d", n) }

// A fully reported workflow credential refusal needs no repeated label write.
// Partial reports and recorded retries still go through effects' recovery.
func (f *Factory) workflowPublicationHeld(ctx context.Context, t Thread) (bool, error) {
	if !t.Issue.HasLabel(LabelHumanReview) {
		return false, nil
	}
	for i := len(t.Posts) - 1; i >= 0; i-- {
		p := t.Posts[i]
		key := p.Marker.Refusal
		if key == "" {
			continue
		}
		if !strings.HasPrefix(key, "effect-") || strings.HasSuffix(key, "-retry") {
			return false, nil
		}
		state, _, err := f.Repo.Run(ctx, t.Issue.Number, key)
		if err != nil || state != RunRecorded {
			return false, err
		}
		data, err := f.Repo.Recorded(ctx, t.Issue.Number, key)
		if err != nil {
			return false, err
		}
		var refused refusedEffect
		if err := json.Unmarshal([]byte(data), &refused); err != nil {
			return false, err
		}
		if refused.Body != "" || refused.Status != nil || !workflowPermission(refused.Message) {
			return false, nil
		}
		data, err = f.Repo.Recorded(ctx, t.Issue.Number, key+"-retry")
		if err != nil || data != "" {
			return false, err
		}
		for _, c := range t.Pending() {
			if c.Verb == Retry && c.Comment > p.Comment.ID {
				return false, nil
			}
		}
		return true, nil
	}
	return false, nil
}

func (f *Factory) holdRepository(ctx context.Context, t Thread, problem error, label string) error {
	var authority repositoryAuthority
	if !errors.As(problem, &authority) {
		return problem
	}
	// Direct callers without the step's effect context still get the refusal;
	// only an authorized watcher step may record and report a durable hold.
	if ctx.Value(effectsKey{}) == nil {
		return problem
	}
	for i := 0; ; i++ {
		key := repositoryHoldName(i)
		state, _, err := f.Repo.Run(ctx, t.Issue.Number, key)
		if err != nil {
			return err
		}
		if state == RunDone {
			continue
		}
		hold := repositoryHold{Reason: problem.Error(), Label: label}
		if state == RunNone {
			if err := f.mayAct(ctx); err != nil {
				return err
			}
			data, _ := json.Marshal(hold)
			if err := f.Repo.Record(ctx, t.Issue.Number, key, string(data)); err != nil {
				return err
			}
		}
		_, err = f.repositoryHolds(ctx, t)
		return err
	}
}

// repositoryHolds completes interrupted reports and authorized retries using
// only small records. Idle polls make no writes once the hold is visible.
func (f *Factory) repositoryHolds(ctx context.Context, t Thread) (bool, error) {
	n := t.Issue.Number
	for i := 0; ; i++ {
		key := repositoryHoldName(i)
		state, _, err := f.Repo.Run(ctx, n, key)
		if err != nil || state == RunNone {
			return false, err
		}
		if state == RunDone {
			continue
		}
		data, err := f.Repo.Recorded(ctx, n, key)
		if err != nil {
			return true, err
		}
		var hold repositoryHold
		if err := json.Unmarshal([]byte(data), &hold); err != nil {
			return true, err
		}
		var report int64
		for _, p := range t.Posts {
			if p.Marker.Refusal == key {
				report = p.Comment.ID
			}
		}
		if report == 0 {
			if err := f.mayAct(ctx); err != nil {
				return true, err
			}
			m := Marker{Kind: KindNote, Refusal: key}
			p, err := f.GitHub.PostComment(ctx, n, post("repository authority needs a person", hold.Reason, m,
				"\n\nThe recorded work is preserved. Review and commit the original contract and capable gate on the base branch. Restoring authority alone does not resume work: a person with write access must then comment `/invariant retry`. A finished build resumes publication without rebuilding."))
			if err != nil {
				return true, err
			}
			report = p.ID
		}
		if !t.Issue.HasLabel(LabelHumanReview) {
			if err := f.status(ctx, n, LabelHumanReview); err != nil {
				return true, err
			}
		}
		retryKey := key + "-retry"
		data, err = f.Repo.Recorded(ctx, n, retryKey)
		if err != nil {
			return true, err
		}
		var retry Command
		if data != "" {
			if err := json.Unmarshal([]byte(data), &retry); err != nil {
				return true, err
			}
		} else {
			for _, c := range t.Pending() {
				if c.Verb == Retry && c.Comment > report {
					retry = c
					break
				}
			}
			if retry.Verb == "" {
				return true, nil
			}
			if err := f.mayAct(ctx); err != nil {
				return true, err
			}
			data, _ := json.Marshal(retry)
			if err := f.Repo.Record(ctx, n, retryKey, string(data)); err != nil {
				return true, err
			}
		}
		answered := false
		for _, p := range t.Posts {
			answered = answered || p.Marker.Refusal == retryKey
		}
		if !answered {
			if err := f.mayAct(ctx); err != nil {
				return true, err
			}
			m := Marker{Kind: KindNote, Refusal: retryKey, ReplyTo: []int64{retry.Comment}}
			if _, err := f.GitHub.PostComment(ctx, n, post("retrying", "Rechecking the committed base authority before resuming the recorded work.", m)); err != nil {
				return true, err
			}
		}
		if err := f.status(ctx, n, hold.Label); err != nil {
			return true, err
		}
		if err := f.finishEffect(ctx, n, key); err != nil {
			return true, err
		}
		return false, nil
	}
}
