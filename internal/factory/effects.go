package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/gitdek/invariant/internal/github"
)

// A refused effect is plumbing around the proved cores. Its record keeps
// the issue stopped even when GitHub won't accept its report or label.
// The original draft, build and merge records stay where they were; a
// writer's retry resumes them, rather than starting another agent run.
type refusedEffect struct {
	Operation string  `json:"operation"`
	Message   string  `json:"message"`
	Label     string  `json:"label,omitempty"`
	Status    *string `json:"status,omitempty"`
	Body      string  `json:"body,omitempty"` // an existing protocol refusal's answer
	PR        int     `json:"pr,omitempty"`   // a draft that also needs the person's label
}

type effectsKey struct{}

type issueEffects struct {
	issue   int
	next    int
	label   string
	retried bool
}

func effectName(n int) string { return fmt.Sprintf("effect-%d", n) }

// effects runs before even a closed issue's cleanup. Every issue is polled,
// so a refusal needs neither a successful post nor a label to be found.
func (f *Factory) effects(ctx context.Context, t Thread) (context.Context, bool, error) {
	e := &issueEffects{issue: t.Issue.Number}
	for _, label := range statusLabels {
		if t.Issue.HasLabel(label) {
			e.label = label
		}
	}
	ctx = context.WithValue(ctx, effectsKey{}, e)
	for ; ; e.next++ {
		key := effectName(e.next)
		state, _, err := f.Repo.Run(ctx, e.issue, key)
		if err != nil {
			return ctx, true, err
		}
		if state == RunNone {
			return ctx, false, nil
		}
		if state == RunDone {
			continue
		}
		// A new refusal while restoring an authorized status supersedes
		// the old hold. Store it first, so a crash can never release both.
		later, _, err := f.Repo.Run(ctx, e.issue, effectName(e.next+1))
		if err != nil {
			return ctx, true, err
		}
		if later != RunNone {
			continue
		}
		text, err := f.Repo.Recorded(ctx, e.issue, key)
		if err != nil {
			return ctx, true, err
		}
		var refused refusedEffect
		if err := json.Unmarshal([]byte(text), &refused); err != nil {
			return ctx, true, err
		}
		report, err := f.reportEffect(ctx, e.issue, key, refused)
		if err != nil {
			return ctx, true, err
		}
		if refused.Body != "" {
			// Once its failure post and labels are durable, the protocol
			// already waits for a writer's retry, or observes a person's
			// merge. Only its unfinished report needs this extra hold.
			if err := f.finishEffect(ctx, e.issue, key); err != nil {
				return ctx, true, err
			}
			e.next++
			e.retried = true
			return ctx, false, nil
		}
		retryKey := key + "-retry"
		text, err = f.Repo.Recorded(ctx, e.issue, retryKey)
		if err != nil {
			return ctx, true, err
		}
		var retry Command
		if text != "" {
			if err := json.Unmarshal([]byte(text), &retry); err != nil {
				return ctx, true, err
			}
		} else {
			for _, c := range t.Pending() {
				if c.Verb == Retry && c.Comment > report.ID {
					retry = c
					break
				}
			}
			if retry.Verb == "" {
				return ctx, true, nil
			}
			b, _ := json.Marshal(retry)
			if err := f.mayAct(ctx); err != nil {
				return ctx, true, err
			}
			if err := f.Repo.Record(ctx, e.issue, retryKey, string(b)); err != nil {
				return ctx, true, err
			}
		}
		posts, err := f.postsOn(ctx, e.issue)
		if err != nil {
			return ctx, true, err
		}
		answered := false
		for _, p := range posts {
			answered = answered || p.Marker.Refusal == retryKey
		}
		if !answered {
			if err := f.mayAct(ctx); err != nil {
				return ctx, true, err
			}
			m := Marker{Kind: KindNote, Refusal: retryKey, ReplyTo: []int64{retry.Comment}}
			if _, err := f.GitHub.PostComment(ctx, e.issue, post("retrying", "Resuming the recorded work after the refused operation.", m)); err != nil {
				return ctx, true, err
			}
		}
		// A status change may have followed a successful final post. Resume
		// that change itself: the thread need not take another business step.
		label := refused.Label
		if refused.Status != nil {
			label = *refused.Status
		}
		e.next++
		if err := f.status(ctx, e.issue, label); err != nil {
			return ctx, true, err
		}
		if err := f.finishEffect(ctx, e.issue, key); err != nil {
			return ctx, true, err
		}
		e.retried = true
		return ctx, refused.Status != nil, nil
	}
}

func (f *Factory) finishEffect(ctx context.Context, issue int, key string) error {
	if err := f.mayAct(ctx); err != nil {
		return err
	}
	result, err := f.saveResult(ctx, struct{}{}, "resumed refused effect", "")
	if err != nil {
		return err
	}
	return f.Repo.Finish(ctx, issue, key, result)
}

