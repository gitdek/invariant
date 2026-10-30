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
		body := "  A small proposal.\n\n<details>\n<summary>Source</summary>\n\nSome source.\n</details>\n"
		closing := "\nTo ratify exactly this, comment `/invariant ratify 0123456789ab`. " +
			"To change anything, say what in a comment, then comment `/invariant revise`.  \n"
		want := "◉ **Invariant** · plan for ratification\n\n" + strings.TrimSpace(body+closing) +
			"\n\n_Drafted by `codex`._\n\n" + m.encode() + "\n"
		if got := closingCommandsPost(t, "plan for ratification", body, m, closing); got != want {
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
			name: "CI never started", kind: KindFailed,
			make: func(m Marker) string {
				return ciFailedComment(pr, github.CheckRun{URL: "https://example.test/" + strings.Repeat("x", 2*maxComment)}, gateNeverStarted, m)
			},
			want: "Once CI can run, re-run the job, then comment `/invariant retry`.",
		},
		{
			name: "CI start unknown", kind: KindFailed,
			make: func(m Marker) string {
				return ciFailedComment(pr, github.CheckRun{URL: "https://example.test/" + strings.Repeat("x", 2*maxComment)}, gateUnknown, m)
			},
			want: "give it the Actions read-only permission to let it tell. If CI couldn't start, re-run the job once it can, then comment `/invariant retry`. Otherwise, a person needs to look at this.",
		},
		{
			name: "unavailable coding agent", kind: KindUnsupported,
			make: func(m Marker) string { return unrunnableComment(long, []string{"codex"}, m) },
			want: "To go on, name an agent this watcher can run in the issue's `Agent:` line, such as `Agent: codex`, then comment `/invariant revise`.",
		},
		{
			name: "several closing lines", kind: KindNote,
			make: func(m Marker) string {
				return closingCommandsPost(t, "note", long, m, "\n\nComment `/invariant retry` to try again.\nTo request changes, comment `/invariant revise`.\n\nA person must choose the next step.")
			},
			want: "Comment `/invariant retry` to try again.\nTo request changes, comment `/invariant revise`.\n\nA person must choose the next step.",
		},
		{
			name: "stop shares a line with long prose", kind: KindPlanOpened,
			make: func(m Marker) string {
				return planOpenedComment(PlanOfIssues{By: "gitdek", Issues: []PlannedIssue{{Title: long}, {Title: "Next step"}}}, 0, 251, m)
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

	// The other proposal constructors must also supply their instructions.
	// Long recorded answers exercise clipping without exceeding the text
	// limit on the plan of issues itself.
	p := &formalize.Proposal{Draft: formalize.Draft{Name: "Check results", Language: "go"}, Hash: "sha256:" + strings.Repeat("12", 32)}
	amendment := *p
	amendment.Target = &formalize.Target{Dir: "examples/results", Manifest: p.Manifest(), Previous: "#1", Amends: p.Hash}
	issues := &formalize.IssuePlan{Name: "Check results", Summary: "Show the result.", Issues: []formalize.PlannedIssue{{Title: "Show the result", Body: "Show the complete result.\n\nKind: plumbing"}}}
	if err := issues.Validate(); err != nil {
		t.Fatal(err)
	}
	ip := &formalize.Proposal{Draft: formalize.Draft{Name: issues.Name}, IssuePlan: issues, Hash: issues.Hash()}
	for _, tc := range []struct {
		proposal *formalize.Proposal
		make     func(*formalize.Proposal, Marker) string
	}{
		{p, func(p *formalize.Proposal, m Marker) string { return proposalComment(p, nil, m) }},
		{&amendment, func(p *formalize.Proposal, m Marker) string { return amendmentComment(p, nil, nil, m) }},
		{ip, issuePlanComment},
	} {
		m := Marker{Kind: KindProposal, Agent: "codex", Proposal: tc.proposal,
			Answers: []formalize.Answer{{Fork: "F1", Question: "Which result?", Option: "A", Says: long, By: "gitdek"}}}
		got := tc.make(tc.proposal, m)
		closingCommandsCheckPost(t, got, m, true)
		closingCommandsCheckInstruction(t, got, "To ratify exactly this, comment `/invariant ratify "+strings.TrimPrefix(tc.proposal.Hash, "sha256:")[:12]+
			"`. To change anything, say what in a comment, then comment `/invariant revise`.")
	}

	// These instructions were inline before the API change. Supplying them
	// separately must not introduce a paragraph break or change their words.
	for _, tc := range []struct {
		got   string
		title string
		body  string
	}{
		{
			got:   ciFailedComment(pr, github.CheckRun{URL: "https://example.test/run"}, gateNeverStarted, Marker{Kind: KindNote}),
			title: "needs a person",
			body:  "CI couldn't start `invariant/gate` on #251 ([run](https://example.test/run)). GitHub never gave the job a runner, so the gate didn't run, and I won't merge it. Once CI can run, re-run the job, then comment `/invariant retry`.",
		},
		{
			got:   planOpenedComment(PlanOfIssues{By: "gitdek", Issues: []PlannedIssue{{Title: "First step"}, {Title: "Next step"}}}, 0, 251, Marker{Kind: KindNote}),
			title: "issue opened",
			body:  "I opened #251 for step 1 of 2 of this plan: **First step**. I'll solve it on the authority of @gitdek, who ratified the plan. I'll open the next step's issue once it merges, unless a writer comments `/invariant stop` here.",
		},
		{
			got:   closedComment(pr, Marker{Kind: KindNote}),
			title: "closed",
			body:  "#251 was closed without merging, so I've stopped. Comment `/invariant revise` to start again from a new proposal.",
		},
		{
			got:   unrunnableComment("unknown", []string{"codex"}, Marker{Kind: KindNote}),
			title: "not one I can take",
			body:  "This issue's coding agent is `unknown`, and this watcher can't run it. It can run `codex`. An issue's `Agent:` line picks its agent, else the manifest of the project it changes does, else it gets the watcher's own.\n\nSo I haven't drafted anything, and I've left this issue for a person. To go on, name an agent this watcher can run in the issue's `Agent:` line, such as `Agent: codex`, then comment `/invariant revise`.",
		},
	} {
		m := Marker{Kind: KindNote}
		want := "◉ **Invariant** · " + tc.title + "\n\n" + tc.body + "\n\n" + m.encode() + "\n"
		if tc.got != want {
			t.Errorf("short %q post changed its original output", tc.title)
		}
	}

	{
		m := Marker{Kind: KindProposal}
		instructions := "To ratify exactly this, comment `/invariant ratify abcdef012345`. To change anything, say what in a comment, then comment `/invariant revise`."
		got := closingCommandsPost(t, "proposal for ratification", "Keep this explanation.\n\n<details>\n<summary>Source</summary>\n\n"+long+"\n</details>", m, "\n\n"+instructions)
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

func TestPostsKeepOnlyExplicitClosingInstructions(t *testing.T) {
	// The closing input is optional so existing posts without instructions
	// and previously pinned acceptance tests keep their three-argument API.
	if _, ok := any(post).(func(string, string, Marker, ...string) string); !ok {
		t.Error("post must accept closing text separately from its body")
	}
	m := Marker{Kind: KindNote, ReplyTo: []int64{251}}
	long := strings.Repeat("Ordinary evidence with café and 界. ", 4000)
	for _, closing := range []string{
		"\n\nBefore trying this again, read the failure report and\nfix the cause, then comment\n`/invariant retry` to build again.\nTo change the request instead, say what you want and\ncomment `/invariant revise`.\n\nA person must choose the next step.",
		"\n\nAfter reviewing the evidence,\ncomment `/invariant future-command case-251` to continue.",
	} {
		got := closingCommandsPost(t, "note", long, m, closing)
		closingCommandsCheckPost(t, got, m, true)
		closingCommandsCheckInstruction(t, got, strings.TrimSpace(closing))
		if !strings.HasSuffix(got, closing+"\n\n"+closingCommandsOmission+"\n\n"+m.encode()+"\n") {
			t.Error("the complete caller-supplied closing must precede the omission notice and marker")
		}
	}

	// A diagnostic can quote an instruction without asking the reader to
	// carry it out. With no supplied closing, these retain the old output.
	diagnostic := "> Diagnostic recorded: Comment `/invariant retry` to try again.\n> This quotes a previous run; no action is requested."
	for _, body := range []string{
		diagnostic,
		strings.Repeat("x", 2*maxComment) + "\n\n" + diagnostic,
		diagnostic + "\n\n" + strings.Repeat("y", 2*maxComment),
	} {
		prefix := "◉ **Invariant** · note\n\n"
		suffix := "\n\n" + m.encode() + "\n"
		want := prefix + body + suffix
		if len(want) > maxComment {
			suffix = "\n\n" + closingCommandsOmission + suffix
			want = prefix + strings.TrimSpace(body[:maxComment-len(prefix)-len(suffix)]) + suffix
		}
		for _, got := range []string{post("note", body, m), closingCommandsPost(t, "note", body, m, ""), noteComment(body, m)} {
			if got != want {
				t.Error("a quoted command changed a post that supplies no closing instructions")
			}
		}
	}

	// Explicit instructions do not promote commands quoted elsewhere in
	// the body, even when the diagnostic resembles a closing instruction.
	body := strings.Repeat("z", 2*maxComment) + "\n\n" + diagnostic
	closing := "\n\nAfter reading the report,\ncomment `/invariant revise` to change the request."
	got := closingCommandsPost(t, "note", body, m, closing)
	suffix := closing + "\n\n" + closingCommandsOmission + "\n\n" + m.encode() + "\n"
	prefix := "◉ **Invariant** · note\n\n"
	want := prefix + body[:maxComment-len(prefix)-len(suffix)] + suffix
	if got != want {
		t.Error("only the explicitly supplied closing should be reserved, leaving the body its remaining budget")
	}

	// buildFailedComment's arbitrary error is diagnostic body text too.
	// Its ordinary no-action footer does not turn a quoted retry into a
	// request; factory-authored stopped-build guidance is supplied separately.
	failed := Marker{Kind: KindFailed, Agent: "codex"}
	got = buildFailedComment(nil, nil, fmt.Errorf("%s", body), failed)
	prefix = "◉ **Invariant** · needs a person\n\n"
	suffix = "\n\n" + closingCommandsOmission + "\n\n_Built by `codex`._\n\n" + failed.encode() + "\n"
	failureBody := "I couldn't build the code: " + body + "\n\nA person needs to look at this."
	want = prefix + failureBody[:maxComment-len(prefix)-len(suffix)] + suffix
	if got != want {
		t.Error("a command quoted in an error changed a failure post with no supplied instructions")
	}
}

// This bridge lets the regressions execute on the base API as well as the
// intended optional-closing API. On base, the suffix is ordinary body text
// and the long-post assertions fail; the test above also requires the new
// API, so retaining the old signature cannot satisfy the contract.
func closingCommandsPost(t *testing.T, title, body string, m Marker, closing string) string {
	t.Helper()
	switch write := any(post).(type) {
	case func(string, string, Marker, ...string) string:
		return write(title, body, m, closing)
	case func(string, string, Marker) string:
		return write(title, body+closing, m)
	default:
		t.Fatal("unexpected post signature")
		return ""
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
