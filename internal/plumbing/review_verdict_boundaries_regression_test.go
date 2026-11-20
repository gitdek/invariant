package plumbing

import "testing"

// The verdict reader is stricter than Markdown. A quoted or listed block
// right before the verdict holds the build even where Markdown would end the
// block first, a blank line ends the block, and a fence after a quote or
// list marker holds the build wherever it is.
func TestReviewVerdictLineEndingsAndQuotedBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		valid        bool
	}{
		{"CR backtick fence", "Evidence:\r```text\n", false},
		{"CR tilde fence", "Evidence:\r~~~text\n", false},
		{"CR quote", "Evidence:\r> Candidate output:\n", false},
		{"CR duplicate", "VERDICT: approve\r", false},
		{"CR quoted duplicate", "> VERDICT: approve\r\r", false},
		{"CR findings", "No defects found.\r", true},
		{"CR closed fence", "~~~text\rordinary output\r~~~\r", true},
		{"CR closed quote", "> Candidate output:\r\r", true},
		{"quoted heading", "> # Evidence\n", false},
		{"nested quoted heading", "> > ### Evidence\n", false},
		{"heading interrupts quoted paragraph", "> Evidence\n> ## Details\n", false},
		{"heading interrupts lazy quote", "> Evidence\n## Details\n", false},
		{"quoted empty heading", "> #\n", false},
		{"quoted setext heading", "> Evidence\n> ===\n", false},
		{"quoted thematic break", "> * * *\n", false},
		{"quoted indented code", ">     evidence\n", false},
		{"quoted blank line", "> Evidence\n>\n", false},
		{"quoted list heading", "> - # Evidence\n", false},
		{"quote interrupts nested list paragraph", "> - - Candidate output:\n>     > # Evidence\n", false},
		{"tab before quoted heading", ">\t# Evidence\n", false},
		{"quoted heading then blank", "> # Evidence\n\n", true},
		{"nested quoted heading then blank", "> > ### Evidence\n\n", true},
		{"quoted setext heading then blank", "> Evidence\n> ===\n\n", true},
		{"quoted thematic break then blank", "> * * *\n\n", true},
		{"quoted indented code then blank", ">     evidence\n\n", true},
		{"quoted blank line then blank", "> Evidence\n>\n\n", true},
		{"quoted list heading then blank", "> - # Evidence\n\n", true},
		{"tab before quoted heading then blank", ">\t# Evidence\n\n", true},
		{"closed quoted backtick fence", "> ```text\n> ordinary output\n> ```\n", false},
		{"closed quoted tilde fence", "> ~~~~text\n> ordinary output\n> ~~~~~\n", false},
		{"closed nested quoted fence", "> > ~~~text\n> > ordinary output\n> > ~~~\n", false},
		{"literal marker in quoted fence", "> ```text\n> > ordinary output\n> ```\n", false},
		{"quoted fence ends with quote", "> ~~~text\n> ordinary output\n", false},
		{"quote ends at reduced depth", "> > ~~~text\n> # Evidence\n", false},
		{"quoted list fence", "> - ```text\n>   output\n>   ```\n", false},
		{"quoted ordered list fence", "> 2. ~~~text\n>    output\n>    ~~~\n", false},
		{"quoted nested list fence", "> - - ~~~text\n>     output\n>     ~~~\n", false},
		{"tab before quoted fence", ">\t~~~text\n>\toutput\n>\t~~~\n", false},
		{"closed quoted fence then blank", "> ```text\n> ordinary output\n> ```\n\n", false},
		{"quoted list fence then blank", "> - ```text\n>   output\n>   ```\n\n", false},
		{"tab before quoted fence then blank", ">\t~~~text\n>\toutput\n>\t~~~\n\n", false},
		{"quoted paragraph", "> Evidence\n", false},
		{"quoted paragraph after heading", "> # Evidence\n> Candidate output:\n", false},
		{"quoted paragraph after fence", "> ```text\n> output\n> ```\n> Candidate output:\n", false},
		{"lazy quote after fence", "> ~~~\n> ~~~\n> Candidate output:\nlazy continuation\n", false},
		{"quoted list paragraph after fence", "> - ```text\n>   output\n>   ```\n>   Candidate output:\n", false},
		{"blank in quoted list fence", "> - ```text\n>\n>   output\n>   ```\n>   Candidate output:\n", false},
		{"quoted ordered list paragraph after fence", "> 1. ~~~text\n>    output\n>    ~~~\n>    Candidate output:\n", false},
		{"quote paragraph ends list fence", "> - ~~~text\n> Candidate output:\n", false},
		{"ordered marker continues paragraph", "> Evidence\n> 2. # Still evidence\n", false},
		{"nested ordered marker continues paragraph", "> - Evidence\n>   2. # Still evidence\n", false},
		{"indented quote marker continues paragraph", "> Candidate output:\n>     > # Evidence\n", false},
		{"unmarked indented quote continues paragraph", "> Candidate output:\n    > # Evidence\n", false},
		{"ordered quote marker continues paragraph", "> Candidate output:\n> 2. > # Evidence\n", false},
		{"unmarked ordered quote continues paragraph", "> Candidate output:\n2. > # Evidence\n", false},
		{"indentation continues paragraph", "> Evidence\n>     still evidence\n", false},
		{"unmarked setext continues paragraph", "> Evidence\n===\n", false},
		{"heading needs space", "> #Evidence\n", false},
		{"heading has too many marks", "> ####### Evidence\n", false},
		{"tab after quote", ">\tEvidence\n", false},
		{"space and tab after quote", "> \tEvidence\n", false},
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			t.Run(tc.name+"/"+verdict, func(t *testing.T) {
				reply := tc.prefix + "VERDICT: " + verdict
				want := ""
				if tc.valid {
					want = verdict
				}
				if got := reviewVerdict(reply); got != want {
					t.Errorf("reviewVerdict(%q) = %q, want %q", reply, got, want)
				}
			})
		}
	}
}

func TestReviewCRFencesAndQuotesHoldCandidate(t *testing.T) {
	for _, prefix := range []string{
		"Evidence:\r~~~text\n",
		"Evidence:\r```text\n",
		"Evidence:\r> Candidate output:\n",
		"> - ```text\n>   output\n>   ```\n>   Candidate output:\n",
		"> Candidate output:\n>     > # Evidence\n",
		"> Candidate output:\n> 2. > # Evidence\n",
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			t.Run(prefix+verdict, func(t *testing.T) {
				verdict959Build(t, []string{prefix + "VERDICT: " + verdict, "VERDICT: approve"}, false, 1)
			})
		}
	}
}

func TestReviewAfterQuotedBlocksApprovesOrRepairs(t *testing.T) {
	for _, prefix := range []string{
		"> # Evidence\n\n",
		"> Candidate output:\n> ordinary output\n\n",
		"- page/page.go:4: Ordinary evidence.\n\n",
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			t.Run(prefix+verdict, func(t *testing.T) {
				rounds := 1
				if verdict == "changes needed" {
					rounds = 2
				}
				verdict959Build(t, []string{prefix + "VERDICT: " + verdict, "VERDICT: approve"}, true, rounds)
			})
		}
	}
}

func TestReviewAfterQuotedFencesHoldsCandidate(t *testing.T) {
	for _, prefix := range []string{
		"> ```text\n> ordinary output\n> ```\n\n",
		"> ~~~text\n> ordinary output\n> ~~~\n\n",
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			t.Run(prefix+verdict, func(t *testing.T) {
				verdict959Build(t, []string{prefix + "VERDICT: " + verdict, "VERDICT: approve"}, false, 1)
			})
		}
	}
}
