package factory

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

// A saved record's digest shows only that its bytes are the ones saved, and
// ratification writes where the proposal says. A correctly digested record
// with an unsafe directory or slug must leave the pending ratification
// waiting, with nothing built.
func TestSavedDraft790UnsafeSavedPathsStayPending(t *testing.T) {
	for _, c := range []struct {
		name, problem string
		edit          func(*formalize.Proposal)
	}{
		{"parent directory", "saved project directory", func(p *formalize.Proposal) { p.Dir = "../outside" }},
		{"bare parent", "saved project directory", func(p *formalize.Proposal) { p.Dir = ".." }},
		{"absolute directory", "saved project directory", func(p *formalize.Proposal) { p.Dir = "/tmp/outside" }},
		{"unclean directory", "saved project directory", func(p *formalize.Proposal) { p.Dir = "examples/x/../../../outside" }},
		{"CI configuration", "saved project directory", func(p *formalize.Proposal) { p.Dir = ".github/workflows" }},
		{"climbing slug", "saved proposal slug", func(p *formalize.Proposal) { p.Slug = "a/../../../outside" }},
		{"empty slug", "saved proposal slug", func(p *formalize.Proposal) { p.Slug = "" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			result := saved790Result(t, false)
			c.edit(result.Proposal)
			r := newRig(t)
			saved790Open(t, r)
			ref := saved790Save(t, r, result, "draft-issue")
			saved790Post(t, r, ref, result.Proposal)
			saved790Fresh(t, r)
			if _, err := r.f.read(t.Context(), *r.gh.issues[790]); err == nil || !strings.Contains(err.Error(), c.problem) {
				t.Fatalf("a correctly digested unsafe path was not refused as one: %v", err)
			}
			r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
			before := len(r.gh.comments[790])
			if err := r.f.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), c.problem) {
				t.Fatalf("unsafe saved path has no actionable diagnostic: %v", err)
			}
			if len(r.gh.comments[790]) != before || len(r.form.requests) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatal("unsafe saved path consumed the ratification, ran an agent or started a build")
			}
		})
	}
}

// An authenticated envelope that names a saved record but no longer parses
// must block like any broken reference. Read as no marker, it would let the
// earlier proposal answer the pending ratification.
func TestSavedDraft790MalformedEnvelopesStayPending(t *testing.T) {
	result := saved790Result(t, false)
	r := newRig(t)
	saved790Open(t, r)
	r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: result.Proposal, ReplyTo: []int64{0}}).encode())
	ref := saved790Save(t, r, result, "draft-issue")
	posted := saved790Post(t, r, ref, result.Proposal)
	index := len(r.gh.comments[790]) - 1
	saved790Fresh(t, r)
	saved790Read(t, r, result.Proposal)
	r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
	envelope := reader603JSON(t, saved790Envelope(ref, result.Proposal))
	cut := bytes.Index(envelope, []byte(`"sha256"`))
	if cut < 0 {
		t.Fatal("fixture envelope has no digest to cut at")
	}
	for _, c := range []struct {
		name string
		raw  []byte
	}{
		{"trailing object", append(bytes.Clone(envelope), `{"kind":"proposal"}`...)},
		{"trailing garbage", append(bytes.Clone(envelope), "garbage"...)},
		{"cut short", bytes.Clone(envelope[:cut])},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := "◉ **Invariant** · saved draft\n\n<!-- invariant:" + base64.StdEncoding.EncodeToString(c.raw) + " -->"
			if _, ok := DecodeMarker(body); !ok {
				t.Fatal("a malformed reference envelope read as no marker")
			}
			r.gh.comments[790][index].Body = body
			before := len(r.gh.comments[790])
			if _, err := r.f.read(t.Context(), *r.gh.issues[790]); err == nil {
				t.Fatal("malformed reference envelope fell back to the earlier proposal")
			}
			if _, err := r.f.postsOn(t.Context(), 790); err == nil {
				t.Fatal("parent-step reader ignored a malformed reference envelope")
			}
			if _, err := r.f.superseded(t.Context(), 790, Post{Comment: posted}); err == nil {
				t.Fatal("build recovery reader ignored a malformed reference envelope")
			}
			err := r.f.Poll(t.Context())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "reference") {
				t.Fatalf("malformed envelope has no actionable diagnostic: %v", err)
			}
			if len(r.gh.comments[790]) != before || len(r.form.requests) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatal("malformed envelope consumed the pending ratification or started work")
			}
		})
	}
	// Repairing the comment, with no new command, lets the same pending
	// ratification run exactly once.
	r.gh.comments[790][index].Body = posted.Body
	r.poll()
	if len(r.build.built) != 1 || len(r.form.requests) != 0 {
		t.Fatal("repaired envelope did not run the original pending ratification")
	}
	r.poll()
	if len(r.build.built) != 1 {
		t.Fatal("repaired ratification built twice")
	}
}
