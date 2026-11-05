package factory

import (
	"html"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestPublishedTablesPreserveLiteralTildes(t *testing.T) {
	const says = "Keep ~~pending~~ literal and ~ready~ too."
	const escapedSays = "Keep &#126;&#126;pending&#126;&#126; literal and &#126;ready&#126; too."
	const label = "Ready ~~now~~"
	const escapedLabel = "Ready &#126;&#126;now&#126;&#126;"
	p := bufferProposal(t)
	p.Statements[1].Says = says
	if err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	// Exercise the renderers with literal wording in each kind of table.
	p.Reader = &formalize.Reader{Version: 1, Chart: &statechart.Result{
		States:  []statechart.State{{ID: "ready", Label: label, UnavailableActions: []string{"Take"}}},
		Initial: []string{"ready"},
		Report: statechart.Report{
			Rules: p.Statements,
			Hidden: []statechart.Arrow{{
				From: "ready", To: "ready", Label: "Wait ~~here~~", HidingRules: []string{"poll"},
			}},
		},
	}}
	proposal := proposalComment(p, nil, Marker{Kind: KindProposal, Proposal: p})
	p.Target = &formalize.Target{Dir: "projects/buffer", Amends: p.Hash, Previous: "#1", Manifest: p.Manifest()}
	old := p.Statements[1]
	old.Says = "Keep ~~old~~ literal."
	removed := project.Statement{Name: "Removed", Kind: project.Invariant, Says: "Keep ~~removed~~ literal."}
	added := project.Statement{Name: "Added", Kind: project.Invariant, Says: "Keep ~~added~~ literal."}
	changes := &formalize.Changes{Statements: []formalize.Change{
		{Name: old.Name, How: formalize.Changed, Old: &old, New: &p.Statements[1]},
		{Name: removed.Name, How: formalize.Removed, Old: &removed},
		{Name: added.Name, How: formalize.Added, New: &added},
	}}
	amendment := amendmentComment(p, nil, changes, Marker{Kind: KindProposal, Proposal: p})
	for _, tc := range []struct {
		name string
		body string
		rows []string
	}{
		{"proposal", proposal, []string{
			"| `WithinCap` | invariant | " + escapedSays + " |",
		}},
		{"amendment", amendment, []string{
			"| `WithinCap` | changed invariant | " + escapedSays + " *(was: Keep &#126;&#126;old&#126;&#126; literal.)* |",
			"| `Removed` | **removed** invariant | Keep &#126;&#126;removed&#126;&#126; literal. |",
			"| `Added` | added invariant | Keep &#126;&#126;added&#126;&#126; literal. |",
		}},
		{"reader", readerComment(p), []string{
			"| " + escapedLabel + " | Take |  |",
			"| WithinCap | invariant | " + escapedSays + " |",
			"| " + escapedLabel + " | Wait &#126;&#126;here&#126;&#126; | " + escapedLabel + " |  | poll |",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, row := range tc.rows {
				if !strings.Contains(tc.body, row+"\n") {
					t.Errorf("missing literal table row %q:\n%s", row, tc.body)
				}
			}
			for _, line := range strings.Split(tc.body, "\n") {
				if strings.HasPrefix(line, "| ") && strings.Contains(line, "~") {
					t.Errorf("unescaped tilde can become strikethrough: %s", line)
				}
			}
			if !strings.Contains(html.UnescapeString(tc.body), says) {
				t.Error("escaping changed the statement wording")
			}
			if !strings.Contains(html.UnescapeString(tc.body), label) {
				t.Error("escaping changed the state label")
			}
		})
	}
}
