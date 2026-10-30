package dashboard

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
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
	Kind       string     `json:"kind"`                 // forks, proposal or failed
	Failure    string     `json:"failure,omitempty"`    // why it failed: stopped, limit, gate, ci or unmergeable
	Forks      []Question `json:"forks,omitempty"`      // the questions to answer
	Name       string     `json:"name,omitempty"`       // the proposal's project
	Hash       string     `json:"hash,omitempty"`       // the proposal to ratify, as its short hash
	Statements []Said     `json:"statements,omitempty"` // what the proposal says
	Amends     string     `json:"amends,omitempty"`     // what an amendment replaces
	// Unchanged is an amendment whose statements and bounds are exactly
	// those of the lock it amends, so only the code changes.
	Unchanged bool `json:"unchanged,omitempty"`
	// Plan is a proposed or failed plumbing plan (D-0105). A proposal's
	// acceptance tests are in Statements, each of kind test. A failure
	// retains the plan's trusted files for its retry copy.
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
	// card's main action first. The card offers only these, and /act posts
	// only these, so neither offers a command the factory refuses (#218,
	// D-0156).
	Takes []string `json:"takes"`
	// Retry describes the existing retry command, only when Takes offers
	// it. Every field is plain text, escaped by both pages when rendered.
	Retry *RetryView `json:"retry,omitempty"`
}

// RetryView is the consequence a person sees before posting a retry.
type RetryView struct {
	Label        string `json:"label"`
	Confirmation string `json:"confirmation"`
	Guidance     string `json:"guidance"`
}

// PlanView is what a plumbing plan says: the change, the files it may
// write, and which of them are in the trusted base.
type PlanView struct {
	Summary string   `json:"summary"`
	Files   []string `json:"files"`
	Trusted []string `json:"trusted,omitempty"`
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
	Question string   `json:"question"`
	Options  []Choice `json:"options"`
}

// Choice is one of a question's options.
type Choice struct {
	ID   string `json:"id"`
	Says string `json:"says"`
}

// Said is one statement of a proposal, in plain language.
type Said struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Says string `json:"says"`
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

// Lane reads an issue's comments the way the factory does. self is the
// factory's bot login; empty means its posts are told apart by their
// markers alone, as they were before it had an App (D-0041).
func Lane(issue github.Issue, comments []github.Comment, self string, now time.Time) Issue {
	opened := parseTime(issue.CreatedAt)
	l := Issue{Number: issue.Number, Title: issue.Title, Open: issue.State == "open", Opened: opened}
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
	last := ""
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
				last = m.Kind
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
			q := Question{ID: f.ID, Question: f.Question}
			for _, o := range f.Options {
				q.Options = append(q.Options, Choice{ID: o.ID, Says: o.Says})
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
		if len(w.Hash) > 12 {
			w.Hash = w.Hash[:12]
		}
		for _, s := range p.Statements {
			w.Statements = append(w.Statements, Said{Name: s.Name, Kind: s.Kind, Says: s.Says})
		}
		if pl := p.Plan; pl != nil {
			w.Plan = &PlanView{Summary: pl.Summary, Files: pl.Files, Trusted: pl.Trusted}
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
		if p := m.Proposal; p != nil && p.Plan != nil {
			w.Plan = &PlanView{Summary: p.Plan.Summary, Files: p.Plan.Files, Trusted: p.Plan.Trusted}
		}
		// A build that stopped before it made a pull request builds or
		// drafts again (#13). One whose pull request is open only resumes
		// watching: the factory drafts again once that pull request is closed,
		// which makes its card a closed one.
		w.Takes = []string{factory.Retry}
		if w.Failure == factory.FailStopped || w.Failure == factory.FailLimit {
			w.Takes = append(w.Takes, factory.Revise)
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
	if slices.Contains(w.Takes, factory.Retry) {
		w.Retry = retryView(w)
	}
	return w
}

func retryTrusted(w *Waiting) bool {
	return w.Failure == factory.FailTrusted || (w.Plan != nil && len(w.Plan.Trusted) != 0)
}

// retryView explains watching or building without changing which commands
// the factory accepts. A marker cannot establish the scope of a later head,
// so ordinary copy also keeps the trusted-base exception.
func retryView(w *Waiting) *RetryView {
	if w.PR == 0 {
		return &RetryView{
			Label:        "Build ratified proposal again",
			Confirmation: "Have the factory build the ratified proposal again, as you?",
			Guidance:     "Have the factory build the ratified proposal again. It starts another build without drafting or ratifying again.",
		}
	}
	pr := w.PR
	r := &RetryView{}
	if retryTrusted(w) {
		r.Label = fmt.Sprintf("Have a person merge #%d once CI passes", pr)
		r.Confirmation = fmt.Sprintf("Have the factory watch #%d again until CI's gate passes on its current head, as you? A person merges #%d because it changes the trusted base. It won't review #%d again.", pr, pr, pr)
		r.Guidance = fmt.Sprintf("Have the factory watch #%d again until CI's gate passes on its current head. A person merges #%d because it changes the trusted base; the factory does not mark its draft ready or merge it.", pr, pr)
	} else {
		r.Label = fmt.Sprintf("Merge #%d once CI passes", pr)
		r.Confirmation = fmt.Sprintf("Have the factory merge #%d once CI's gate passes on its current head, as you? It won't review #%d again. If it changes the trusted base, a person merges it.", pr, pr)
		r.Guidance = fmt.Sprintf("Have the factory watch #%d again. Once CI's gate passes on its current head and the existing scope, ratification and merge checks allow it, the factory marks a draft ready and merges it. If it changes the trusted base, a person merges it; the factory does not mark that draft ready.", pr)
	}
	r.Guidance += fmt.Sprintf(" It won't review #%d again, even if the earlier review asked for changes. Push any requested fixes before retrying; otherwise it can be merged as-is. Retry watches CI; it does not restart CI jobs.", pr)
	if w.Plan != nil {
		r.Guidance += fmt.Sprintf(" To build again instead, close #%d, say \"draft the same plan again\" in a comment on the issue, then comment /invariant revise. Ratify the resulting draft before building again.", pr)
	} else {
		r.Guidance += fmt.Sprintf(" To build again instead, close #%d, say what the proposal should contain in a comment on the issue, then comment /invariant revise. Ratify the resulting proposal before building again.", pr)
	}
	return r
}

func postText(m factory.Marker, previous string) string {
	switch m.Kind {
	case factory.KindForks:
		return plural(len(m.Forks), "asked a question", "asked %d questions")
	case factory.KindProposal:
		n := 0
		if m.Proposal != nil {
			n = len(m.Proposal.Statements)
			if m.Proposal.Target != nil {
				return fmt.Sprintf("proposed an amendment: %d statements", n)
			}
		}
		return fmt.Sprintf("proposed %d statements, checked by TLC", n)
	case factory.KindRatified:
		return "committed the ratification, and started writing the code"
	case factory.KindPR:
		if previous == factory.KindFailed {
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

func commandText(cmds []factory.Command, previous *factory.Marker) string {
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
		// Use the failure this command answered, before a later factory
		// post can replace it with a different pull request or proposal.
		if previous != nil {
			if w := waitingFor(*previous, time.Time{}); w != nil && w.Retry != nil {
				return "asked to " + strings.ToLower(w.Retry.Label[:1]) + w.Retry.Label[1:] + ". " + strings.TrimPrefix(actedText(w, factory.Retry), "Asked for a retry. ")
			}
		}
		return "asked the factory to try again"
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
