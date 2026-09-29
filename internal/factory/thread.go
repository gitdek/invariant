// Package factory turns issues into merged pull requests (slice 4). It reads
// an issue and its comments, asks people about forks, posts a proposal for
// ratification, commits what was ratified, builds the code, opens a pull
// request, and merges it once CI's gate passes. It keeps no state of its
// own: everything it knows is on GitHub, in its own comments, so it can stop
// and start at any time.
package factory

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// People direct the factory with commands, each on a line of its own in an
// issue or a comment.
const (
	Solve  = "solve"  // /invariant solve: take on this issue
	Choose = "choose" // /invariant choose F1 A: decide a fork
	Revise = "revise" // /invariant revise: draft again, reading the comments
	Ratify = "ratify" // /invariant ratify <hash>: ratify the current proposal
	Retry  = "retry"  // /invariant retry: look at a failed pull request again, once people have fixed what failed
	Plan   = "plan"   // /invariant plan: draft a plan of issues for the PRD this issue holds or links
	Stop   = "stop"   // /invariant stop: open no more of the ratified plan of issues on this issue (#126)
)

// Command is one instruction from a person with write access.
type Command struct {
	Verb    string
	Args    []string
	Comment int64 // the comment it came in; 0 for the issue itself
	By      string
	URL     string
	At      string
}

// Takes says whether an issue is one the factory takes on: it carries the
// invariant label or one of the factory's own, or it opens with a
// /invariant solve line. A report that lists the factory's issues uses it;
// the factory itself checks who asked before it acts.
func Takes(is github.Issue) bool {
	for _, l := range is.Labels {
		if l.Name == LabelTrigger || strings.HasPrefix(l.Name, LabelTrigger+":") {
			return true
		}
	}
	return hasVerb(ParseCommands(is.Body), Solve)
}

// ParseCommands finds the commands in a comment's text. A command is a line
// of its own, possibly in backticks, outside code blocks and quotes.
func ParseCommands(body string) []Command {
	var out []Command
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(line, ">") {
			continue
		}
		f := strings.Fields(strings.Trim(line, "`"))
		if len(f) < 2 || f[0] != "/invariant" {
			continue
		}
		switch verb := strings.ToLower(f[1]); verb {
		case Solve, Choose, Revise, Ratify, Retry, Plan, Stop:
			out = append(out, Command{Verb: verb, Args: f[2:]})
		}
	}
	return out
}

// Kinds of factory post. Each records what state the issue is in.
const (
	KindForks       = "forks"       // asked people to decide forks
	KindProposal    = "proposal"    // proposed statements for ratification
	KindStuck       = "stuck"       // couldn't draft statements that check out
	KindUnsupported = "unsupported" // the issue isn't one the factory can take
	KindRatified    = "ratified"    // committed the ratified statements; building. A plan of issues is only recorded
	KindPR          = "pr"          // opened a pull request; waiting for CI's gate
	KindFailed      = "failed"      // the build or CI's gate failed; people need to look
	KindMerged      = "merged"      // merged
	KindClosed      = "closed"      // the pull request was closed without merging
	KindNote        = "note"        // answered a command without changing anything
	// A ratified plan's posts on its issue, which, like notes, leave that
	// issue's state as it was (#126).
	KindPlanOpened = "plan-opened" // recorded the issue opened for one of the plan's steps
	KindPlanDone   = "plan-done"   // every one of the plan's issues has merged
)

// Why the issue's latest failure happened, as a failed post records it
// (#13).
const (
	FailStopped     = "stopped"     // the build stopped before it made a pull request
	FailLimit       = "limit"       // two builds had stopped, so the factory didn't start another
	FailGate        = "gate"        // the code failed the gate in the factory's own run
	FailCI          = "ci"          // CI's gate failed on the pull request
	FailUnmergeable = "unmergeable" // CI's gate passed, but the pull request can't merge
	// FailTrusted is a plan's pull request that passed CI's gate and the
	// review, and changes the trusted base, so a person merges it (D-0105).
	// The protocol counts it as one the factory can't merge.
	FailTrusted = "trusted"
	// FailMerge is a pull request whose gate passed that GitHub refused to
	// merge, or to mark ready for review when it was a draft (#145). The
	// protocol counts it as one the factory can't merge.
	FailMerge = "merge"
)

// maxStops is how many builds of an issue can stop before it makes a pull
// request, until a writer says retry. After that many, the factory doesn't
// start another.
const maxStops = 2

