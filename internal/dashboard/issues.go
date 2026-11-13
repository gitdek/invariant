package dashboard

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
)

// Stages an issue moves through, as the page draws them.
const (
	StageQueued    = "queued"    // the factory was asked, and hasn't posted yet
	StageAsking    = "asking"    // it asked people to decide forks
	StageRatifying = "ratifying" // it proposed statements for ratification
	StageBuilding  = "building"  // it's writing the code
	StageGate      = "gate"      // its pull request is waiting for CI's gate
	StageReview    = "review"    // it needs a person
	StageMerged    = "merged"
	StageClosed    = "closed" // closed without a merge
)

var stageLabels = map[string]string{
	factory.LabelAsking:      StageAsking,
	factory.LabelProposal:    StageRatifying,
	factory.LabelBuilding:    StageBuilding,
	factory.LabelPR:          StageGate,
	factory.LabelMerged:      StageMerged,
	factory.LabelHumanReview: StageReview,
}

// Who a span of an issue's time belongs to.
const (
	WhoFactory = "factory"
	WhoPeople  = "people"
	WhoCI      = "ci"
	WhoPerson  = "person" // an event, not a span: a person acted
)

// Issue is one issue the factory took, as a lane on the page: what happened
// when, who it was waiting on in between, and the measures D-0048 tracks.
type Issue struct {
	Repo           string     `json:"repo"`
	Number         int        `json:"number"`
	Title          string     `json:"title"`
	Open           bool       `json:"open"`
	Stage          string     `json:"stage"`
	Language       string     `json:"language,omitempty"`
	Project        string     `json:"project,omitempty"`
	Amends         string     `json:"amends,omitempty"` // "#3" when the issue amended a project
	PR             int        `json:"pr,omitempty"`
	Opened         time.Time  `json:"opened"`
	Closed         *time.Time `json:"closed,omitempty"`
	Events         []Event    `json:"events"`
	Spans          []Span     `json:"spans"`
	FactorySeconds int        `json:"factorySeconds"` // CI included, time waiting on people not
	PeopleSeconds  int        `json:"peopleSeconds"`
	PeopleComments int        `json:"peopleComments"`
	Answers        int        `json:"answers,omitempty"` // forks people decided
	Questions      int        `json:"questions,omitempty"`
	Statements     int        `json:"statements,omitempty"`
	SpendUSD       float64    `json:"spendUSD,omitempty"` // the agents' estimated spend, where the factory recorded it
	GateRuns       int        `json:"gateRuns,omitempty"`
	// Waiting is what the factory needs from a person, when it's waiting on
	// one: its open questions, the proposal to ratify, or a pull request
	// that failed. It comes from the factory's own post, never from
	// people's comments.
	Waiting *Waiting `json:"waiting,omitempty"`
	// Acted is a command posted from /act, its Ratify all or the agent's
	// door that GitHub took, and no read of GitHub has found yet (#175).
	// Until one does, the issue waits on no one.
	Acted *Acted `json:"acted,omitempty"`
}

