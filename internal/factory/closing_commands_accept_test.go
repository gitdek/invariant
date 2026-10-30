package factory

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

const closingCommandsOmission = "_Some detail is left out, because GitHub limits how long a comment can be._"

func TestClippedPlanKeepsRatificationInstructions(t *testing.T) {
	// Keep the short-post regression in the same acceptance test as the
	// failing long proposal: a short-only test would already pass on base.
	{
		m := Marker{Kind: KindProposal, Agent: "codex", Hash: "sha256:0123456789abcdef"}
		body := "  A small proposal.\n\n<details>\n<summary>Source</summary>\n\nSome source.\n</details>\n\n" +
			"To ratify exactly this, comment `/invariant ratify 0123456789ab`. " +
			"To change anything, say what in a comment, then comment `/invariant revise`.  \n"
		want := "◉ **Invariant** · plan for ratification\n\n" + strings.TrimSpace(body) +
			"\n\n_Drafted by `codex`._\n\n" + m.encode() + "\n"
		if got := post("plan for ratification", body, m); got != want {
			t.Fatal("a short post changed its text, whitespace, details, byline or marker")
		}
	}

	{
		const file = "internal/example/results_accept_test.go"
		plan := &plumbing.Plan{
			Name:    "Check every result",
			Summary: "Report the full result for every case.",
			Files:   []string{"internal/example/results.go"},
			Sources: map[string]string{},
		}
		// Independent deterministic payloads survive gzip. Repeated padding
		// would leave a tiny marker and would miss the failure in issue #251.
		rng := rand.New(rand.NewSource(251))
		var source, table strings.Builder
		source.WriteString("package example\n\nimport \"testing\"\n\n")
		for i := 0; i < 72; i++ {
			payload := make([]byte, 512)
			if _, err := rng.Read(payload); err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("TestResult%02d", i)
			says := fmt.Sprintf("Case %02d: ", i) + strings.Repeat("The displayed result includes the complete expected text and punctuation. ", 6)
			plan.Tests = append(plan.Tests, plumbing.Test{Name: name, File: file, Says: says})
			fmt.Fprintf(&source, "func %s(t *testing.T) { t.Log(%q) }\n\n", name, base64.StdEncoding.EncodeToString(payload))
			fmt.Fprintf(&table, "| `%s` in `%s` | %s |\n", name, file, says)
		}
		plan.Sources[file] = source.String()
		if err := plan.Validate(); err != nil {
			t.Fatalf("invalid proposal fixture: %v", err)
		}
		p := &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name}, Plan: plan, Hash: plan.Hash()}
		m := Marker{Kind: KindProposal, Proposal: p, Agent: "codex"}
		markerLen := len(m.encode())
		if markerLen < maxComment/2 || markerLen > maxComment-2000 {
			t.Fatalf("fixture needs a large marker with room for instructions, got %d bytes", markerLen)
		}
		if table.Len() <= maxComment-markerLen {
			t.Fatal("fixture's table fits beside its marker; it would not exercise body clipping")
		}
		got := planComment(p, m)
		closingCommandsCheckPost(t, got, m, true)
		want := "To ratify exactly this, comment `/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")[:12] +
			"`. To change anything, say what in a comment, then comment `/invariant revise`."
		closingCommandsCheckInstruction(t, got, want)
		if strings.Contains(got, "<details>") || strings.Contains(got, "</details>") {
			t.Error("the oversized proposal retained collapsed detail")
		}
		if strings.Contains(got, table.String()) {
			t.Error("the fixture did not actually shorten the test table")
		}
		if !strings.Contains(got, "Here's my plan for **Check every result**.") {
			t.Error("shortening discarded the proposal's introduction despite having room")
		}
	}
}