// Marker is the state a factory post records, hidden at its end.
type Marker struct {
	Kind     string              `json:"kind"`
	ReplyTo  []int64             `json:"reply_to,omitempty"` // the commands it answers; 0 is the issue itself
	Forks    []formalize.Fork    `json:"forks,omitempty"`
	Answers  []formalize.Answer  `json:"answers,omitempty"` // the forks decided so far
	Proposal *formalize.Proposal `json:"proposal,omitempty"`
	Project  string              `json:"project,omitempty"` // the project's directory
	Branch   string              `json:"branch,omitempty"`
	Hash     string              `json:"hash,omitempty"` // the ratified proposal
	PR       int                 `json:"pr,omitempty"`
	Failure  string              `json:"failure,omitempty"` // why a failed post failed: Fail*
	// Spend is the agents' estimated cost for the step this post reports,
	// and GateRuns the gate runs its synthesis used. A post that carries an
	// earlier marker forward clears them, so nothing counts twice (D-0048).
	Spend    float64 `json:"spend,omitempty"`
	GateRuns int     `json:"gate_runs,omitempty"`
	// Agent is the coding agent whose run this post reports: the one that
	// drafted or built what it says (#179). A post that carries an earlier
	// marker forward clears it too, since it reports no run.
	Agent   string   `json:"agent,omitempty"`
	Numbers *Numbers `json:"numbers,omitempty"` // the issue's record, on the post that merges it
	// A plan's record names one of its steps, counting from 1, the issue the
	// factory opened for it, and the writer who ratified the plan, on whose
	// authority that issue is solved (#126). Its Hash is the plan's.
	Step     int    `json:"step,omitempty"`
	Opened   int    `json:"opened,omitempty"`
	Ratifier string `json:"ratifier,omitempty"`
}

// carried is the marker a later post carries forward: the same issue state,
// without the step's own numbers or agent.
func (m Marker) carried() Marker {
	m.Spend, m.GateRuns, m.Agent, m.Numbers = 0, 0, "", nil
	return m
}

// Why is why a failed post failed: one of the Fail kinds, or "" for a post
// that didn't fail.
func (m Marker) Why() string { return m.failure() }

// failure is why a failed post failed. Posts from before failures were
// recorded have a pull request when CI failed it, and none when the build
// stopped.
func (m Marker) failure() string {
	switch {
	case m.Kind != KindFailed:
		return ""
	case m.Failure != "":
		return m.Failure
	case m.PR == 0:
		return FailStopped
	}
	return FailCI
}

// The marker is base64 inside an HTML comment, so nothing in it, such as
// TLA+ text, can end the comment early, and GitHub doesn't render it.
// It's gzipped first, marked z:, so a proposal's model still fits in a
// comment. Markers from before that are read as they are.
var markerRE = regexp.MustCompile(`<!-- invariant:(z:)?([A-Za-z0-9+/=]+) -->`)

// maxMarker is the most a marker may unzip to. Anyone can post one, so a
// small one can't make the factory unzip a huge one.
const maxMarker = 4 << 20

func (m Marker) encode() string {
	b, _ := json.Marshal(m)
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	w.Write(b)
	w.Close()
	return "<!-- invariant:z:" + base64.StdEncoding.EncodeToString(z.Bytes()) + " -->"
}

// DecodeMarker reads the marker in a comment, if it has one.
func DecodeMarker(body string) (Marker, bool) {
	match := markerRE.FindStringSubmatch(body)
	if match == nil {
		return Marker{}, false
	}
	b, err := base64.StdEncoding.DecodeString(match[2])
	if err == nil && match[1] == "z:" {
		b, err = unzip(b)
	}
	var m Marker
	if err != nil || json.Unmarshal(b, &m) != nil || m.Kind == "" {
		return Marker{}, false
	}
	return m, true
}

func unzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(r, maxMarker+1))
	if err == nil && len(out) > maxMarker {
		err = errors.New("the marker is too big")
	}
	return out, err
}

// Post is one of the factory's comments.
type Post struct {
	Comment github.Comment
	Marker  Marker
}

// Thread is an issue as the factory reads it.
type Thread struct {
	Issue    github.Issue
	Posts    []Post           // the factory's comments, oldest first
	Commands []Command        // commands from people with write access, oldest first
	People   []github.Comment // comments from people with write access
}

// State is the factory's latest post, not counting notes or a plan's posts
// on its issue. ok is false for an issue the factory hasn't touched.
func (t Thread) State() (post Post, ok bool) {
	for i := len(t.Posts) - 1; i >= 0; i-- {
		switch t.Posts[i].Marker.Kind {
		case KindNote, KindPlanOpened, KindPlanDone:
		default:
			return t.Posts[i], true
		}
	}
	return Post{}, false
}

// Stops is how many builds have stopped before making a pull request since
// a writer's retry last built a stopped build again (#13). A retry of a
// pull request that failed doesn't count them afresh, as the protocol has
// it.
func (t Thread) Stops() int {
	retries := map[int64]bool{}
	for _, c := range t.Commands {
		if c.Verb == Retry {
			retries[c.Comment] = true
		}
	}
	n := 0
	for _, p := range t.Posts {
		if p.Marker.Kind == KindNote {
			continue
		}
		for _, id := range p.Marker.ReplyTo {
			if retries[id] && p.Marker.Kind == KindRatified {
				n = 0
			}
		}
		if p.Marker.failure() == FailStopped {
			n++
		}
	}
	return n
}

// stoppedBuild says whether an issue's latest post says its build stopped
// before it made a pull request.
func stoppedBuild(p Post) bool {
	f := p.Marker.failure()
	return f == FailStopped || f == FailLimit
}

// Pending lists the commands no post has answered yet, oldest first.
func (t Thread) Pending() []Command {
	answered := map[int64]bool{}
	for _, p := range t.Posts {
		for _, id := range p.Marker.ReplyTo {
			answered[id] = true
		}
	}
	var out []Command
	for _, c := range t.Commands {
		if !answered[c.Comment] {
			out = append(out, c)
		}
	}
	return out
}