// Waiting is what an open issue needs from a person.
type Waiting struct {
	Proposal   *formalize.Proposal `json:"proposal,omitempty"`
	Reader     *ReaderPresentation `json:"reader,omitempty"`
	Posters    *PosterLinks        `json:"posters,omitempty"`    // its chart for people to read, in place of the evidence image (#784)
	Kind       string              `json:"kind"`                 // forks, proposal or failed
	Failure    string              `json:"failure,omitempty"`    // why it failed: stopped, limit, gate, ci, unmergeable, trusted, merge, unready or draft
	Forks      []Question          `json:"forks,omitempty"`      // the questions to answer
	Name       string              `json:"name,omitempty"`       // the proposal's project
	Hash       string              `json:"hash,omitempty"`       // the proposal to ratify, as its short hash
	Statements []Said              `json:"statements,omitempty"` // what the proposal says
	Amends     string              `json:"amends,omitempty"`     // what an amendment replaces
	// Relaxed names the bugs the lock being amended calls current that this
	// proposal classifies otherwise, or leaves out (#857). The gate has to
	// replay a current bug against the code, and ratifying the amendment
	// ends that for each of these, so the card says so before it's opened.
	Relaxed []string `json:"relaxed,omitempty"`
	// Unchanged is an amendment whose statements and bounds are exactly
	// those of the lock it amends, so only the code changes.
	Unchanged bool `json:"unchanged,omitempty"`
	// Plan is a plumbing plan to ratify (D-0105). Its acceptance tests are
	// in Statements, each of kind test.
	Plan *PlanView `json:"plan,omitempty"`
	// IssuePlan is a plan of issues to ratify, in place of statements.
	IssuePlan *IssuePlanView `json:"issuePlan,omitempty"`
	// Together is a proposal that changes no statement: a plumbing plan, a
	// plan of issues, or a rebuild at its lock's hash. /act can ratify it
	// with others, with one confirmation. One that changes statements
	// ratifies alone, from its own card.
	Together bool `json:"together,omitempty"`
	// ActURL is the issue's own card on /act, when the dashboard serves
	// /act, so the public page's card can link to it.
	ActURL string    `json:"actURL,omitempty"`
	PR     int       `json:"pr,omitempty"` // the pull request that failed, or was closed
	Since  time.Time `json:"since"`
	// Takes is what the factory takes now, as /invariant's verbs, the
	// card's main action first, less a retry whose build can't start (Gone).
	// The card offers only these, and /act posts only these, so neither
	// offers a command the factory refuses (#218, D-0156).
	Takes []string `json:"takes"`
	// Retry says what the retry the card offers does, only where Takes
	// offers one (#263).
	Retry *RetryCopy `json:"retry,omitempty"`
	// Gone is a retry's build that couldn't start, since the commit holding
	// the plan's ratification is gone, so the card offers only drafting the
	// plan again (D-0220, D-0238).
	Gone bool `json:"gone,omitempty"`
	// Now is the card's pull request as GitHub had it at the last refresh,
	// when the card names one and it could be read (#865). Everything else
	// here says how it was when the factory stopped.
	Now *PullNow `json:"now,omitempty"`
}

// PullNow is a pull request as GitHub has it: whether it's open, and for an
// open one whether it's a draft, whether it can merge into its base, and how
// CI's gate went on its head.
type PullNow struct {
	State string `json:"state"` // open, closed or merged
	Draft bool   `json:"draft,omitempty"`
	// Conflicts says GitHub can't merge it into its base. Unsettled says
	// GitHub hasn't worked that out yet, as right after a push.
	Conflicts bool `json:"conflicts,omitempty"`
	Unsettled bool `json:"unsettled,omitempty"`
	// Gate is how CI's gate went on its head: passed, failed, running, or
	// empty when none has run there.
	Gate string `json:"gate,omitempty"`
	// Changed is when what this says last changed, as far as GitHub tells:
	// the later of the pull request's last update and the latest start or
	// end of its gate's runs on its head. A card whose pull request has
	// changed since the factory's post has waited only since then, so one
	// whose gate has passed since isn't folded away as old (#902).
	Changed *time.Time `json:"changed,omitempty"`
}

// RetryCopy is what a card says of its retry, as plain text both pages
// escape: its button's label, the question /act asks before it posts, what
// the retry leads to, and what the page says once GitHub takes it. The
// command it posts is /invariant retry all the same.
type RetryCopy struct {
	Label string `json:"label"`
	// LabelPassed is the label once CI's gate has passed on the pull
	// request's head and nothing keeps it from merging, so the button
	// doesn't speak of a gate still to pass (#902). It's empty where the
	// retry doesn't wait on the gate.
	LabelPassed  string `json:"labelPassed,omitempty"`
	Confirmation string `json:"confirmation"`
	Guidance     string `json:"guidance"`
	Posted       string `json:"posted"`
}

