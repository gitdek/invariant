package factory

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
)

func TestPlanCodeKeepsAdjacentDelimitersLiteral(t *testing.T) {
	// Backslash parity cannot change a table boundary, and backtick runs
	// cannot consume adjacent text. Use the independent GFM/literal oracles.
	for slashes := 0; slashes <= 4; slashes++ {
		for ticks := 1; ticks <= 5; ticks++ {
			value := strings.Repeat("\\", slashes) + "|" + strings.Repeat("`", ticks) +
				"&#124; &#96; &amp; @owner @org/team owner/repo#123 \"quotes\" 'quotes' </code> www.example.invalid\r\nend"
			encoded := planCode(value)
			if !strings.HasPrefix(encoded, "<code>") || strings.ContainsAny(encoded, "\r\n") {
				t.Fatalf("literal escaped its inline code element: %q", encoded)
			}
			cells := cells702("| before | " + encoded + " | after |")
			if len(cells) != 3 || cells[0] != "before" || cells[2] != "after" {
				t.Fatalf("literal changed neighboring cells: %#v", cells)
			}
			if got, rest, ok := literal702(cells[1]); !ok || rest != "" || got != value {
				t.Fatalf("literal displays %q with remainder %q, want %q", got, rest, value)
			}
		}
	}
}

func TestLegacyPlanTableKeepsAuthoredMarkdown(t *testing.T) {
	plan := &plumbing.Plan{
		Name:    "Legacy acceptance",
		Summary: "Keep **authored prose** and [links](https://example.invalid).",
		Files:   []string{"app/main.go"},
		Tests: []plumbing.Test{{
			Name: "TestReady", File: "app/ready_accept_test.go",
			Says: "Shows **ready** for @owner and #123; see [context](https://example.invalid).",
		}},
		Sources: map[string]string{
			"app/ready_accept_test.go": "package app\nimport \"testing\"\nfunc TestReady(t *testing.T) { t.Fatal(\"not ready\") }\n",
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	p := &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name}, Plan: plan, Hash: plan.Hash()}
	body := planComment(p, Marker{Kind: KindProposal, Proposal: p, Hash: p.Hash})
	for _, want := range []string{
		"**What changes.** " + plan.Summary,
		"| Test | Shows |\n| :-- | :-- |\n| `TestReady` in `app/ready_accept_test.go` | " + plan.Tests[0].Says + " |\n\n",
		"```go\n" + plan.Sources[plan.Tests[0].File] + "```",
		"comment `/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")[:12] + "`.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("legacy proposal changed %q", want)
		}
	}
}
