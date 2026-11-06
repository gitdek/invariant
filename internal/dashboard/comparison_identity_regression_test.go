package dashboard

import (
	"bytes"
	"testing"
)

// Evidence without a comparison keeps the identity it had before comparisons
// existed, so its immutable URLs keep resolving after a deploy (#648).
func TestReaderEvidenceWithoutAComparisonKeepsItsIdentity(t *testing.T) {
	b := readerJSON(readerEvidence{Format: readerFormat, Repo: "o/r", Dir: "examples/x", Commit: "abc", Problem: "missing_archive"})
	if bytes.Contains(b, []byte("Comparison")) {
		t.Fatalf("evidence without a comparison names one: %s", b)
	}
}

// A saved comparison link whose proposal is gone could never be served again,
// so restoring the snapshot drops it rather than linking to a missing page
// (#648).
func TestRestoreDropsAComparisonLinkWithoutItsProposal(t *testing.T) {
	s := &Server{Repos: []*Repo{{Name: "o/r"}}}
	snap := &Snapshot{Repo: "o/r", Issues: []Issue{{Number: 1, Waiting: &Waiting{Kind: "proposal", Reader: &ReaderPresentation{Format: comparisonFormat, Page: "/reader/" + string(bytes.Repeat([]byte("a"), 64)) + ".html"}}}}}
	s.restoreComparisons(snap)
	if snap.Issues[0].Waiting.Reader != nil {
		t.Fatal("the restored card still links to a comparison it can't serve")
	}
}