// PlanView is what a plumbing plan says: the change, the files it may
// write, and which of them are in the trusted base.
type PlanView struct {
	Summary string   `json:"summary"`
	Files   []string `json:"files"`
	Trusted []string `json:"trusted,omitempty"`

	Tests      []plumbing.Test     `json:"tests,omitempty"`
	Execution  *plumbing.Execution `json:"execution,omitempty"`
	SourceHash string              `json:"sourceHash,omitempty"`
	SourceURL  string              `json:"sourceURL,omitempty"`
}

// IssuePlanView is what a plan of issues says: what the issues do together,
// and each one's title, in order.
type IssuePlanView struct {
	Summary string   `json:"summary"`
	Issues  []string `json:"issues"`
}

// Question is one fork the factory asked about.
type Question struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind,omitempty"`
	Question string   `json:"question"`
	Options  []Choice `json:"options"`
}

// Choice is one of a question's options.
type Choice struct {
	ID      string              `json:"id"`
	Says    string              `json:"says"`
	Preview *formalize.Preview  `json:"preview,omitempty"`
	Reader  *ReaderPresentation `json:"reader,omitempty"`
	Posters *PosterLinks        `json:"posters,omitempty"` // its chart for people to read (#784)
}

// Said is one statement of a proposal, in plain language.
type Said struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Says string `json:"says"`
	// Behavior is what a bug's statement says of the code (#857): current
	// when the code has the bug today, hypothetical when nothing says it
	// does, and "not classified" for a bug an older lock left without
	// either. No other kind of statement has one.
	Behavior string `json:"behavior,omitempty"`
	// Was is what the lock being amended says of the same bug, when that's
	// something else. Ratifying the amendment changes it.
	Was string `json:"was,omitempty"`
}

// behaviorOf is what a bug's statement says of the code, in the words the
// gate's receipt uses.
func behaviorOf(s project.Statement) string {
	if s.CodeBehavior == "" {
		return "not classified"
	}
	return s.CodeBehavior
}

// amendedStatements is the statements of the lock a proposal amends, when
// the proposal carries them: with its target, or with its chart's
// comparison.
func amendedStatements(p *formalize.Proposal) []project.Statement {
	if p.Target != nil && p.Target.Baseline != nil {
		return p.Target.Baseline.Lock.Statements
	}
	if p.Reader != nil && p.Reader.Comparison != nil {
		return p.Reader.Comparison.Baseline.Lock.Statements
	}
	return nil
}

// Event is one thing that happened: a factory post, a person's command, a
// commit or a CI run.
type Event struct {
	At    time.Time `json:"at"`
	Repo  string    `json:"repo,omitempty"`
	Issue int       `json:"issue,omitempty"`
	Who   string    `json:"who"`          // factory, person or ci
	By    string    `json:"by,omitempty"` // the person's login
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
}

// Span is a stretch of an issue's life, and who it was waiting on.
type Span struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Who  string    `json:"who"`
	Open bool      `json:"open,omitempty"` // still going
}

// takes is factory.Takes as the factory itself decides it (#752). Labels
// need write access to set, but anyone can open an issue that says
// /invariant solve, and the factory takes that only from a writer, or from
// itself when it opens a plan's step.
func takes(is github.Issue) bool {
	for _, l := range is.Labels {
		if l.Name == factory.LabelTrigger || strings.HasPrefix(l.Name, factory.LabelTrigger+":") {
			return true
		}
	}
	bot := is.User.Type == "Bot" || strings.HasSuffix(is.User.Login, "[bot]")
	return factory.Takes(is) && (bot || !outsider(is.AuthorAssociation))
}

// factoryLane is an issue's lane as the page shows it: only markers login
// posted count, as the factory counts only its own, so a marker another
// commenter posts drives no card, preview or comparison (#666). With no
// known login it counts none, and people's comments still count as theirs.
func factoryLane(issue github.Issue, comments []github.Comment, login string, now time.Time) Issue {
	if login == "" {
		comments = slices.DeleteFunc(slices.Clone(comments), func(c github.Comment) bool {
			_, ok := factory.DecodeMarker(c.Body)
			return ok
		})
	}
	return Lane(issue, comments, login, now)
}

