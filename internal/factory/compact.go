package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
)

var errCompactSize = errors.New("The indispensable decision text and complete charts still exceed the comment or expanded marker size limit. Nothing actionable was published; the full saved record is retained.")

// errLegacyDraft is a completed record that was saved without its
// publication, as records were before a draft's run kept it. A reference
// holds a saved record to the check context saved with it, which that record
// lacks, so it can't be published by reference: its caller refuses the
// overflow as it does with no dashboard, and a revision drafts a record that
// can.
var errLegacyDraft = errors.New("the completed draft was saved without its publication, so no reference can name it")

func completeComment(body string, m Marker) bool {
	_, ok := DecodeMarker(m.encode())
	return ok && len(body) <= maxComment
}

// An inline question can first exceed a limit when its marker gains answers.
// Its original full-question post names the command that completed the draft;
// subsequent partial-choice posts name answers, not new draft runs.
func questionDraftStep(t Thread, p *formalize.Proposal) string {
	for i := len(t.Posts) - 1; i >= 0; i-- {
		m := t.Posts[i].Marker
		if m.Kind == KindForks && len(m.Forks) == len(p.Forks) && draftEqual(m.Proposal, p) && len(m.ReplyTo) > 0 {
			return draftStepName(Command{Comment: m.ReplyTo[len(m.ReplyTo)-1]})
		}
	}
	return ""
}

// compactDraft is used only after the complete inline comment exceeds a
// publication limit. Read back the completed record through the same resolver
// that will later authorize answers and ratification; never shorten its data.
func (f *Factory) compactDraft(ctx context.Context, issue int, step string, m Marker) (Marker, string, error) {
	var ref DraftReference
	if m.ProposalRef == nil {
		state, commit, err := f.Repo.Run(ctx, issue, step)
		if err != nil {
			return Marker{}, "", err
		}
		if state != RunDone {
			return Marker{}, "", fmt.Errorf("compact publication needs a completed saved draft")
		}
		payload, err := f.Repo.Show(ctx, commit, "result.json")
		if err != nil {
			return Marker{}, "", err
		}
		if fields, err := draftObject(payload); err == nil && (fields["publication"] == nil || string(fields["publication"]) == "null") {
			return Marker{}, "", errLegacyDraft
		}
		ref = DraftReference{Version: 1, Repository: f.Repository, Issue: issue, Step: step,
			Commit: commit, SHA256: draftDigest(payload), ProposalHash: m.Proposal.Hash}
		m.ProposalRef, err = json.Marshal(ref)
		if err != nil {
			return Marker{}, "", err
		}
	} else if err := json.Unmarshal(m.ProposalRef, &ref); err != nil {
		return Marker{}, "", err
	}
	m, result, err := m.ResolveDraft(ctx, f.Repository, issue, f.Repo.Run, f.Repo.Show)
	if err != nil {
		return Marker{}, "", err
	}
	link, err := ref.URL(f.DashboardURL)
	if err != nil {
		return Marker{}, "", err
	}
	// An amendment shows what changes in TLA+ itself. A proposal, or a
	// question whose alternatives have statements, leaves their TLA+ to the
	// record and says so beside the link, so a compact comment never leaves
	// out unsaid what a person ratifies or chooses.
	evidence := func(statements string) string {
		return "\n\n[Complete saved draft and evidence](" + link + ")." + statements +
			" The exact saved record retains full sources, occurrence tables, full comparison evidence, hidden steps and provenance.\n"
	}
	var body string
	switch p := result.Proposal; {
	case m.Kind == KindForks:
		statements := ""
		if slices.ContainsFunc(m.Forks, func(f formalize.Fork) bool {
			return slices.ContainsFunc(f.Options, func(o formalize.Option) bool { return o.Preview != nil })
		}) {
			statements = " Each alternative's statements are in that record in TLA+, not in this comment."
		}
		body = compactForksComment(m.Forks, m, evidence(statements))
	case p.Target != nil:
		changes := formalize.Diff(p.Target.Baseline, p)
		body = amendmentCommentWithEvidence(p, result.Report, changes, m, evidence(""))
	default:
		body = proposalCommentWithEvidence(p, result.Report, m, evidence(" The statements' TLA+ is in that record, not in this comment."))
	}
	if !completeComment(body, m) {
		return Marker{}, "", errCompactSize
	}
	return m, body, nil
}

// compactStatements lists a model's statements as a proposal's own table
// does. A name is written in a code span, which shows its text as it is: an
// entity there would be read as the name's own characters.
func compactStatements(p *formalize.Proposal) string {
	var b strings.Builder
	b.WriteString("| Statement | Kind | Says |\n| :-- | :-- | :-- |\n")
	for _, s := range p.Statements {
		says := tableLiteral(s.Says)
		if s.Expect != "" {
			says += fmt.Sprintf(" It must break `%s`.", s.Expect)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", s.Name, tableLiteral(s.Kind), says)
	}
	return b.String()
}

func compactAnswers(answers []formalize.Answer) string {
	if len(answers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n**Decided on this issue:**\n\n")
	for _, a := range answers {
		fmt.Fprintf(&b, "- %s. %s **%s.** %s (@%s)\n", tableLiteral(a.Fork), tableLiteral(a.Question), tableLiteral(a.Option), tableLiteral(a.Says), tableLiteral(a.By))
	}
	return b.String()
}

func compactForksComment(forks []formalize.Fork, m Marker, evidence string) string {
	var b strings.Builder
	if m.Proposal.HasPreviews() {
		b.WriteString("These are unratified alternatives with model-only evidence and candidate rules. Decide which behavior you want:\n")
	} else {
		b.WriteString("Before I write down what must be true for this issue, I need you to decide:\n")
	}
	for _, f := range forks {
		fmt.Fprintf(&b, "\n**%s. %s**\n", tableLiteral(f.ID), tableLiteral(f.Question))
		for _, o := range f.Options {
			fmt.Fprintf(&b, "\n**%s.** %s\n", tableLiteral(o.ID), tableLiteral(o.Says))
			if v := o.Preview; v != nil {
				b.WriteString("\n" + compactStatements(v.Model))
				fmt.Fprintf(&b, "\n**Already checked** against this unratified model-only alternative, within %s: %s\n", bounds(v.Model.Bounds), checked(v.Receipt))
				b.WriteString(readerCommentWithEvidence(v.Model, evidence))
				if v.Model.Reader.Chart == nil {
					for _, fork := range slices.Sorted(maps.Keys(v.Choices)) {
						fmt.Fprintf(&b, "\nPreviously authorized choice: %s = %s.\n", tableLiteral(fork), tableLiteral(v.Choices[fork]))
					}
				}
			}
		}
	}
	b.WriteString(compactAnswers(m.Answers))
	b.WriteString(evidence)
	example := forks[0].ID + " " + forks[0].Options[0].ID
	closing := fmt.Sprintf("\n\nAnswer each one with a comment like `/invariant choose %s`, one line per question. "+
		"Or say what you want in your own words, then comment `/invariant revise`.", example)
	return post("a decision for you", strings.TrimSuffix(b.String(), "\n"), m, closing)
}
