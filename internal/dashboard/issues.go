package dashboard

import (
	"fmt"
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
}

// Event is one thing that happened: a factory post, a person's command, a
// commit or a CI run.
type Event struct {
	At    time.Time `json:"at"`
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
	for _, c := range comments {
		at := parseTime(c.CreatedAt)
		if m, ok := factory.DecodeMarker(c.Body); ok && (self == "" || c.User.Login == self) {
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
		l.Events = append(l.Events, Event{At: at, Issue: issue.Number, Who: WhoPerson, By: c.User.Login, Kind: cmds[0].Verb, Text: commandText(cmds)})
	}
	sort.SliceStable(l.Events, func(i, j int) bool { return l.Events[i].At.Before(l.Events[j].At) })

	switch {
	case !l.Open && l.Stage == StageMerged:
	case !l.Open:
		l.Stage = StageClosed
	case l.Stage == "":
		l.Stage = StageQueued
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

func commandText(cmds []factory.Command) string {
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
