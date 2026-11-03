package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/gitdek/invariant/internal/github"
)

// A definitive refusal is stored beside the issue's runs before it is
// reported. Polls can find it even when neither a comment nor a label could
// be written. Reports and retries are notes: they do not replace the state
// whose work was refused, or change the protocol and recovery cores.
type effectRefusal struct {
	Operation string        `json:"operation"`
	Message   string        `json:"message"`
	Status    *effectStatus `json:"status,omitempty"`
	Issue     int           `json:"issue,omitempty"`
	Labels    []string      `json:"labels,omitempty"`
	Human     bool          `json:"human,omitempty"`
	Body      string        `json:"body,omitempty"`
	Report    bool          `json:"report,omitempty"`
	PullLabel int           `json:"pull_label,omitempty"`
}

type effectStatus struct {
	Issue int    `json:"issue"`
	Label string `json:"label"`
}

type issueEffects struct {
	f          *Factory
	issue      github.Issue
	next       int
	pending    *effectRefusal
	failed     *effectRefusal
	failedName string
	didStatus  bool
	retrying   *Command
	resumed    bool
}

type effectsKey struct{}
type effectStatusKey struct{}
type bypassEffectsKey struct{}

func effectName(n int) string { return fmt.Sprintf("effect-refusal-%d", n) }

// effectGitHub uses the step's context, so concurrent issues and a fresh
// watcher never share a refusal's state. Inner wrappers are bypassed; the
// lease wrapper still checks every actual write.
type effectGitHub struct{ GitHub }

var refusedStatus = regexp.MustCompile(`(?i)\bHTTP 4\d\d\b`)

func permanentEffect(ctx context.Context, err error) bool {
	return err != nil && fromGitHub(ctx, err) && !github.Transient(err) &&
		(refusedStatus.MatchString(err.Error()) || errors.Is(err, github.ErrNotFound) || errors.Is(err, github.ErrNoPermission))
}

func (f *Factory) effectGitHub() func() {
	if _, ok := f.GitHub.(effectGitHub); ok {
		return func() {}
	}
	previous := f.GitHub
	f.GitHub = effectGitHub{previous}
	return func() {
		// Parallel steps keep using the wrapper after Poll returns. A
		// synchronous caller gets its configured client back when done.
		if f.Parallel <= 1 {
			f.GitHub = previous
		}
	}
}

func bypassEffects(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassEffectsKey{}, true)
}

func (g effectGitHub) refused(ctx context.Context, r effectRefusal, err error) error {
	s, ok := ctx.Value(effectsKey{}).(*issueEffects)
	if !ok || ctx.Value(bypassEffectsKey{}) != nil || !permanentEffect(ctx, err) {
		return err
	}
	if act := s.f.mayAct(ctx); act != nil {
		return errors.Join(err, act)
	}
	r.Message, r.Human = err.Error(), s.issue.HasLabel(LabelHumanReview)
	if s.pending != nil {
		r.Human = s.pending.Human
	}
	if status, ok := ctx.Value(effectStatusKey{}).(*effectStatus); ok && status.Issue == r.Issue {
		r.Status = status
	}
	return errors.Join(err, s.record(ctx, r))
}

func (s *issueEffects) record(ctx context.Context, r effectRefusal) error {
	if err := s.f.mayAct(ctx); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	name := effectName(s.next)
	if err := s.f.Repo.Record(ctx, s.issue.Number, name, string(b)); err != nil {
		return err
	}
	s.failed, s.failedName = &r, name
	s.next++
	return nil
}

func (g effectGitHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	c, err := g.GitHub.PostComment(bypassEffects(ctx), n, body)
	return c, g.refused(ctx, effectRefusal{Operation: "post comment", Issue: n, Body: body}, err)
}

func (g effectGitHub) CreateIssue(ctx context.Context, is github.NewIssue) (github.Issue, error) {
	i, err := g.GitHub.CreateIssue(bypassEffects(ctx), is)
	return i, g.refused(ctx, effectRefusal{Operation: "create issue"}, err)
}