// Lane reads an issue's comments the way the factory does. self is the
// factory's bot login; empty means its posts are told apart by their
// markers alone, as they were before it had an App (D-0041).
func Lane(issue github.Issue, comments []github.Comment, self string, now time.Time) Issue {
	opened := parseTime(issue.CreatedAt)
	// Lists, never null: a closed issue with one event has no span, and the
	// page reads both of every issue (#430).
	l := Issue{Number: issue.Number, Title: issue.Title, Open: issue.State == "open", Opened: opened, Events: []Event{}, Spans: []Span{}}
	var said []saidAt
	for _, lab := range issue.Labels {
		if s, ok := stageLabels[lab.Name]; ok {
			l.Stage = s
		}
		switch lab.Name {
		case factory.LabelGo:
			l.Language = "go"
		case factory.LabelTypeScript:
			l.Language = "typescript"
		case factory.LabelPython:
			l.Language = "python"
		}
	}
	l.Events = append(l.Events, Event{At: opened, Issue: issue.Number, Who: WhoPerson, By: issue.User.Login, Kind: "opened", Text: "opened the issue"})
	var last factory.Marker
	var latest *factory.Marker
	var latestAt time.Time
	for _, c := range comments {
		at := parseTime(c.CreatedAt)
		if m, ok := factory.DecodeMarker(c.Body); ok && (self == "" || c.User.Login == self) {
			if m.Kind != factory.KindNote {
				mm := m
				latest, latestAt = &mm, at
			}
			l.SpendUSD += m.Spend
			l.GateRuns += m.GateRuns
			e := Event{At: at, Issue: issue.Number, Who: WhoFactory, Kind: m.Kind, Text: postText(m, last)}
			l.Events = append(l.Events, e)
			if m.Kind != factory.KindNote {
				last = m
			}
			if m.Project != "" {
				l.Project = m.Project
			}
			if m.PR != 0 {
				l.PR = m.PR
			}
			if p := m.Proposal; p != nil {
				l.Statements = len(p.Statements)
				if p.Target != nil && p.Target.Previous != "" {
					l.Amends = p.Target.Previous
				}
				if l.Language == "" {
					l.Language = p.Language
				}
				if l.Project == "" && p.Target != nil {
					l.Project = p.Target.Dir
				}
			}
			if len(m.Forks) > 0 {
				l.Questions = len(m.Forks)
			}
			continue
		}
		if c.User.Type == "Bot" || strings.HasSuffix(c.User.Login, "[bot]") {
			continue
		}
		// The factory obeys only people who can write to the repository,
		// so a stranger's command neither answers a card nor moves its lane.
		if outsider(c.AuthorAssociation) {
			continue
		}
		cmds := factory.ParseCommands(c.Body)
		if len(cmds) == 0 {
			continue
		}
		l.PeopleComments++
		for _, c := range cmds {
			if c.Verb == factory.Choose {
				l.Answers++
			}
		}
		said = append(said, saidAt{at, cmds})
		l.Events = append(l.Events, Event{At: at, Issue: issue.Number, Who: WhoPerson, By: c.User.Login, Kind: cmds[0].Verb, Text: commandText(cmds, latest)})
	}
	sort.SliceStable(l.Events, func(i, j int) bool { return l.Events[i].At.Before(l.Events[j].At) })

	switch {
	case !l.Open && l.Stage == StageMerged:
	case !l.Open:
		l.Stage = StageClosed
	case l.Stage == "":
		l.Stage = StageQueued
	}
	if l.Open && latest != nil {
		l.Waiting = waitingFor(*latest, latestAt)
		// Once people have given what the post asked for, the next move is
		// the factory's.
		var since []factory.Command
		for _, s := range said {
			if s.at.After(latestAt) {
				since = append(since, s.cmds...)
			}
		}
		if l.Waiting != nil && answered(l.Waiting, since) {
			l.Waiting = nil
			// Its label still says a person must act until the factory gets
			// to it, as when it's building another issue first. It's queued.
			if l.Stage == StageAsking || l.Stage == StageRatifying || l.Stage == StageReview {
				l.Stage = StageQueued
			}
		}
	}
	if !l.Open {
		if t := parseTime(issue.ClosedAt); !t.IsZero() {
			l.Closed = &t
		}
	}

	for i := 0; i+1 < len(l.Events); i++ {
		from, to := l.Events[i], l.Events[i+1]
		l.add(Span{From: from.At, To: to.At, Who: spanWho(from, to)})
	}
	if l.Open && len(l.Events) > 0 {
		end := l.Events[len(l.Events)-1]
		who := WhoFactory
		switch l.Stage {
		case StageAsking, StageRatifying, StageReview:
			who = WhoPeople
		case StageGate:
			who = WhoCI
		}
		if now.After(end.At) {
			l.add(Span{From: end.At, To: now, Who: who, Open: true})
		}
	}
	return l
}

