package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
)

// waiting896 is what the page reads of an issue's card.
type waiting896 struct {
	Kind    string   `json:"kind"`
	Hash    string   `json:"hash"`
	Takes   []string `json:"takes"`
	Stopped *struct {
		Comment int64  `json:"comment"`
		URL     string `json:"url"`
		Why     string `json:"why"`
		Repair  string `json:"repair"`
	} `json:"stopped"`
	SavedDraft *struct {
		Problem string `json:"problem"`
	} `json:"savedDraft"`
}

func card896(t *testing.T, l Issue) *waiting896 {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var is struct {
		Waiting *waiting896 `json:"waiting"`
	}
	if err := json.Unmarshal(b, &is); err != nil {
		t.Fatal(err)
	}
	return is.Waiting
}

// The page reads an issue's comments by the factory's own rule (#896). When
// a comment of the factory's own can't be read, the factory stops acting on
// the issue and posts nothing, so the card says the issue is stopped, names
// the comment and offers no command, never the proposal before it. Restored,
// the card is the proposal's again.
func TestAnIssueTheFactoryStoppedOnShowsAsStopped896(t *testing.T) {
	proposal := &formalize.Proposal{Draft: formalize.Draft{Name: "Recording sessions", Statements: []project.Statement{{Name: "OneAtATime", Kind: project.Invariant, Says: "One recording at a time."}}}, Hash: "sha256:" + strings.Repeat("ab", 32)}
	issue := github.Issue{Number: 8, Title: "Recording sessions", State: "open", User: person948, CreatedAt: "2026-11-14T13:00:00Z",
		Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelProposal}}}
	whole := say948(t, 2, "2026-11-14T17:10:22Z", factory.Marker{Kind: factory.KindProposal, Proposal: proposal})
	revised := factory.Marker{Kind: factory.KindProposal, Proposal: proposal}
	newest := say948(t, 4, "2026-11-14T18:00:00Z", revised)
	cut := newest
	cut.Body = newest.Body[:strings.Index(newest.Body, "<!-- invariant:")]
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	ratify := "/invariant ratify " + strings.Repeat("ab", 6)

	for name, comments := range map[string][]github.Comment{
		"the newest cut before its marker": {whole, command948(3, "2026-11-14T17:30:00Z", "Keep it."), cut},
		"an earlier one cut":               {cut, command948(3, "2026-11-14T18:30:00Z", "Keep it."), say948(t, 5, "2026-11-14T19:00:00Z", revised)},
	} {
		t.Run(name, func(t *testing.T) {
			l := Lane(issue, comments, bot948.Login, now)
			w := card896(t, l)
			if w == nil || w.Kind != KindStopped || w.Stopped == nil || w.Stopped.Comment != 4 || w.Stopped.URL != cut.URL || w.Hash != "" || len(w.Takes) != 0 {
				t.Fatalf("the card is %+v; want it stopped at comment 4, naming it, with no command", w)
			}
			if w.Stopped.Why != "the factory's marker is cut short or damaged" || !strings.Contains(w.Stopped.Repair, "restore the comment") {
				t.Errorf("the card says %q and %q; want the factory's reason and what repairs it", w.Stopped.Why, w.Stopped.Repair)
			}
			l.Repo = "gitdek/onair"
			for _, body := range []string{ratify, "/invariant revise", "/invariant retry"} {
				if err := (act{Repo: l.Repo, Issue: 8, Body: body}).check([]Issue{l}, nil); err == nil {
					t.Errorf("the command gate takes %q on an issue the factory has stopped on", body)
				}
			}
		})
	}
	// Restored, the newest comment is the proposal to ratify again.
	l := Lane(issue, []github.Comment{whole, command948(3, "2026-11-14T17:30:00Z", "Keep it."), newest}, bot948.Login, now)
	l.Repo = "gitdek/onair"
	if w := card896(t, l); w == nil || w.Kind != factory.KindProposal || w.Stopped != nil {
		t.Fatalf("the restored card is %+v; want the proposal", w)
	}
	if err := (act{Repo: l.Repo, Issue: 8, Body: ratify}).check([]Issue{l}, nil); err != nil {
		t.Errorf("the restored proposal can't be ratified: %v", err)
	}
}

// The same for a reference whose saved record GitHub says is missing,
// changed or unfit: the factory can't read it either, so the issue is
// stopped there, and the card keeps the record's diagnostic. A read that
// failed for a moment stops nothing: the factory may read it fine (#896).
func TestASavedRecordThatIsGoneStopsTheIssue896(t *testing.T) {
	const repo = "owner/saved"
	ref := factory.DraftReference{Version: 1, Repository: repo, Issue: 9, Step: "draft-issue", Commit: strings.Repeat("c", 40),
		SHA256: "sha256:" + strings.Repeat("d", 64), ProposalHash: "sha256:" + strings.Repeat("e", 64)}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	c := say948(t, 2, "2026-11-15T18:23:03Z", factory.Marker{Kind: factory.KindProposal, ProposalRef: raw})
	issue := github.Issue{Number: 9, Title: "Recording pieces", State: "open", User: person948, CreatedAt: "2026-11-14T17:00:00Z",
		Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelProposal}}}
	r := &Repo{Name: repo, FactoryLogin: bot948.Login, src: sources{comments: map[int]cachedComments{9: {comments: []github.Comment{c}}}}}
	s := &Server{Repos: []*Repo{r}}
	for _, stops := range []bool{true, false} {
		m, _ := factory.DecodeMarker(c.Body)
		d := *draftPresentation(m)
		d.Problem = "draft reference for owner/saved#9: draft-issue is not completed at the referenced commit; retry after restoring the saved record"
		s.proposals.resolved = map[string]savedDraftResolution{savedCommentKey(repo, 9, c): {marker: m, presentation: d, stops: stops}}
		w := card896(t, s.savedLane(issue, r, bot948.Login, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)))
		switch {
		case w == nil || w.SavedDraft == nil || w.SavedDraft.Problem != d.Problem:
			t.Errorf("stops=%v: the card %+v lost the record's diagnostic", stops, w)
		case stops && (w.Kind != KindStopped || w.Stopped == nil || w.Stopped.Comment != 2 || len(w.Takes) != 0):
			t.Errorf("a record that's gone leaves the card %+v; want it stopped at comment 2, with no command", w)
		case !stops && w.Kind == KindStopped:
			t.Errorf("a read that failed for a moment stopped the issue: %+v", w)
		}
	}
}