func (g effectGitHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	p, err := g.GitHub.CreatePullRequest(bypassEffects(ctx), pr)
	return p, g.refused(ctx, effectRefusal{Operation: "create pull request"}, err)
}

func (g effectGitHub) ClosePullRequest(ctx context.Context, n int) (github.PullRequest, error) {
	p, err := g.GitHub.ClosePullRequest(bypassEffects(ctx), n)
	return p, g.refused(ctx, effectRefusal{Operation: "close pull request", Issue: n}, err)
}

func (g effectGitHub) AddLabels(ctx context.Context, n int, labels ...string) error {
	err := g.GitHub.AddLabels(bypassEffects(ctx), n, labels...)
	return g.refused(ctx, effectRefusal{Operation: "add label", Issue: n, Labels: labels}, err)
}

func (g effectGitHub) RemoveLabel(ctx context.Context, n int, label string) error {
	err := g.GitHub.RemoveLabel(bypassEffects(ctx), n, label)
	return g.refused(ctx, effectRefusal{Operation: "remove label", Issue: n, Labels: []string{label}}, err)
}

func (g effectGitHub) DeleteBranch(ctx context.Context, branch string) error {
	err := g.GitHub.DeleteBranch(bypassEffects(ctx), branch)
	return g.refused(ctx, effectRefusal{Operation: "delete branch"}, err)
}

// read finds the newest refusal. A later refusal supersedes the one a
// writer retried; a finished record has no work left. The names are a
// sequence per issue, so discovery needs no GitHub label or local cache.
func (s *issueEffects) read(ctx context.Context) error {
	for ; ; s.next++ {
		state, _, err := s.f.Repo.Run(ctx, s.issue.Number, effectName(s.next))
		if err != nil || state == RunNone {
			return err
		}
		s.pending = nil
		if state == RunRecorded {
			what, err := s.f.Repo.Recorded(ctx, s.issue.Number, effectName(s.next))
			if err != nil {
				return err
			}
			var r effectRefusal
			if err := json.Unmarshal([]byte(what), &r); err != nil {
				return err
			}
			s.pending = &r
		}
	}
}

// report retries only reporting, never the refused operation. Adding the
// label is independent of posting: either can fail without hiding the hold.
func (s *issueEffects) report(ctx context.Context, r *effectRefusal, name string) (Post, error) {
	if r.Report {
		return s.resumePost(ctx, r)
	}
	f, n := s.f, s.issue.Number
	if err := f.mayAct(ctx); err != nil {
		return Post{}, err
	}
	ctx = bypassEffects(ctx)
	posts, err := f.postsOn(ctx, n)
	if err != nil {
		return Post{}, err
	}
	var reported Post
	for _, p := range posts {
		if p.Marker.Refusal == name {
			reported = p
			break
		}
	}
	if reported.Comment.ID == 0 {
		m := Marker{Kind: KindNote, Refusal: name}
		c, postErr := f.GitHub.PostComment(ctx, n, effectRefusedComment(r.Operation, r.Message, m))
		err = postErr
		reported = Post{Comment: c, Marker: m}
	}
	labelErr := f.GitHub.AddLabels(ctx, n, LabelHumanReview)
	return reported, errors.Join(err, labelErr)
}

