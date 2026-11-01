package decisions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshProposalFromSelectedCheckout(t *testing.T) {
	s, repo := fixture(t)
	id, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: "Review this proposal."}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(repo.JournalDir(), id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	proposal := "sha256:" + strings.Repeat("a", 64)
	if err := s.RatifyProposal(repo, id, "@reviewer", proposal); err != nil {
		t.Fatal(err)
	}
	// Another checkout ratifies the same decision without naming a proposal.
	other := Repo{Name: repo.Name, Dir: t.TempDir()}
	write(t, filepath.Join(other.JournalDir(), id+".jsonl"), string(first))
	if err := s.Ratify(other, id, "@other"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		repo     Repo
		proposal string
		who      string
	}{
		{repo, proposal, "@reviewer"},
		{other, "", "@other"},
		{repo, proposal, "@reviewer"},
	} {
		if err := s.Refresh(tc.repo, id); err != nil {
			t.Fatal(err)
		}
		n, err := s.Get(repo.Name + "/" + id)
		if err != nil {
			t.Fatal(err)
		}
		if n.Proposal != tc.proposal || n.Who != tc.who || n.Status != "ratified" {
			t.Errorf("refresh: %+v, want proposal %q by %s", n, tc.proposal, tc.who)
		}
	}
}