func (l *Issue) add(s Span) {
	secs := int(s.To.Sub(s.From).Seconds())
	if secs < 0 {
		return
	}
	l.Spans = append(l.Spans, s)
	if s.Who == WhoPeople {
		l.PeopleSeconds += secs
	} else {
		l.FactorySeconds += secs
	}
}

// spanWho says who the factory was waiting on between two events: on
// people when a person acted next, on CI from the moment it opened or
// resumed watching a pull request, and on its own work otherwise.
func spanWho(from, to Event) string {
	switch {
	case to.Who == WhoPerson:
		return WhoPeople
	case from.Who == WhoFactory && from.Kind == factory.KindPR:
		return WhoCI
	}
	return WhoFactory
}

// saidAt is the commands in one of people's comments, and when.
type saidAt struct {
	at   time.Time
	cmds []factory.Command
}

// answered says whether people's commands since the factory's latest post
// give what it asked for: an answer to every open question, a ratify naming
// the proposal, a retry of what failed, or a revise.
func answered(w *Waiting, cmds []factory.Command) bool {
	chosen := map[string]bool{}
	for _, c := range cmds {
		switch c.Verb {
		case factory.Revise, factory.Retry:
			if slices.Contains(w.Takes, c.Verb) {
				return true
			}
		case factory.Solve:
			if w.Kind == factory.KindStuck || w.Kind == factory.KindClosed {
				return true
			}
		case factory.Ratify:
			if w.Kind == factory.KindProposal && len(c.Args) == 1 && w.Hash != "" {
				named := strings.ToLower(strings.TrimPrefix(c.Args[0], "sha256:"))
				if len(named) >= len(w.Hash) && strings.HasPrefix(named, w.Hash) {
					return true
				}
			}
		case factory.Choose:
			if len(c.Args) == 2 {
				chosen[strings.ToUpper(c.Args[0])] = true
			}
		}
	}
	if w.Kind != factory.KindForks || len(w.Forks) == 0 {
		return false
	}
	for _, q := range w.Forks {
		if !chosen[strings.ToUpper(q.ID)] {
			return false
		}
	}
	return true
}