// retry records the writer's new consent before resuming. Its note answers
// only that new command; the command whose work was refused stays pending.
func (s *issueEffects) retry(ctx context.Context, reported Post, name string) (bool, error) {
	f, n := s.f, s.issue.Number
	t, err := f.read(ctx, s.issue)
	if err != nil {
		return false, err
	}
	what, err := f.Repo.Recorded(ctx, n, name+"-retry")
	if err != nil {
		return false, err
	}
	var c Command
	if what != "" {
		if err := json.Unmarshal([]byte(what), &c); err != nil {
			return false, err
		}
	} else {
		for _, candidate := range t.Pending() {
			if candidate.Verb == Retry && candidate.Comment > reported.Comment.ID {
				c = candidate
				break
			}
		}
		if c.Verb == "" {
			return false, nil
		}
		if err := f.mayAct(ctx); err != nil {
			return false, err
		}
		b, err := json.Marshal(c)
		if err != nil {
			return false, err
		}
		if err := f.Repo.Record(ctx, n, name+"-retry", string(b)); err != nil {
			return false, err
		}
	}
	if s.pending.Report {
		// The protocol answers this command by watching the PR again.
		// Keep it pending, and use it instead of any older queued retry.
		for _, p := range t.Posts {
			if slices.Contains(p.Marker.ReplyTo, c.Comment) {
				s.resumed = true
				state, _ := t.State()
				if state.Comment.ID == p.Comment.ID && state.Marker.Kind == KindPR {
					return true, f.status(ctx, n, LabelPR)
				}
				return true, nil
			}
		}
		s.retrying = &c
		return true, nil
	}
	for _, p := range t.Posts {
		if p.Marker.EffectRetry == name {
			return true, nil
		}
	}
	if err := f.mayAct(ctx); err != nil {
		return false, err
	}
	m := Marker{Kind: KindNote, EffectRetry: name, ReplyTo: []int64{c.Comment}}
	_, err = f.GitHub.PostComment(bypassEffects(ctx), n, noteComment("Resuming the work GitHub refused, using its recorded results.", m))
	return err == nil, err
}

func (f *Factory) withEffects(ctx context.Context, issue github.Issue, work func(context.Context) error) error {
	if ctx.Value(effectsKey{}) != nil {
		return work(ctx)
	}
	s := &issueEffects{f: f, issue: issue}
	ctx = context.WithValue(ctx, effectsKey{}, s)
	if err := s.read(ctx); err != nil {
		return err
	}
	var err error
	pendingName := effectName(s.next - 1)
	if r := s.pending; r != nil {
		name := pendingName
		reported, reportErr := s.report(ctx, r, name)
		if reportErr != nil {
			return reportErr
		}
		s.resumed, err = s.mergedElsewhere(ctx, reported)
		if err == nil && !s.resumed {
			if retry, retryErr := s.retry(ctx, reported, name); retryErr != nil || !retry {
				if s.failed == nil {
					return retryErr
				}
				err = retryErr
			}
		}
		// A post may already have moved the issue on before its label
		// change was refused. Finish that change without posting again.
		switch {
		case r.Report:
			// Its failure post is the report, not the refused work.
		case r.Body != "":
			if resumeClosedPost(issue, r.Body) {
				_, err = s.resumePost(ctx, r)
			}
		case r.Status != nil:
			err = f.status(ctx, r.Status.Issue, r.Status.Label)
		case r.Operation == "add label":
			err = f.GitHub.AddLabels(ctx, r.Issue, r.Labels...)
		case r.Operation == "remove label":
			err = f.GitHub.RemoveLabel(ctx, r.Issue, r.Labels[0])
		}
	}
	if err == nil && !s.resumed {
		err = work(ctx)
	}
	if s.failed != nil {
		if s.failed.Report {
			return err // reporting never releases the refusal's hold
		}
		_, reportErr := s.report(ctx, s.failed, s.failedName)
		return errors.Join(err, reportErr)
	}
	if r := s.pending; r != nil && err == nil {
		if !s.didStatus && !r.Human && !r.Report {
			err = f.GitHub.RemoveLabel(ctx, issue.Number, LabelHumanReview)
			if s.failed != nil {
				_, reportErr := s.report(ctx, s.failed, s.failedName)
				return errors.Join(err, reportErr)
			}
		}
		if err == nil {
			err = s.finish(ctx, pendingName, r)
		}
	}
	return err
}

