package dashboard

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/factory"
)

// A command posted from /act, its Ratify all or the agent's door shows at
// once, on every page (#175). Once GitHub takes a command that gives its
// issue what it waits for, the server keeps it, and serves the snapshot it
// last read again, with that issue waiting on no one, queued as D-0093 shows
// an issue whose person has acted, and the command shown posted. Each
// refresh forgets every command whose comment it has read, and serves what
// it read with the rest still applied, so the factory's reading takes over
// once GitHub lists the comment, and a forgotten command never shows again.

// Acted is a command GitHub took on an issue, as the page shows it.
type Acted struct {
	Command string    `json:"command"` // the command lines, without the agent's note
	Text    string    `json:"text"`    // what it asked for, and what the factory does next
	URL     string    `json:"url"`     // the comment GitHub made
	At      time.Time `json:"at"`      // when GitHub took it
}

// acted is the commands the server keeps, and the snapshot it last read,
// which it serves with them applied. mu is held while a snapshot is
// published, so a refresh and a post never publish over each other.
type acted struct {
	mu   sync.Mutex
	read *Snapshot // as the latest refresh read it, or as the last run left it
	kept []kept
}

// kept is a command GitHub took on repo#issue, which answers the factory's
// post of since.
type kept struct {
	repo  string
	issue int
	since time.Time
	Acted
}

// took keeps a command GitHub took, as the comment at url, when it gives its
// issue what it waits for, as the factory reads it, and at once serves every
// page the issue acted on. One that gives it less, such as an answer to one
// of two questions, leaves it waiting.
func (s *Server) took(c act, url string) {
	k := kept{repo: c.Repo, issue: c.Issue, Acted: Acted{Command: strings.TrimSpace(c.Body), URL: url, At: time.Now().UTC()}}
	cmds := factory.ParseCommands(k.Command)
	s.acted.mu.Lock()
	defer s.acted.mu.Unlock()
	if s.acted.read == nil {
		return
	}
	for _, is := range s.acted.read.Issues {
		if is.Repo != k.repo || is.Number != k.issue || is.Waiting == nil || !answered(is.Waiting, cmds) {
			continue
		}
		k.since, k.Text = is.Waiting.Since, actedText(is.Waiting, cmds[0].Verb)
		s.acted.kept = append(s.acted.kept, k)
		if _, err := s.serve(nil); err != nil {
			s.logf("act: showing what was posted on %s#%d: %v", k.repo, k.issue, err)
		}
		return
	}
}

// actedText says what a command asked for, and what the factory does next,
// by its first command, which is the one the factory takes.
func actedText(w *Waiting, verb string) string {
	switch verb {
	case factory.Ratify:
		if w.IssuePlan != nil {
			return "Ratified. The factory opens its first issue next."
		}
		return "Ratified. The factory builds it next."
	case factory.Choose:
		return "Answered. The factory drafts again next."
	case factory.Retry:
		return "Asked for a retry. The factory tries again next."
	}
	return "Asked for a new draft. The factory drafts again next."
}

// publish keeps snap as what a refresh read, forgets every command whose
// comment the refresh read among its issue's comments, and serves snap with
// the rest applied. It returns what it serves, gzipped. Only a refresh reads
// the comments, so only a refresh publishes.
func (s *Server) publish(snap Snapshot) ([]byte, error) {
	s.acted.mu.Lock()
	defer s.acted.mu.Unlock()
	left := s.acted.kept[:0]
	for _, k := range s.acted.kept {
		if !s.readComment(k) {
			left = append(left, k)
		}
	}
	s.acted.read, s.acted.kept = &snap, left
	return s.serve(nil)
}

// resume serves the last run's snapshot, gzipped as gz, until the first
// refresh replaces it, unless one already has.
func (s *Server) resume(snap Snapshot, gz []byte) {
	s.acted.mu.Lock()
	defer s.acted.mu.Unlock()
	if s.acted.read == nil {
		s.acted.read = &snap
		s.serve(gz) // nothing is kept yet, so it serves gz as it is
	}
}

// readComment says whether a refresh has read k's comment, among its issue's
// comments.
func (s *Server) readComment(k kept) bool {
	for _, r := range s.Repos {
		if r.Name != k.repo {
			continue
		}
		for _, c := range r.src.comments[k.issue].comments {
			if c.URL == k.URL {
				return true
			}
		}
	}
	return false
}

// serve publishes the snapshot last read, with every command still kept
// applied, as every page reads it, and returns it gzipped. gz, when the
// caller has it, is the snapshot last read, gzipped, which it serves as it
// is while no command applies. The caller holds s.acted.mu.
func (s *Server) serve(gz []byte) ([]byte, error) {
	snap, applied := s.acted.apply(*s.acted.read)
	if applied || gz == nil {
		js, err := json.Marshal(snap)
		if err != nil {
			return nil, err
		}
		if gz, err = gzipped(js); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.state, s.issues = gz, snap.Issues
	s.mu.Unlock()
	return gz, nil
}

// apply is snap with every kept command applied whose issue still waits on
// the post it answers: the issue waits on no one and shows the command, and
// it's queued, since its label says a person must act until the factory
// gets to it (D-0093). It says whether any applied, and leaves snap's issues
// as they were read.
func (a *acted) apply(snap Snapshot) (Snapshot, bool) {
	applied := false
	for _, k := range a.kept {
		for i, is := range snap.Issues {
			if is.Repo != k.repo || is.Number != k.issue || is.Waiting == nil || !is.Waiting.Since.Equal(k.since) {
				continue
			}
			if !applied {
				snap.Issues, applied = append([]Issue(nil), snap.Issues...), true
			}
			shown := k.Acted
			l := &snap.Issues[i]
			l.Waiting, l.Acted = nil, &shown
			if l.Stage == StageAsking || l.Stage == StageRatifying || l.Stage == StageReview {
				l.Stage = StageQueued
			}
		}
	}
	return snap, applied
}