// waitingFor says what the factory's latest post asks of a person, if
// anything.
func waitingFor(m factory.Marker, at time.Time) *Waiting {
	w := &Waiting{Kind: m.Kind, Since: at}
	switch m.Kind {
	case factory.KindForks:
		for _, f := range m.Forks {
			q := Question{ID: f.ID, Kind: f.Kind, Question: f.Question}
			for _, o := range f.Options {
				q.Options = append(q.Options, Choice{ID: o.ID, Says: o.Says, Preview: o.Preview})
			}
			w.Forks = append(w.Forks, q)
		}
		// Forks already decided in this round don't need asking again.
		decided := map[string]bool{}
		for _, a := range m.Answers {
			decided[a.Fork] = true
		}
		var open []Question
		for _, q := range w.Forks {
			if !decided[q.ID] {
				open = append(open, q)
			}
		}
		w.Forks = open
		w.Takes = []string{factory.Choose, factory.Revise}
	case factory.KindProposal:
		p := m.Proposal
		if p == nil {
			return nil
		}
		w.Name, w.Hash = p.Name, strings.TrimPrefix(p.Hash, "sha256:")
		if p.Plan == nil && p.IssuePlan == nil && p.ModuleText != "" {
			w.Proposal = p
		}
		if len(w.Hash) > 12 {
			w.Hash = w.Hash[:12]
		}
		was, kept := map[string]string{}, map[string]bool{}
		amended := amendedStatements(p)
		for _, s := range amended {
			if s.Kind == project.Bug {
				was[s.Name] = behaviorOf(s)
			}
		}
		for _, s := range p.Statements {
			said := Said{Name: s.Name, Kind: s.Kind, Says: s.Says}
			if s.Kind == project.Bug {
				said.Behavior = behaviorOf(s)
				if old, ok := was[s.Name]; ok && old != said.Behavior {
					said.Was = old
				}
				kept[s.Name] = s.CodeBehavior == "current"
			}
			w.Statements = append(w.Statements, said)
		}
		for _, s := range amended {
			if s.Kind == project.Bug && s.CodeBehavior == "current" && !kept[s.Name] {
				w.Relaxed = append(w.Relaxed, s.Name)
			}
		}
		if pl := p.Plan; pl != nil {
			w.Plan = &PlanView{Summary: pl.Summary, Files: pl.Files, Trusted: pl.Trusted, Tests: pl.Tests, Execution: pl.Execution, SourceHash: p.Hash}
			for _, t := range pl.Tests {
				w.Statements = append(w.Statements, Said{Name: t.Name, Kind: "test", Says: t.Says})
			}
		}
		if ip := p.IssuePlan; ip != nil {
			w.IssuePlan = &IssuePlanView{Summary: ip.Summary, Issues: []string{}}
			for _, is := range ip.Issues {
				w.IssuePlan.Issues = append(w.IssuePlan.Issues, is.Title)
			}
		}
		if p.Target != nil {
			w.Amends = p.Target.Previous
			w.Unchanged = p.Target.Amends != "" && p.Target.Amends == p.Hash
		}
		// A plan, of either kind, pins no statement, and a rebuild at its
		// lock's hash changes none.
		w.Together = w.Unchanged || (len(p.Statements) == 0 && (p.Plan != nil || p.IssuePlan != nil))
		w.Takes = []string{factory.Ratify, factory.Revise}
	case factory.KindFailed:
		w.PR, w.Failure = m.PR, m.Why()
		// A build that stopped before it made a pull request builds or
		// drafts again (#13). One whose pull request is open only retries,
		// which watches it again: the factory drafts again once that pull
		// request is closed, which makes its card a closed one.
		w.Takes = []string{factory.Retry}
		if w.Failure == factory.FailStopped || w.Failure == factory.FailLimit {
			w.Takes = append(w.Takes, factory.Revise)
		}
		w.Retry = retryCopy(m)
		// A retry's build that couldn't reach the plan's ratification can't
		// build it again, so a person drafts the plan again, as its post says
		// (D-0220, D-0238).
		if m.Gone {
			w.Takes, w.Retry, w.Gone = []string{factory.Revise}, nil, true
		}
	case factory.KindStuck:
		// There's no draft to build, so it only drafts again.
		w.Takes = []string{factory.Revise}
	case factory.KindClosed:
		// Its pull request was closed without merging, and the issue is
		// still open: the factory waits for a person to draft again.
		w.PR = m.PR
		w.Takes = []string{factory.Revise}
	default:
		return nil
	}
	return w
}