func TestClippedPostsKeepClosingCommands(t *testing.T) {
	long := strings.Repeat("Evidence with café and 界. ", 4000)
	pr := github.PullRequest{Number: 251}
	for _, tc := range []struct {
		name string
		kind string
		make func(Marker) string
		want string
	}{
		{
			name: "choose and revise", kind: KindForks,
			make: func(m Marker) string {
				return forksComment([]formalize.Fork{{ID: "F1", Question: "Which result?", Options: []formalize.Option{{ID: "A", Says: long}}}}, m)
			},
			want: "Answer each one with a comment like `/invariant choose F1 A`, one line per question. Or say what you want in your own words, then comment `/invariant revise`.",
		},
		{
			name: "failed draft", kind: KindStuck,
			make: func(m Marker) string { return stuckComment(long, m) },
			want: "Say what you'd like in a comment, then comment `/invariant revise` to try again.",
		},
		{
			name: "unsupported issue", kind: KindUnsupported,
			make: func(m Marker) string { return unsupportedComment(long, m) },
			want: "If I've misread the issue, say how in a comment, then comment `/invariant revise`.",
		},
		{
			name: "retry and revise without a PR", kind: KindFailed,
			make: func(m Marker) string { return planFailedComment(nil, nil, fmt.Errorf("%s", long), m) },
			want: "A person needs to look at this. Comment `/invariant retry` to build again, or say what to change and comment `/invariant revise`.",
		},
		{
			name: "retry and revise with a draft PR", kind: KindFailed,
			make: func(m Marker) string { return planFailedComment(&pr, nil, fmt.Errorf("%s", long), m) },
			want: "A person needs to look at this. Comment `/invariant retry` to build again. To change the plan instead, close #251, say what to change, and comment `/invariant revise`.",
		},
		{
			name: "retry refused merge", kind: KindFailed,
			make: func(m Marker) string { return mergeRefusedComment(pr, false, fmt.Errorf("%s", long), m) },
			want: "A person needs to look at this. Once the cause is fixed, comment `/invariant retry` and I'll try again.",
		},
		{
			name: "several closing lines", kind: KindNote,
			make: func(m Marker) string {
				return post("note", long+"\n\nComment `/invariant retry` to try again.\nTo request changes, comment `/invariant revise`.\n\nA person must choose the next step.", m)
			},
			want: "Comment `/invariant retry` to try again.\nTo request changes, comment `/invariant revise`.\n\nA person must choose the next step.",
		},
		{
			name: "stop shares a line with long prose", kind: KindPlanOpened,
			make: func(m Marker) string {
				return post("issue opened", long+"I'll open the next step's issue once it merges, unless a writer comments `/invariant stop` here.", m)
			},
			want: "I'll open the next step's issue once it merges, unless a writer comments `/invariant stop` here.",
		},
	} {
		t.Log(tc.name)
		m := Marker{Kind: tc.kind, Agent: "codex", ReplyTo: []int64{251}}
		got := tc.make(m)
		closingCommandsCheckPost(t, got, m, true)
		closingCommandsCheckInstruction(t, got, tc.want)
	}

	{
		m := Marker{Kind: KindProposal}
		instructions := "To ratify exactly this, comment `/invariant ratify abcdef012345`. To change anything, say what in a comment, then comment `/invariant revise`."
		got := post("proposal for ratification", "Keep this explanation.\n\n<details>\n<summary>Source</summary>\n\n"+long+"\n</details>\n\n"+instructions, m)
		want := "◉ **Invariant** · proposal for ratification\n\nKeep this explanation.\n\n" + instructions + "\n\n" + closingCommandsOmission + "\n\n" + m.encode() + "\n"
		if got != want {
			t.Fatal("a post that fits after dropping details changed its existing output")
		}
	}

	{
		m := Marker{Kind: KindNote}
		prefix := "◉ **Invariant** · note\n\n"
		suffix := "\n\n" + closingCommandsOmission + "\n\n" + m.encode() + "\n"
		body := strings.Repeat("x", 2*maxComment)
		want := prefix + body[:maxComment-len(prefix)-len(suffix)] + suffix
		if got := post("note", body, m); got != want {
			t.Fatal("shortening a post with no closing command changed")
		}
	}
}

func closingCommandsCheckPost(t *testing.T, got string, m Marker, omitted bool) {
	t.Helper()
	if len(got) > maxComment {
		t.Errorf("post has %d bytes, beyond its %d-byte budget", len(got), maxComment)
	}
	if !utf8.ValidString(got) {
		t.Error("shortening split a UTF-8 character")
	}
	if omitted && strings.Count(got, closingCommandsOmission) != 1 {
		t.Error("shortened post must retain exactly one existing omission notice")
	}
	if !strings.HasSuffix(got, "\n\n"+m.encode()+"\n") {
		t.Error("shortening changed the encoded marker or its placement")
	}
	decoded, ok := DecodeMarker(got)
	if !ok || !reflect.DeepEqual(decoded, m) {
		t.Error("shortening changed the state or proposal recorded in the marker")
	}
	wantByline := ""
	if m.Agent != "" {
		switch m.Kind {
		case KindForks, KindProposal, KindStuck, KindUnsupported:
			wantByline = "\n\n_Drafted by `" + m.Agent + "`._"
		case KindPR, KindFailed:
			wantByline = "\n\n_Built by `" + m.Agent + "`._"
		}
	}
	if wantByline != "" && !strings.HasSuffix(got, wantByline+"\n\n"+m.encode()+"\n") {
		t.Error("shortening changed the agent's byline or its placement")
	}
}

func closingCommandsCheckInstruction(t *testing.T, got, want string) {
	t.Helper()
	visible := strings.SplitN(got, "<!-- invariant:", 2)[0]
	if strings.Count(visible, want) != 1 {
		t.Errorf("closing instructions must appear whole and once in the visible post:\n%s", want)
	}
}
