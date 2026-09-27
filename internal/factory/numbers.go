package factory

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// Numbers is what an issue took, read from its thread (D-0048, 5.4): the
// factory's time, CI included; the time it waited on people; the comments
// people directed it with; the agents' estimated spend; and the gate runs
// synthesis used.
type Numbers struct {
	FactorySeconds int     `json:"factory_seconds"`
	PeopleSeconds  int     `json:"people_seconds"`
	PeopleComments int     `json:"people_comments"`
	SpendUSD       float64 `json:"spend_usd,omitempty"`
	GateRuns       int     `json:"gate_runs,omitempty"`
}

// NumbersOf counts what the thread took, from the issue's opening to end.
// Time before a person's command was spent waiting on people. All other
// time is the factory's, CI's gate included.
func NumbersOf(t Thread, end time.Time) Numbers {
	type event struct {
		at     time.Time
		person bool
	}
	opened, _ := time.Parse(time.RFC3339, t.Issue.CreatedAt)
	events := []event{{at: opened}}
	var n Numbers
	comments := map[int64]bool{}
	for _, c := range t.Commands {
		if c.Comment == 0 {
			continue // the issue's own solve line, or its label
		}
		at, _ := time.Parse(time.RFC3339, c.At)
		events = append(events, event{at: at, person: true})
		comments[c.Comment] = true
	}
	for _, p := range t.Posts {
		at, _ := time.Parse(time.RFC3339, p.Comment.CreatedAt)
		events = append(events, event{at: at})
		n.SpendUSD += p.Marker.Spend
		n.GateRuns += p.Marker.GateRuns
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
	for i, e := range events {
		next, person := end, false
		if i+1 < len(events) {
			next, person = events[i+1].at, events[i+1].person
		}
		secs := int(next.Sub(e.at).Seconds())
		switch {
		case secs <= 0:
		case person:
			n.PeopleSeconds += secs
		default:
			n.FactorySeconds += secs
		}
	}
	n.PeopleComments = len(comments)
	return n
}

// Sentence says the numbers in plain words, for the merge comment.
func (n Numbers) Sentence() string {
	s := fmt.Sprintf("It took %s of factory time, CI included, and %s from people.", minutes(n.FactorySeconds), plural(n.PeopleComments, "comment"))
	if n.SpendUSD > 0 {
		s += fmt.Sprintf(" The agents' estimated spend was $%.2f", n.SpendUSD)
		if n.GateRuns > 0 {
			s += fmt.Sprintf(", and synthesis used %s", plural(n.GateRuns, "gate run"))
		}
		s += "."
	}
	return s
}

func minutes(secs int) string {
	if secs < 90 {
		return plural(secs, "second")
	}
	return plural((secs+30)/60, "minute")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ThreadFrom reads an issue's thread from its comments alone, counting every
// person's commands. It's for reports, such as the ledger. The factory
// itself checks who may direct it before it acts (read).
func ThreadFrom(issue github.Issue, comments []github.Comment, self string) Thread {
	t := Thread{Issue: issue}
	for _, c := range comments {
		if m, ok := DecodeMarker(c.Body); ok && (self == "" || c.User.Login == self) {
			t.Posts = append(t.Posts, Post{Comment: c, Marker: m})
			continue
		}
		if c.User.Type == "Bot" || strings.HasSuffix(c.User.Login, "[bot]") {
			continue
		}
		t.People = append(t.People, c)
		for _, cmd := range ParseCommands(c.Body) {
			cmd.Comment, cmd.By, cmd.URL, cmd.At = c.ID, c.User.Login, c.URL, c.CreatedAt
			t.Commands = append(t.Commands, cmd)
		}
	}
	return t
}