// retryCopy says what a writer's retry of the failure m records does, as
// the factory takes it (#263). With no pull request, it builds the ratified
// proposal again (D-0064). With one, it watches that pull request again
// (D-0047): once CI's gate passes on its current head, the factory marks it
// ready and merges it, without another review (#145, D-0105), unless it
// changes the trusted base, which only a person merges (D-0109). The
// failure, the plan's trusted files or a project in the trusted base show
// that it does. None can rule out what a later push changes, so the
// factory's merge keeps that exception.
func retryCopy(m factory.Marker) *RetryCopy {
	pr := m.PR
	if pr == 0 {
		return &RetryCopy{
			Label:        "Build ratified proposal again",
			Confirmation: "Have the factory build the ratified proposal again, as you?",
			Guidance:     "Retrying has the factory build the ratified proposal again, from the same ratification, without drafting it again.",
			Posted:       "Asked for a retry. The factory builds the ratified proposal again next.",
		}
	}
	plan := m.Proposal != nil && m.Proposal.Plan != nil
	trusted := m.ChangesTrustedBase()
	if m.Rebuilds() {
		return rebuildCopy(pr, trusted)
	}
	r := &RetryCopy{}
	if trusted {
		r.Label = fmt.Sprintf("Have a person merge #%d once CI passes", pr)
		r.LabelPassed = fmt.Sprintf("Have a person merge #%d", pr)
		r.Confirmation = fmt.Sprintf("Have the factory watch #%d again until CI's gate passes on its current head, as you? A person merges #%d because it changes the trusted base. It won't review #%d again.", pr, pr, pr)
		r.Guidance = fmt.Sprintf("Retrying has the factory watch #%d again until CI's gate passes on its current head. It changes the trusted base, so a person merges it: the factory won't mark it ready or merge it.", pr)
		r.Posted = fmt.Sprintf("Asked for a retry. The factory watches #%d again until CI's gate passes on its current head, then waits for a person to merge it. It won't review #%d again.", pr, pr)
	} else {
		const exception = " If it changes the trusted base, a person merges it."
		r.Label = fmt.Sprintf("Merge #%d once CI passes", pr)
		r.LabelPassed = fmt.Sprintf("Merge #%d", pr)
		r.Confirmation = fmt.Sprintf("Have the factory merge #%d once CI's gate passes on its current head, as you? It won't review #%d again.", pr, pr) + exception
		r.Guidance = fmt.Sprintf("Retrying has the factory watch #%d again. Once CI's gate passes on its current head, and it's in scope with its ratified lock, the factory marks it ready, if it's a draft, once GitHub says it can merge it, and merges it.", pr) + exception
		r.Posted = fmt.Sprintf("Asked for a retry. The factory watches #%d again, then marks it ready and merges it once CI's gate passes on its current head. It won't review #%d again.", pr, pr) + exception
	}
	r.Guidance += fmt.Sprintf(" The factory won't review #%d again, even if its review asked for changes, so push any changes it needs before you retry.", pr)
	if plan {
		r.Guidance += fmt.Sprintf(" To build the plan again instead, close #%d, say \"draft the same plan again\" or what to change, and comment /invariant revise, then ratify the new draft before it's built.", pr)
	} else {
		r.Guidance += fmt.Sprintf(" To build again instead, close #%d, say \"draft the same statements again\" or what to change, and comment /invariant revise, then ratify the new proposal before it's built.", pr)
	}
	return r
}

// rebuildCopy is what a retry of a plan's draft does, when its build failed
// in the factory's own run: it closes the draft and builds the plan again,
// review included, and the new build opens a pull request of its own (#271,
// D-0191, D-0218).
func rebuildCopy(pr int, trusted bool) *RetryCopy {
	label, merge := fmt.Sprintf("Build the plan again, replacing #%d", pr), " If it changes the trusted base, a person merges it."
	if trusted {
		label, merge = label+", for a person to merge", " It changes the trusted base, so a person merges it."
	}
	return &RetryCopy{
		Label:        label,
		Confirmation: fmt.Sprintf("Have the factory close #%d and build the ratified plan again, as you? The new build is checked and reviewed again, and opens a pull request of its own.", pr) + merge,
		Guidance: fmt.Sprintf("Retrying has the factory close #%d, the draft its build left when it failed in the factory's own run, and build the ratified plan again from its ratification, on a branch of its own, with a new review. #%d stays closed, with its work there to read. The new build opens its own pull request: ready once its checks and review pass, or a draft again.", pr, pr) + merge +
			fmt.Sprintf(" To merge #%d as it is instead, push any changes it needs, mark it ready and merge it yourself, and the factory records the merge.", pr) +
			fmt.Sprintf(" To change the plan instead, close #%d, say what to change, or \"draft the same plan again\" to keep it, and comment /invariant revise, then ratify the new draft before it's built.", pr),
		Posted: fmt.Sprintf("Asked for a retry. The factory closes #%d and builds the ratified plan again, and the new build opens its own pull request.", pr) + merge,
	}
}

