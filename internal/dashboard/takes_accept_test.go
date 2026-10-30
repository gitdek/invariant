package dashboard

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
)

// Each card offers what the factory takes in its state, and /act posts
// exactly that (#218). After a failure, the factory drafts again only once
// nothing of the build is open: a build whose pull request is still open
// only watches it again, and closing that pull request gives the issue a closed
// card, which drafts again. A draft the factory couldn't make has nothing
// to build, so it only drafts again.
func TestCardsOfferOnlyWhatTheFactoryTakes(t *testing.T) {
	for _, c := range []struct {
		name string
		m    factory.Marker
		want []string
	}{
		{"open questions", factory.Marker{Kind: factory.KindForks, Forks: []formalize.Fork{{ID: "F1", Options: []formalize.Option{{ID: "A"}}}}}, []string{factory.Choose, factory.Revise}},
		{"a proposal", factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Hash: "sha256:11bff3f218d7aa"}}, []string{factory.Ratify, factory.Revise}},
		{"a build that stopped", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped}, []string{factory.Retry, factory.Revise}},
		{"two builds that stopped", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailLimit}, []string{factory.Retry, factory.Revise}},
		{"a review that asks for changes, in draft #217", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailGate, PR: 217}, []string{factory.Retry}},
		{"CI's gate failing on #217", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailCI, PR: 217}, []string{factory.Retry}},
		{"#217 changing the trusted base", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailTrusted, PR: 217}, []string{factory.Retry}},
		{"a draft it couldn't make", factory.Marker{Kind: factory.KindStuck}, []string{factory.Revise}},
		{"#217 closed without merging", factory.Marker{Kind: factory.KindClosed, PR: 217}, []string{factory.Revise}},
	} {
		w := waitingFor(c.m, time.Now())
		if w == nil {
			t.Fatalf("%s: no card", c.name)
		}
		if !slices.Equal(w.Takes, c.want) {
			t.Errorf("%s: the card offers %q; the factory takes %q", c.name, w.Takes, c.want)
		}
		issues := []Issue{{Repo: "o/r", Number: 211, Open: true, Waiting: w}}
		for _, verb := range []string{factory.Retry, factory.Revise} {
			err := act{Repo: "o/r", Issue: 211, Body: "/invariant " + verb}.check(issues, nil)
			if takes := slices.Contains(c.want, verb); takes != (err == nil) {
				t.Errorf("%s: /act's check of /invariant %s says %v, where the factory takes it: %v", c.name, verb, err, takes)
			}
		}
		// A revise the factory refuses answers nothing, so the card stays.
		if answered(w, []factory.Command{{Verb: factory.Revise}}) != slices.Contains(c.want, factory.Revise) {
			t.Errorf("%s: a revise counts as an answer where the factory refuses it, or doesn't where it takes it", c.name)
		}
	}
}

// The page's script draws a card's commands from what the server says the
// factory takes, not from whether the card names a pull request.
func TestCardsDrawWhatTheFactoryTakes(t *testing.T) {
	b, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if !strings.Contains(js, ".takes") {
		t.Error("the page's script never reads a card's takes")
	}
	if strings.Contains(js, "Boolean(w.pr)") {
		t.Error("the page's script still offers Draft again on any card with a pull request")
	}
}