// effect keeps only definitive GitHub refusals. A local failure, lost
// lease, canceled step or transient response keeps its existing handling.
func (f *Factory) effect(ctx context.Context, issue int, operation string, answer error, status *string) error {
	if !permanentEffect(answer) {
		return answer
	}
	var held effectHeld
	if errors.As(answer, &held) {
		return answer
	}
	e, ok := ctx.Value(effectsKey{}).(*issueEffects)
	if !ok || e == nil || e.issue != issue {
		return answer
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.mayAct(ctx); err != nil {
		return err
	}
	refused := refusedEffect{Operation: operation, Message: answer.Error(), Label: e.label, Status: status}
	b, _ := json.Marshal(refused)
	key := effectName(e.next)
	if err := f.Repo.Record(ctx, issue, key, string(b)); err != nil {
		return errors.Join(answer, err)
	}
	_, err := f.reportEffect(ctx, issue, key, refused)
	return effectHeld{errors.Join(answer, err)}
}

var refusedStatus = regexp.MustCompile(`(?i)\bHTTP 4\d\d\b`)

func permanentEffect(err error) bool {
	return err != nil && !github.Transient(err) &&
		(refusedStatus.MatchString(err.Error()) || errors.Is(err, github.ErrNotFound) || errors.Is(err, github.ErrNoPermission))
}

// The publication and replacement helpers also read GitHub. A failed read
// from the client is deferred, rather than held as a refused write.
func (f *Factory) effectResult(ctx context.Context, issue int, operation string, err error) error {
	if err != nil && strings.HasPrefix(err.Error(), "GET ") {
		return err
	}
	return f.effect(ctx, issue, operation, err, nil)
}

// sayRefused preserves the protocol's existing failure post and retry for
// merges and marking drafts ready, while making its report recoverable.
func (f *Factory) sayRefused(ctx context.Context, issue int, operation string, answer error, m Marker, body func(Marker) string, pr int) error {
	e, ok := ctx.Value(effectsKey{}).(*issueEffects)
	if !ok || e == nil || !permanentEffect(answer) {
		return f.say(ctx, issue, body(m), LabelHumanReview)
	}
	if err := f.mayAct(ctx); err != nil {
		return err
	}
	key := effectName(e.next)
	m.Refusal = key
	refused := refusedEffect{Operation: operation, Message: answer.Error(), Body: body(m), PR: pr}
	b, _ := json.Marshal(refused)
	if err := f.Repo.Record(ctx, issue, key, string(b)); err != nil {
		return err
	}
	_, err := f.reportEffect(ctx, issue, key, refused)
	if err != nil {
		return effectHeld{err}
	}
	if err := f.finishEffect(ctx, issue, key); err != nil {
		return err
	}
	e.next++
	return nil
}

type effectHeld struct{ error }

func (e effectHeld) Unwrap() error { return e.error }

func (f *Factory) reportEffect(ctx context.Context, issue int, key string, refused refusedEffect) (github.Comment, error) {
	posts, err := f.postsOn(ctx, issue)
	if err != nil {
		return github.Comment{}, err
	}
	var report github.Comment
	for _, p := range posts {
		if p.Marker.Refusal == key {
			report = p.Comment
			break
		}
	}
	if report.ID == 0 {
		if err := f.mayAct(ctx); err != nil {
			return report, err
		}
		body := refused.Body
		if body == "" {
			body = post("GitHub refused an operation", fmt.Sprintf("GitHub refused to %s:\n\n%s", refused.Operation, quote(refused.Message, 100)), Marker{Kind: KindNote, Refusal: key},
				"\n\nThe recorded work is preserved. A person with write access can comment `/invariant retry` after fixing the refusal.")
		}
		report, err = f.GitHub.PostComment(ctx, issue, body)
	}
	// Try the label even if the report failed. Neither is the durable hold.
	if leaseErr := f.mayAct(ctx); leaseErr != nil {
		return report, errors.Join(err, leaseErr)
	}
	if refused.PR != 0 {
		if labelErr := f.GitHub.AddLabels(ctx, refused.PR, LabelHumanReview); labelErr != nil {
			return report, errors.Join(err, labelErr)
		}
	}
	if refused.Body != "" {
		// Reporting is retried under the existing hold, including labels.
		// It must not create another refusal for its own failed report.
		ctx = context.WithValue(ctx, effectsKey{}, (*issueEffects)(nil))
		return report, errors.Join(err, f.status(ctx, issue, LabelHumanReview))
	}
	return report, errors.Join(err, f.GitHub.AddLabels(ctx, issue, LabelHumanReview))
}
