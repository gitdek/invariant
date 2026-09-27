package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/scope"
	"github.com/gitdek/invariant/internal/synth"
)

// GitHub is what the factory does on GitHub. github.Client is the real one.
type GitHub interface {
	OpenIssues(ctx context.Context) ([]github.Issue, error)
	Comments(ctx context.Context, issue int) ([]github.Comment, error)
	Events(ctx context.Context, issue int) ([]github.Event, error)
	Permission(ctx context.Context, login string) (string, error)
	PostComment(ctx context.Context, issue int, body string) (github.Comment, error)
	EnsureLabel(ctx context.Context, name, color, description string) error
	AddLabels(ctx context.Context, issue int, labels ...string) error
	RemoveLabel(ctx context.Context, issue int, label string) error
	CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error)
	PullRequest(ctx context.Context, n int) (github.PullRequest, error)
	CheckRuns(ctx context.Context, sha, name string) ([]github.CheckRun, error)
	Merge(ctx context.Context, n int, sha, method string) (string, error)
	DeleteBranch(ctx context.Context, branch string) error
}

// Formalizer drafts statements for an issue. formalize.Formalizer is the
// real one.
type Formalizer interface {
	Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error)
}

// Builder writes and gates the code for a ratified project in dir, leaving
// the finished project in out/result. For an amendment, it starts from the
// project's existing code.
type Builder interface {
	Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error)
}

// Repo is the factory's own clone of the repository. Clone is the real one.
type Repo interface {
	Fetch(ctx context.Context) error
	Dirs(ctx context.Context, ref, under string) ([]string, error)
	Worktree(ctx context.Context, branch, from string) (string, error)
	RemoveWorktree(ctx context.Context, dir string) error
	Commit(ctx context.Context, worktree, dir, message string) (string, error)
	Push(ctx context.Context, worktree, branch string) error
	RevParse(ctx context.Context, ref string) (string, error)
	Show(ctx context.Context, ref, file string) ([]byte, error)
	Export(ctx context.Context, ref string, paths []string, dst string) error
	Scope(ctx context.Context, base, head string, issue int) (scope.Result, error)
}

// Factory turns issues into merged pull requests.
type Factory struct {
	Repository string // owner/name
	GitHub     GitHub
	Repo       Repo
	Formalizer Formalizer
	Builder    Builder
	Base       string // the branch pull requests merge into
	Projects   string // the directory new projects go in, such as examples
	Work       string // where transcripts and logs go
	Check      string // the CI check that gates a merge: invariant/gate
	Language   string // the code's language when an issue has no language label (D-0040)
	// Self is the factory's own login when it acts as its App's bot
	// (D-0041). Then only the bot's comments count as the factory's posts,
	// and no bot's comment is ever a command. Empty means the factory posts
	// as the person gh is logged in as, and its posts are told apart by
	// their markers alone.
	Self string
	Log  func(format string, args ...any)
	Now  func() time.Time
	// Activity, when set, hears what the factory starts on an issue:
	// formalizing, answering, ratifying or building. It hears 0 and "" when
	// the factory is between steps. The watcher writes it down for the
	// dashboard (D-0049).
	Activity func(issue int, doing string)

	writers map[string]bool
}

// Labels show where each issue is. The factory keeps exactly one status
// label on an issue it's working on.
const (
	LabelTrigger     = "invariant"
	LabelGo          = "invariant:go"
	LabelTypeScript  = "invariant:typescript"
	LabelPython      = "invariant:python"
	LabelAsking      = "invariant:asking"
	LabelProposal    = "invariant:awaiting-ratification"
	LabelBuilding    = "invariant:building"
	LabelPR          = "invariant:pr-open"
	LabelMerged      = "invariant:merged"
	LabelHumanReview = "invariant:human-review-needed"
)

var labels = []struct{ name, color, description string }{
	{LabelTrigger, "0CA678", "Ask Invariant to take this issue"},
	{LabelGo, "00ADD8", "Invariant writes this issue's code in Go"},
	{LabelTypeScript, "3178C6", "Invariant writes this issue's code in TypeScript"},
	{LabelPython, "3776AB", "Invariant writes this issue's code in Python"},
	{LabelAsking, "38D9A9", "Invariant asked a question"},
	{LabelProposal, "38D9A9", "Invariant proposed statements to ratify"},
	{LabelBuilding, "0CA678", "Invariant is writing the code"},
	{LabelPR, "0CA678", "Invariant opened a pull request"},
	{LabelMerged, "8250DF", "Invariant merged its pull request"},
	{LabelHumanReview, "D1242F", "Invariant needs a person to look"},
}