// postText says what a factory post did. previous is the factory's post
// before it, not counting notes.
func postText(m factory.Marker, previous factory.Marker) string {
	switch m.Kind {
	case factory.KindForks:
		return plural(len(m.Forks), "asked a question", "asked %d questions")
	case factory.KindProposal:
		n := 0
		if p := m.Proposal; p != nil {
			// A plan has no statements, and nothing of it was checked by
			// TLC: it says what it is (#848).
			switch {
			case p.Plan != nil:
				return fmt.Sprintf("proposed a plan: %s to write, %s", plural(len(p.Plan.Files), "1 file", "%d files"), plural(len(p.Plan.Tests), "1 acceptance test", "%d acceptance tests"))
			case p.IssuePlan != nil:
				return plural(len(p.IssuePlan.Issues), "proposed a plan of 1 issue", "proposed a plan of %d issues")
			}
			n = len(p.Statements)
			if p.Target != nil {
				return fmt.Sprintf("proposed an amendment: %d statements", n)
			}
		}
		return fmt.Sprintf("proposed %d statements, checked by TLC", n)
	case factory.KindRatified:
		// A retry after the rebuild's build stopped builds again, and closes
		// nothing: the draft it replaces was closed already (#271).
		if m.Replaces != 0 && previous.PR == m.Replaces {
			return fmt.Sprintf("closed #%d and started building the plan again", m.Replaces)
		}
		if m.Replaces != 0 {
			return "started building the plan again"
		}
		return "committed the ratification, and started writing the code"
	case factory.KindPR:
		if previous.Kind == factory.KindFailed {
			return fmt.Sprintf("watching #%d again", m.PR)
		}
		return fmt.Sprintf("opened #%d with code that passed the gate", m.PR)
	case factory.KindFailed:
		return "needs a person"
	case factory.KindMerged:
		return fmt.Sprintf("merged #%d once CI's gate passed", m.PR)
	case factory.KindClosed:
		return fmt.Sprintf("#%d was closed without merging", m.PR)
	case factory.KindStuck:
		return "couldn't draft statements that check out"
	case factory.KindUnsupported:
		return "can't take this issue"
	case factory.KindNote:
		return "answered a command"
	}
	return m.Kind
}

// commandText says what a person's commands asked for. answered is the
// factory's latest post before them, if any: a retry asked for what its
// failure's card offered then, whatever the factory posted after (#263).
func commandText(cmds []factory.Command, answered *factory.Marker) string {
	switch verb := cmds[0].Verb; verb {
	case factory.Choose:
		var picks []string
		for _, c := range cmds {
			if c.Verb == factory.Choose {
				picks = append(picks, strings.Join(c.Args, " "))
			}
		}
		return "chose " + strings.Join(picks, ", ")
	case factory.Ratify:
		if len(cmds[0].Args) > 0 {
			return "ratified " + strings.TrimPrefix(cmds[0].Args[0], "sha256:")
		}
		return "ratified"
	case factory.Revise:
		return "asked for a revision"
	case factory.Retry:
		if answered == nil || answered.Kind != factory.KindFailed {
			return "asked the factory to try again"
		}
		r := retryCopy(*answered)
		said := "asked to " + strings.ToLower(r.Label[:1]) + r.Label[1:]
		if answered.PR != 0 && !answered.Rebuilds() {
			said += fmt.Sprintf(", and the factory won't review #%d again", answered.PR)
		}
		return said
	case factory.Solve:
		return "asked the factory to take it"
	default:
		return verb
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// stampOf writes a time as GitHub does, and no time as nothing, so that
// parseTime reads it back as it was.
func stampOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
