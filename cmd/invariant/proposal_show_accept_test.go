package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gitdek/invariant/internal/decisions"
)

const cliProposal358 = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func showOutput358(t *testing.T, args ...string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdout
	err = func() error {
		os.Stdout = f
		defer func() { os.Stdout = old }()
		return askCmd(t.Context(), "show", args)
	}()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDecisionsShowReportsRatifiedProposalInTextAndJSON(t *testing.T) {
	root := repoWithOrigin(t, "https://github.com/example/demo.git", "README.md", "Demo.\n")
	repo := decisions.Repo{Name: "demo", Dir: root}
	s, err := decisions.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, d := range []decisions.NewDecision{
		{Door: "one-way", Status: "proposed", Who: "agent", Text: "Approve the reviewed proposal.", Record: "review"},
		{Door: "two-way", Status: "decided", Who: "agent", Text: "Ratify without a proposal."},
		{Door: "two-way", Status: "ratified", Who: "@recorder", Text: "Recorded as ratified; text mentions " + cliProposal358},
		{Door: "two-way", Status: "proposed", Who: "agent", Text: "Still proposed."},
		{Door: "two-way", Status: "decided", Who: "agent", Text: "Still decided."},
		{Door: "two-way", Status: "proposed", Who: "agent", Text: "Replaced after ratification."},
	} {
		if _, err := s.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"D-0001", "D-0006"} {
		if err := s.RatifyProposal(repo, id, "@reviewer", cliProposal358); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Ratify(repo, "D-0002", "@reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := s.Supersede(repo, "D-0006", "D-0005", "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.Link(repo, "D-0001", decisions.Edge{Type: decisions.Refines, To: "D-0005"}, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.Link(repo, "D-0004", decisions.Edge{Type: decisions.Cites, To: "D-0001"}, "agent"); err != nil {
		t.Fatal(err)
	}
	// The CLI must build this separate store from the checkout on first use.
	storePath := filepath.Join(t.TempDir(), "cli.db")
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("D-%04d", i)
		n, err := s.Get("demo/" + id)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"-C", root, "-project", "demo", "-store", storePath}
		want := fmt.Sprintf("%s · %s · %s · %s · %s", n.ID, n.Door, n.Status, n.Date, n.Who)
		if i == 1 {
			want += " · proposal " + cliProposal358
		}
		want += "\n" + n.Text + "\n"
		edge := func(kind, target, text string) string {
			return fmt.Sprintf("  %-11s %-46s %s\n", kind, target, text)
		}
		if i == 1 {
			want += "Record: decisions/D-0001-review.md\n\nFrom it:\n" + edge("refines", "demo/D-0005", "Still decided.") + "\nTo it:\n" + edge("cites", "demo/D-0004", "Still proposed.")
		}
		if i == 4 {
			want += "\nFrom it:\n" + edge("cites", "demo/D-0001", "Approve the reviewed proposal.")
		}
		if i == 5 {
			want += "\nFrom it:\n" + edge("supersedes", "demo/D-0006", "Replaced after ratification.") + "\nTo it:\n" + edge("refines", "demo/D-0001", "Approve the reviewed proposal.")
		}
		if i == 6 {
			want += "\nTo it:\n" + edge("supersedes", "demo/D-0005", "Still decided.")
		}
		for _, requested := range []string{id, "demo/" + id} {
			if got := showOutput358(t, append(args, requested)...); got != want {
				t.Errorf("show %s:\ngot: %s\nwant: %s", requested, got, want)
			}
		}
		var got struct {
			Node  map[string]any       `json:"node"`
			Edges []decisions.Neighbor `json:"edges"`
		}
		if err := json.Unmarshal([]byte(showOutput358(t, append(args, "-json", id)...)), &got); err != nil {
			t.Fatal(err)
		}
		wantNode := map[string]any{"id": n.ID, "kind": n.Kind, "project": n.Project, "date": n.Date, "door": n.Door, "status": n.Status, "who": n.Who, "text": n.Text}
		if n.Record != "" {
			wantNode["record"] = n.Record
		}
		if i == 1 {
			wantNode["proposal"] = cliProposal358
		}
		if !reflect.DeepEqual(got.Node, wantNode) {
			t.Errorf("show -json %s: node = %#v, want %#v", id, got.Node, wantNode)
		}
		near, err := s.Neighbors(n.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Edges, near) {
			t.Errorf("show -json %s changed its edges: %#v, want %#v", id, got.Edges, near)
		}
	}
}