var statusLabels = []string{LabelAsking, LabelProposal, LabelBuilding, LabelPR, LabelMerged, LabelHumanReview}

// Prepare creates the labels the factory uses.
func (f *Factory) Prepare(ctx context.Context) error {
	for _, l := range labels {
		if err := f.GitHub.EnsureLabel(ctx, l.name, l.color, l.description); err != nil {
			return fmt.Errorf("label %s: %w", l.name, err)
		}
	}
	return nil
}

// Watch polls until ctx ends.
func (f *Factory) Watch(ctx context.Context, every time.Duration) error {
	for {
		if err := f.Poll(ctx); err != nil {
			f.logf("poll: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

// Poll takes one step on every open issue that needs one.
func (f *Factory) Poll(ctx context.Context) error {
	f.writers = map[string]bool{}
	issues, err := f.GitHub.OpenIssues(ctx)
	if err != nil {
		return err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	var errs []error
	for _, issue := range issues {
		err := f.step(ctx, issue)
		f.doing(0, "")
		if err != nil {
			errs = append(errs, fmt.Errorf("#%d: %w", issue.Number, err))
		}
	}
	f.doing(0, "")
	return errors.Join(errs...)
}

// read gathers an issue's thread: the factory's posts, and the commands and
// comments of people with write access.
func (f *Factory) read(ctx context.Context, issue github.Issue) (Thread, error) {
	t := Thread{Issue: issue}
	comments, err := f.GitHub.Comments(ctx, issue.Number)
	if err != nil {
		return t, err
	}
	for _, c := range comments {
		if m, ok := DecodeMarker(c.Body); ok && (f.Self == "" || c.User.Login == f.Self) {
			t.Posts = append(t.Posts, Post{Comment: c, Marker: m})
		}
	}
	// A new issue is the factory's if a writer opened it with /invariant solve,
	// or a writer gave it the invariant label.
	if len(t.Posts) == 0 {
		if ok, err := f.writer(ctx, issue.User.Login); err != nil {
			return t, err
		} else if ok && hasVerb(ParseCommands(issue.Body), Solve) {
			t.Commands = append(t.Commands, Command{Verb: Solve, By: issue.User.Login, URL: issue.URL, At: issue.CreatedAt})
		} else if issue.HasLabel(LabelTrigger) {
			if by, err := f.labeledBy(ctx, issue); err != nil {
				return t, err
			} else if by != "" {
				t.Commands = append(t.Commands, Command{Verb: Solve, By: by, URL: issue.URL, At: issue.CreatedAt})
			}
		}
	}
	for _, c := range comments {
		if c.User.Type == "Bot" || c.User.Login == f.Self {
			continue // a bot, the factory included, never directs the factory
		}
		if _, ok := DecodeMarker(c.Body); ok && f.Self == "" {
			continue
		}
		ok, err := f.writer(ctx, c.User.Login)
		if err != nil {
			return t, err
		}
		if !ok {
			continue
		}
		t.People = append(t.People, c)
		for _, cmd := range ParseCommands(c.Body) {
			cmd.Comment, cmd.By, cmd.URL, cmd.At = c.ID, c.User.Login, c.URL, c.CreatedAt
			t.Commands = append(t.Commands, cmd)
		}
	}
	return t, nil
}

// labeledBy is the writer who most recently gave the issue the trigger
// label, if a writer did.
func (f *Factory) labeledBy(ctx context.Context, issue github.Issue) (string, error) {
	events, err := f.GitHub.Events(ctx, issue.Number)
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Event == "labeled" && e.Label != nil && e.Label.Name == LabelTrigger && e.Actor.Type != "Bot" {
			if ok, err := f.writer(ctx, e.Actor.Login); err != nil || ok {
				return e.Actor.Login, err
			}
		}
	}
	return "", nil
}

func (f *Factory) writer(ctx context.Context, login string) (bool, error) {
	if ok, seen := f.writers[login]; seen {
		return ok, nil
	}
	perm, err := f.GitHub.Permission(ctx, login)
	if err != nil {
		return false, err
	}
	ok := perm == "admin" || perm == "maintain" || perm == "write"
	f.writers[login] = ok
	return ok, nil
}

// step takes the next step on one issue.
func (f *Factory) step(ctx context.Context, issue github.Issue) error {
	t, err := f.read(ctx, issue)
	if err != nil {
		return err
	}
	state, started := t.State()
	switch state.Marker.Kind {
	case KindPR:
		return f.watch(ctx, t, state)
	case KindRatified:
		// A build that stopped partway, when the factory stopped. Pick it up.
		return f.build(ctx, t, state)
	}
	pending := t.Pending()
	if len(pending) == 0 {
		return nil
	}
	c := pending[0]
	kind := state.Marker.Kind
	switch c.Verb {
	case Solve:
		if started && kind != KindStuck && kind != KindUnsupported && kind != KindClosed {
			return f.note(ctx, t, c, "I'm already working on this issue.")
		}
		return f.formalize(ctx, t, nil, nil, []int64{c.Comment})
	case Revise:
		switch kind {
		case KindForks, KindProposal, KindStuck, KindUnsupported, KindClosed:
			return f.formalize(ctx, t, state.Marker.Answers, state.Marker.Proposal, []int64{c.Comment})
		}
		return f.note(ctx, t, c, "There's no draft to revise right now.")
	case Choose:
		if kind != KindForks {
			return f.note(ctx, t, c, "There's no open question to answer right now.")
		}
		return f.choose(ctx, t, state, pending)
	case Retry:
		if kind != KindFailed || state.Marker.PR == 0 {
			return f.note(ctx, t, c, "There's no failed pull request to look at again right now.")
		}
		// Watch the pull request again. Nothing merges unless CI's gate passes
		// on its current head, it stays in scope, and its lock is still the
		// ratified proposal.
		m := state.Marker
		m.Kind, m.ReplyTo = KindPR, []int64{c.Comment}
		return f.say(ctx, t.Issue.Number, post("pull request", fmt.Sprintf("Watching #%d again. I'll merge it once CI's `invariant/gate` passes on its current head.", m.PR), m), LabelPR)
	case Ratify:
		if kind != KindProposal {
			return f.note(ctx, t, c, "There's no proposal waiting for ratification right now.")
		}
		p := state.Marker.Proposal
		if !matches(c.Args, p.Hash) {
			return f.note(ctx, t, c, fmt.Sprintf("That doesn't name the current proposal, so I haven't ratified anything. "+
				"To ratify it, comment `/invariant ratify %s`.", strings.TrimPrefix(p.Hash, "sha256:")[:hashChars]))
		}
		return f.ratify(ctx, t, state, c)
	}
	return nil
}

// matches says whether a ratify command names the proposal: at least
// hashChars of its hex digest.
func matches(args []string, hash string) bool {
	if len(args) != 1 {
		return false
	}
	got := strings.ToLower(strings.TrimPrefix(args[0], "sha256:"))
	return len(got) >= hashChars && strings.HasPrefix(strings.TrimPrefix(hash, "sha256:"), got)
}

// choose records people's answers to the open forks. Once every fork is
// answered, it drafts again with the answers.
func (f *Factory) choose(ctx context.Context, t Thread, state Post, pending []Command) error {
	f.doing(t.Issue.Number, "answering")
	forks := state.Marker.Forks
	chosen := map[string]formalize.Answer{}
	var replyTo []int64
	for _, c := range pending {
		if c.Verb != Choose {
			continue
		}
		fork, option, problem := findChoice(forks, c.Args)
		if problem != "" {
			return f.note(ctx, t, c, problem)
		}
		chosen[strings.ToUpper(fork.ID)] = formalize.Answer{Fork: fork.ID, Question: fork.Question, Option: option.ID, Says: option.Says, By: c.By, Comment: c.URL}
		replyTo = append(replyTo, c.Comment)
	}
	for _, fork := range forks {
		if _, ok := chosen[strings.ToUpper(fork.ID)]; !ok {
			return nil // wait for the rest
		}
	}
	answers := append([]formalize.Answer(nil), state.Marker.Answers...)
	for _, fork := range forks {
		answers = append(answers, chosen[strings.ToUpper(fork.ID)])
	}
	return f.formalize(ctx, t, answers, nil, replyTo)
}

func findChoice(forks []formalize.Fork, args []string) (formalize.Fork, formalize.Option, string) {
	if len(args) != 2 {
		return formalize.Fork{}, formalize.Option{}, "To answer a question, comment `/invariant choose` with its number and an option, like `/invariant choose F1 A`."
	}
	for _, fork := range forks {
		if strings.EqualFold(fork.ID, args[0]) {
			if o, ok := fork.Option(args[1]); ok {
				return fork, o, ""
			}
			return fork, formalize.Option{}, fmt.Sprintf("%s has no option %s.", fork.ID, args[1])
		}
	}
	return formalize.Fork{}, formalize.Option{}, fmt.Sprintf("There's no open question %s.", args[0])
}

// formalize drafts statements for the issue and posts what came of it:
// forks to decide, a proposal to ratify, or why neither.
func (f *Factory) formalize(ctx context.Context, t Thread, answers []formalize.Answer, previous *formalize.Proposal, replyTo []int64) error {
	n := t.Issue.Number
	f.logf("#%d: formalizing", n)
	f.doing(n, "formalizing")
	out := filepath.Join(f.Work, fmt.Sprintf("issue-%d", n), "formalize-"+f.now().Format("20060102-150405"))
	req := f.request(t, answers, previous)
	// A Project: line names the project the issue changes, or where a new
	// one goes (D-0045).
	var newDir string
	if dir := projectLine(t.Issue.Body); dir != "" {
		if problem := badDir(dir); problem != "" {
			return f.say(ctx, n, stuckComment("The issue names the project `"+dir+"`, but "+problem+".", Marker{Kind: KindStuck, ReplyTo: replyTo}), LabelHumanReview)
		}
		cur, err := f.current(ctx, dir)
		if err != nil {
			return err
		}
		if cur != nil {
			req.Current, req.Language = cur, cur.Manifest.Language
		} else {
			newDir = dir
		}
	}
	// Code: lines name existing code for the factory to check as it is
	// (D-0054). The formalizer reads it as it is on the base branch.
	if paths := codeLines(t.Issue.Body); len(paths) > 0 {
		for _, p := range paths {
			if problem := badDir(p); problem != "" {
				return f.say(ctx, n, stuckComment("The issue names the code `"+p+"`, but "+problem+".", Marker{Kind: KindStuck, ReplyTo: replyTo}), LabelHumanReview)
			}
		}
		if req.Current != nil && len(req.Current.Manifest.Existing) == 0 {
			return f.say(ctx, n, stuckComment("The issue names code to check, but `"+req.Current.Dir+"` is a project that holds its own code. Name a new directory with the Project: line instead.", Marker{Kind: KindStuck, ReplyTo: replyTo}), LabelHumanReview)
		}
		root, err := os.MkdirTemp("", "invariant-existing-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
		if err := f.Repo.Fetch(ctx); err != nil {
			return err
		}
		if err := f.Repo.Export(ctx, "origin/"+f.Base, paths, root); err != nil {
			return f.say(ctx, n, stuckComment(fmt.Sprintf("The issue names code to check, and I couldn't read it on `%s`: %v", f.Base, err), Marker{Kind: KindStuck, ReplyTo: replyTo}), LabelHumanReview)
		}
		req.Existing, req.Language = &formalize.Existing{Paths: paths, Root: root}, "typescript"
	}
	res, err := f.Formalizer.Formalize(ctx, req, out)
	if err == nil && res.Proposal != nil && newDir != "" {
		res.Proposal.Dir = newDir
	}
	switch {
	case err != nil:
		res = &formalize.Result{Problem: "the formalizer couldn't run: " + err.Error()}
	case res.Proposal == nil && res.Problem == "":
		res.Problem = "the formalizer left no draft"
	}
	m := Marker{ReplyTo: replyTo, Answers: answers}
	switch p := res.Proposal; {
	case res.Problem != "":
		m.Kind, m.Proposal = KindStuck, p
		return f.say(ctx, n, stuckComment(res.Problem, m), LabelHumanReview)
	case p.Unsupported != "":
		m.Kind = KindUnsupported
		return f.say(ctx, n, unsupportedComment(p.Unsupported, m), "")
	case len(p.Forks) > 0:
		m.Kind, m.Forks, m.Proposal = KindForks, p.Forks, p
		return f.say(ctx, n, forksComment(p.Forks, m), LabelAsking)
	case p.Target != nil:
		m.Kind, m.Proposal = KindProposal, p
		return f.say(ctx, n, amendmentComment(p, res.Report, res.Changes, m), LabelProposal)
	default:
		m.Kind, m.Proposal = KindProposal, p
		return f.say(ctx, n, proposalComment(p, res.Report, m), LabelProposal)
	}
}

// projectLine finds the project an issue names, in a line such as
// "Project: examples/04-api-rate-limiter".
func projectLine(body string) string {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if name, dir, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "project") {
			return strings.Trim(strings.TrimSpace(dir), "`/")
		}
	}
	return ""
}

// codeLines reads the existing code an issue names for the factory to
// check, one `Code: <path>` line each (D-0054).
func codeLines(body string) []string {
	var paths []string
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if name, p, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "code") {
			if p = strings.Trim(strings.TrimSpace(p), "`/"); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// badDir says what's wrong with a project directory an issue names.
func badDir(dir string) string {
	switch {
	case path.IsAbs(dir) || path.Clean(dir) != dir || dir == "." || strings.HasPrefix(dir, "../"):
		return "that isn't a clean path inside the repository"
	case dir == ".github" || strings.HasPrefix(dir, ".github/"):
		return "the factory never touches CI configuration"
	case strings.ContainsAny(dir, " \t"):
		return "a project's directory can't contain spaces"
	}
	return ""
}

// current reads the project in dir as it stands on the base branch, or
// returns nil when there's no project there.
func (f *Factory) current(ctx context.Context, dir string) (*formalize.Current, error) {
	if err := f.Repo.Fetch(ctx); err != nil {
		return nil, err
	}
	ref := "origin/" + f.Base
	b, err := f.Repo.Show(ctx, ref, dir+"/.invariant/invariant.json")
	if err != nil {
		return nil, nil
	}
	c := &formalize.Current{Dir: dir}
	if err := json.Unmarshal(b, &c.Manifest); err != nil {
		return nil, fmt.Errorf("%s's manifest: %w", dir, err)
	}
	if b, err = f.Repo.Show(ctx, ref, dir+"/.invariant/ratified.lock"); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c.Lock); err != nil {
		return nil, fmt.Errorf("%s's lock: %w", dir, err)
	}
	if b, err = f.Repo.Show(ctx, ref, dir+"/"+c.Manifest.Module); err != nil {
		return nil, err
	}
	c.ModuleText = string(b)
	return c, nil
}

// language is the language an issue's label asks for, or the repository's
// default (D-0040).
func (f *Factory) language(issue github.Issue) string {
	for label, lang := range map[string]string{LabelGo: "go", LabelTypeScript: "typescript", LabelPython: "python"} {
		if issue.HasLabel(label) {
			return lang
		}
	}
	if f.Language != "" {
		return f.Language
	}
	return "go"
}

// request is what people have said on the issue, for the formalizer and the
// project's request.md.
func (f *Factory) request(t Thread, answers []formalize.Answer, previous *formalize.Proposal) formalize.Request {
	req := formalize.Request{Repo: f.Repository, Language: f.language(t.Issue), Issue: t.Issue.Number, Title: t.Issue.Title,
		Body: withoutCommands(t.Issue.Body), Author: t.Issue.User.Login, Answers: answers, Previous: previous}
	for _, c := range t.People {
		if body := withoutCommands(c.Body); body != "" {
			req.Thread = append(req.Thread, formalize.Message{By: c.User.Login, Body: body})
		}
	}
	return req
}

// withoutCommands drops command lines, which are for the factory, not about
// the system.
func withoutCommands(body string) string {
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if len(ParseCommands(line)) == 0 {
			keep = append(keep, line)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// ratify commits the ratified proposal as a new project on the issue's
// branch, then builds it.
func (f *Factory) ratify(ctx context.Context, t Thread, state Post, c Command) error {
	f.doing(t.Issue.Number, "ratifying")
	posted, err := f.commitRatification(ctx, t, state, c)
	if errors.Is(err, errStale) {
		return f.note(ctx, t, c, "The project changed after I drafted this amendment, so I haven't ratified anything. Comment `/invariant revise` and I'll draft it again from the project as it is now.")
	}
	if err != nil {
		return err
	}
	return f.build(ctx, t, posted)
}

// errStale means an amendment no longer amends the project as it is.
var errStale = errors.New("the project changed since the amendment was drafted")

// commitRatification pushes the ratification commit and says so on the
// issue.
func (f *Factory) commitRatification(ctx context.Context, t Thread, state Post, c Command) (Post, error) {
	n, p := t.Issue.Number, state.Marker.Proposal
	f.logf("#%d: ratified by @%s", n, c.By)
	if err := f.Repo.Fetch(ctx); err != nil {
		return Post{}, err
	}
	if p.Target != nil {
		cur, err := f.current(ctx, p.Target.Dir)
		if err != nil {
			return Post{}, err
		}
		if cur == nil || project.ProposalHash(cur.Lock.Bounds, cur.Lock.Statements) != p.Target.Amends {
			return Post{}, errStale
		}
	}
	branch, dir, from, err := f.branchFor(ctx, n, p)
	if err != nil {
		return Post{}, err
	}
	wt, err := f.Repo.Worktree(ctx, branch, from)
	if err != nil {
		return Post{}, err
	}
	defer f.Repo.RemoveWorktree(ctx, wt)
	rat := &project.Ratification{By: c.By, At: c.At, Issue: n, Comment: c.URL, Proposal: p.Hash}
	if p.Target != nil {
		rat.Amends, rat.Previous = p.Target.Amends, p.Target.Previous
	}
	req := f.request(t, state.Marker.Answers, nil)
	root := filepath.Join(wt, filepath.FromSlash(dir))
	if err := p.Write(root, req.Markdown(), rat); err != nil {
		return Post{}, err
	}
	if p.Target == nil {
		if err := scaffold(root, f.Repository, dir, p); err != nil {
			return Post{}, err
		}
		readme := projectReadme(t, dir, p, state.Marker.Answers, c.By, c.URL)
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
			return Post{}, err
		}
	}
	// What's committed must be exactly what was proposed.
	written, err := project.Load(root)
	if err != nil {
		return Post{}, err
	}
	if got := project.ProposalHash(written.Lock.Bounds, written.Lock.Statements); got != p.Hash {
		return Post{}, fmt.Errorf("the project written for ratification hashes to %s, not the proposal's %s", got, p.Hash)
	}
	msg := fmt.Sprintf("Ratify the statements for #%d\n\nRatified by @%s in %s.\nProposal %s.", n, c.By, c.URL, p.Hash)
	if p.Target != nil {
		msg = fmt.Sprintf("Ratify the amendment for #%d\n\nRatified by @%s in %s.\nProposal %s, amending %s (ratified in %s).",
			n, c.By, c.URL, p.Hash, p.Target.Amends, p.Target.Previous)
	}
	if _, err := f.Repo.Commit(ctx, wt, dir, msg); err != nil && !errors.Is(err, ErrNothingToCommit) {
		return Post{}, err
	}
	if err := f.Repo.Push(ctx, wt, branch); err != nil {
		return Post{}, err
	}
	m := Marker{Kind: KindRatified, ReplyTo: []int64{c.Comment}, Answers: state.Marker.Answers, Proposal: p, Project: dir, Branch: branch, Hash: p.Hash}
	body := ratifiedComment(c.By, dir, branch, p.Hash, len(p.Statements), m)
	posted, err := f.GitHub.PostComment(ctx, n, body)
	if err != nil {
		return Post{}, err
	}
	return Post{Comment: posted, Marker: m}, f.status(ctx, n, LabelBuilding)
}

// branchFor picks the branch and project directory for a ratified proposal,
// and the ref to start from. If an earlier attempt already pushed this
// proposal's branch, it picks that up where it stopped; a branch left from a
// different proposal gets a numbered successor.
func (f *Factory) branchFor(ctx context.Context, issue int, p *formalize.Proposal) (branch, dir, from string, err error) {
	for i := 1; ; i++ {
		branch = fmt.Sprintf("invariant/issue-%d-%s", issue, p.Slug)
		if i > 1 {
			branch += fmt.Sprintf("-%d", i)
		}
		if _, err := f.Repo.RevParse(ctx, "origin/"+branch); err != nil {
			dir, err := f.dirFor(ctx, p)
			return branch, dir, "origin/" + f.Base, err
		}
		sc, err := f.Repo.Scope(ctx, "origin/"+f.Base, "origin/"+branch, issue)
		if err != nil {
			return "", "", "", err
		}
		if sc.Project == "" {
			continue
		}
		b, err := f.Repo.Show(ctx, "origin/"+branch, sc.Project+"/.invariant/ratified.lock")
		var lock project.Lock
		if err == nil && json.Unmarshal(b, &lock) == nil && lock.Ratified != nil && lock.Ratified.Proposal == p.Hash {
			return branch, sc.Project, "origin/" + branch, nil
		}
	}
}

// scaffold writes the language's own project file: a go.mod for Go, and a
// package.json with no dependencies for TypeScript. Python needs none.
func scaffold(root, repo, dir string, p *formalize.Proposal) error {
	switch p.Manifest().Language {
	case "go":
		gomod := fmt.Sprintf("module github.com/%s/%s\n\ngo 1.27.1\n", repo, dir)
		return os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o644)
	case "typescript":
		pkg, err := json.MarshalIndent(map[string]any{"name": p.Slug, "private": true, "type": "module",
			"description": capitalize(p.Name) + ", written by Invariant and checked against its ratified TLA+ model."}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, "package.json"), append(pkg, '\n'), 0o644)
	}
	return nil
}

// dirFor is where a proposal's project lives: the project an amendment
// changes, the directory its issue named, or a new numbered directory.
func (f *Factory) dirFor(ctx context.Context, p *formalize.Proposal) (string, error) {
	switch {
	case p.Target != nil:
		return p.Target.Dir, nil
	case p.Dir != "":
		return p.Dir, nil
	}
	return f.projectDir(ctx, p.Slug)
}

// projectDir picks the directory for a new project: the next number in
// f.Projects, then the slug, like examples/03-bounded-buffer.
func (f *Factory) projectDir(ctx context.Context, slug string) (string, error) {
	dirs, err := f.Repo.Dirs(ctx, "origin/"+f.Base, f.Projects)
	if err != nil {
		return "", err
	}
	next := 1
	for _, d := range dirs {
		if m := numbered.FindStringSubmatch(d); m != nil {
			if n, _ := strconv.Atoi(m[1]); n >= next {
				next = n + 1
			}
		}
	}
	return path.Join(f.Projects, fmt.Sprintf("%02d-%s", next, slug)), nil
}

var numbered = regexp.MustCompile(`^(\d+)-`)

// build writes and gates the code for a ratified project, then opens a pull
// request: ready to merge if the gate passed, a draft for people if not.
func (f *Factory) build(ctx context.Context, t Thread, ratified Post) error {
	n, m := t.Issue.Number, ratified.Marker
	f.logf("#%d: building %s", n, m.Project)
	f.doing(n, "building")
	if err := f.Repo.Fetch(ctx); err != nil {
		return err
	}
	wt, err := f.Repo.Worktree(ctx, m.Branch, "origin/"+m.Branch)
	if err != nil {
		return err
	}
	defer f.Repo.RemoveWorktree(ctx, wt)
	root := filepath.Join(wt, filepath.FromSlash(m.Project))
	next := Marker{Kind: KindFailed, Answers: m.Answers, Proposal: m.Proposal, Project: m.Project, Branch: m.Branch, Hash: m.Hash}
	// A build costs an agent run, so a build that keeps stopping partway
	// isn't retried forever.
	logs := filepath.Join(f.Work, fmt.Sprintf("issue-%d", n))
	if tries, _ := filepath.Glob(filepath.Join(logs, "build-*")); len(tries) >= maxBuilds {
		return f.say(ctx, n, buildFailedComment(nil, nil, fmt.Errorf("%d builds stopped before they finished; the logs are in %s", len(tries), logs), next), LabelHumanReview)
	}
	out := filepath.Join(logs, "build-"+f.now().Format("20060102-150405"))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	amend := m.Proposal != nil && m.Proposal.Target != nil
	res, runErr := f.Builder.Build(ctx, root, out, amend)
	if res == nil || res.Final == nil {
		return f.say(ctx, n, buildFailedComment(nil, res, runErr, next), LabelHumanReview)
	}
	// The agent's files replace the project's, so a file it removed is gone.
	if err := removeOwned(root); err != nil {
		return err
	}
	result := res.Dir
	if result == "" {
		result = filepath.Join(out, "result")
	}
	if err := copyResult(result, root); err != nil {
		return err
	}
	verdict := "passed the gate"
	if !res.Final.Passed {
		verdict = "didn't pass the gate"
	}
	msg := fmt.Sprintf("Implement #%d: %s\n\nWritten by Invariant against the statements ratified in the previous commit. It %s: %s.",
		n, t.Issue.Title, verdict, res.Final.Assurance)
	if amend {
		msg = fmt.Sprintf("Implement #%d: %s\n\nChanged by Invariant to meet the amended statements ratified in the previous commit. It %s: %s.",
			n, t.Issue.Title, verdict, res.Final.Assurance)
	}
	if _, err := f.Repo.Commit(ctx, wt, m.Project, msg); err != nil {
		return err
	}
	if err := f.Repo.Push(ctx, wt, m.Branch); err != nil {
		return err
	}
	pr, err := f.GitHub.CreatePullRequest(ctx, github.NewPullRequest{
		Title: t.Issue.Title, Head: m.Branch, Base: f.Base, Draft: !res.Final.Passed,
		Body: pullRequestBody(t, m, res, m.Proposal),
	})
	if err != nil {
		return err
	}
	next.PR = pr.Number
	if !res.Final.Passed {
		if err := f.GitHub.AddLabels(ctx, pr.Number, LabelHumanReview); err != nil {
			return err
		}
		return f.say(ctx, n, buildFailedComment(&pr, res, runErr, next), LabelHumanReview)
	}
	next.Kind = KindPR
	return f.say(ctx, n, prComment(pr, res.Final, next), LabelPR)
}

// removeOwned deletes the files in a project that the agent owns, before
// its result is copied in.
func removeOwned(root string) error {
	p, err := project.Load(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(file string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, file)
		if synth.Owned(p.Manifest, filepath.ToSlash(rel)) {
			return os.Remove(file)
		}
		return nil
	})
}

// maxBuilds is how many builds of one issue the factory starts before it
// asks a person to look.
const maxBuilds = 2

// copyResult copies the finished project over the ratified one. The model,
// the code and anything else the agent wrote come across; the people's files
// are the same in both, because synthesis assembles the result from the
// originals.
func copyResult(from, to string) error {
	return filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}

// watch follows an open pull request, and merges it once CI's gate passes on
// its head, it stays in scope, and its lock is what was ratified.
func (f *Factory) watch(ctx context.Context, t Thread, state Post) error {
	n, m := t.Issue.Number, state.Marker
	pr, err := f.GitHub.PullRequest(ctx, m.PR)
	if err != nil {
		return err
	}
	next := m
	next.ReplyTo = nil
	switch {
	case pr.Merged:
		next.Kind = KindMerged
		return f.say(ctx, n, post("merged", fmt.Sprintf("#%d was merged.", pr.Number), next), LabelMerged)
	case pr.State == "closed":
		next.Kind = KindClosed
		return f.say(ctx, n, closedComment(pr, next), "")
	}
	runs, err := f.GitHub.CheckRuns(ctx, pr.Head.SHA, f.Check)
	if err != nil {
		return err
	}
	run, done := latest(runs)
	if !done {
		return nil
	}
	next.Kind = KindFailed
	if run.Conclusion != "success" {
		return f.say(ctx, n, ciFailedComment(pr, run, next), LabelHumanReview)
	}
	if err := f.Repo.Fetch(ctx); err != nil {
		return err
	}
	head := "origin/" + m.Branch
	if sha, err := f.Repo.RevParse(ctx, head); err != nil {
		return err
	} else if sha != pr.Head.SHA {
		return nil // the branch moved; wait for CI on its new head
	}
	sc, err := f.Repo.Scope(ctx, "origin/"+f.Base, head, n)
	if err != nil {
		return err
	}
	problems := sc.Problems
	if sc.Project != m.Project {
		problems = append(problems, fmt.Sprintf("it changes %q, not the ratified project %q", sc.Project, m.Project))
	}
	if b, err := f.Repo.Show(ctx, head, m.Project+"/.invariant/ratified.lock"); err != nil {
		problems = append(problems, "its lock can't be read: "+err.Error())
	} else {
		var lock project.Lock
		if err := json.Unmarshal(b, &lock); err != nil || lock.Ratified == nil || lock.Ratified.Proposal != m.Hash ||
			project.ProposalHash(lock.Bounds, lock.Statements) != m.Hash {
			problems = append(problems, "its lock isn't the proposal that was ratified")
		}
	}
	if len(problems) > 0 {
		return f.say(ctx, n, scopeFailedComment(pr, problems, next), LabelHumanReview)
	}
	sha, err := f.GitHub.Merge(ctx, pr.Number, pr.Head.SHA, "merge")
	if err != nil {
		return err
	}
	f.logf("#%d: merged #%d as %s", n, pr.Number, sha)
	if err := f.GitHub.DeleteBranch(ctx, m.Branch); err != nil {
		f.logf("#%d: deleting %s: %v", n, m.Branch, err)
	}
	next.Kind = KindMerged
	return f.say(ctx, n, mergedComment(pr, run, sha, &next), LabelMerged)
}

// latest is the newest check run, and whether it has completed.
func latest(runs []github.CheckRun) (github.CheckRun, bool) {
	if len(runs) == 0 {
		return github.CheckRun{}, false
	}
	newest := runs[0]
	for _, r := range runs[1:] {
		if r.ID > newest.ID {
			newest = r
		}
	}
	return newest, newest.Status == "completed"
}

// note answers a command without changing anything.
func (f *Factory) note(ctx context.Context, t Thread, c Command, text string) error {
	_, err := f.GitHub.PostComment(ctx, t.Issue.Number, noteComment(text, Marker{Kind: KindNote, ReplyTo: []int64{c.Comment}}))
	return err
}

// say posts a comment and sets the issue's status label. An empty label
// clears it.
func (f *Factory) say(ctx context.Context, issue int, body, label string) error {
	if _, err := f.GitHub.PostComment(ctx, issue, body); err != nil {
		return err
	}
	return f.status(ctx, issue, label)
}

func (f *Factory) status(ctx context.Context, issue int, label string) error {
	for _, l := range statusLabels {
		if l != label {
			if err := f.GitHub.RemoveLabel(ctx, issue, l); err != nil {
				return err
			}
		}
	}
	if label == "" {
		return nil
	}
	return f.GitHub.AddLabels(ctx, issue, label)
}

func hasVerb(cmds []Command, verb string) bool {
	for _, c := range cmds {
		if c.Verb == verb {
			return true
		}
	}
	return false
}

func (f *Factory) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Factory) doing(issue int, what string) {
	if f.Activity != nil {
		f.Activity(issue, what)
	}
}

func (f *Factory) logf(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}