// A person's merge of a still-open issue completes the refused PR work
// without retrying it. Closing an issue alone never releases its hold.
func (s *issueEffects) mergedElsewhere(ctx context.Context, reported Post) (bool, error) {
	if !s.pending.Report || reported.Marker.PR == 0 {
		return false, nil
	}
	f := s.f
	t, err := f.read(ctx, s.issue)
	if err != nil {
		return false, err
	}
	state, _ := t.State()
	if state.Marker.Kind == KindMerged && state.Marker.PR == reported.Marker.PR {
		return true, f.status(ctx, s.issue.Number, LabelMerged)
	}
	if s.issue.State == "closed" || state.Comment.ID != reported.Comment.ID {
		return false, nil
	}
	pr, err := f.GitHub.PullRequest(ctx, reported.Marker.PR)
	if err != nil || !pr.Merged {
		return false, err
	}
	return true, f.observedMerge(ctx, t, state, pr)
}

func (s *issueEffects) finish(ctx context.Context, name string, r *effectRefusal) error {
	if err := s.f.mayAct(ctx); err != nil {
		return err
	}
	result, err := s.f.saveResult(ctx, r, "Finished the refused operation", "")
	if err != nil {
		return err
	}
	return s.f.Repo.Finish(ctx, s.issue.Number, name, result)
}

// A closed issue may still need its final observation posted, but never
// resumes a proposal or a build whose post was refused.
func resumeClosedPost(issue github.Issue, body string) bool {
	if issue.State != "closed" {
		return true
	}
	m, ok := DecodeMarker(body)
	return ok && (m.Kind == KindMerged || m.Kind == KindClosed || m.Kind == KindPlanDone)
}

func (s *issueEffects) resumePost(ctx context.Context, r *effectRefusal) (Post, error) {
	f := s.f
	if err := f.mayAct(ctx); err != nil {
		return Post{}, err
	}
	if r.Report {
		ctx = bypassEffects(ctx)
	}
	posts, err := f.postsOn(ctx, r.Issue)
	if err != nil {
		return Post{}, err
	}
	var found Post
	for _, p := range posts {
		if p.Comment.Body == r.Body {
			found = p
			break
		}
	}
	if r.Report && found.Comment.ID != 0 {
		if state, ok := (Thread{Posts: posts}).State(); ok && state.Comment.ID > found.Comment.ID {
			return found, nil // a retry or observation already moved it on
		}
	}
	if r.Status != nil {
		ctx = context.WithValue(ctx, effectStatusKey{}, r.Status)
	}
	if found.Comment.ID == 0 {
		c, err := f.GitHub.PostComment(ctx, r.Issue, r.Body)
		if err != nil {
			if r.Report {
				labelErr := f.GitHub.AddLabels(bypassEffects(ctx), s.issue.Number, LabelHumanReview)
				return Post{}, errors.Join(err, labelErr)
			}
			return Post{}, err
		}
		m, _ := DecodeMarker(r.Body)
		found = Post{Comment: c, Marker: m}
	}
	if r.PullLabel != 0 {
		if err := f.GitHub.AddLabels(ctx, r.PullLabel, LabelHumanReview); err != nil {
			return found, err
		}
	}
	if r.Status != nil {
		return found, f.status(ctx, r.Status.Issue, r.Status.Label)
	}
	return found, nil
}

// Merge and mark-ready refusals already have protocol failure posts. Save
// that exact answer before posting, so a failed report never repeats the
// refused request or loses to a closed-issue observation on the next poll.
func (f *Factory) sayRefusal(ctx context.Context, n int, operation, body, label string, answer error) error {
	s, ok := ctx.Value(effectsKey{}).(*issueEffects)
	if !ok || !permanentEffect(ctx, answer) {
		return f.say(ctx, n, body, label)
	}
	r := effectRefusal{Operation: operation, Message: answer.Error(), Issue: n, Body: body, Report: true,
		Status: &effectStatus{Issue: n, Label: label}, Human: s.issue.HasLabel(LabelHumanReview)}
	if m, ok := DecodeMarker(body); ok && m.Failure == FailUnready {
		r.PullLabel = m.PR
	}
	if err := s.record(ctx, r); err != nil {
		return err
	}
	_, err := s.resumePost(ctx, &r)
	return err
}
