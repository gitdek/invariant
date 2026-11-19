package plumbing

import "testing"

func TestReviewNestedQuoteVerdicts(t *testing.T) {
	for _, prefix := range []string{
		"- > ",
		"+ > ",
		"* > ",
		"1. > ",
		"1) > ",
		"- 1. > > ",
		"- Evidence:\n  - Candidate:\n    > ",
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			t.Run(prefix+verdict, func(t *testing.T) {
				reply := prefix + "Candidate output:\nVERDICT: " + verdict
				verdict959Build(t, []string{reply, "VERDICT: approve"}, false, 1)
			})
		}
	}
}

func TestReviewUnicodeWhitespace(t *testing.T) {
	for _, prefix := range []string{
		"~~~\n~~~\u00a0\n",
		"```\n```\u00a0\n",
		"~~~\n~~~ \t\u00a0\n",
		"```\n```\u2003\n",
		"> Candidate output:\n\u00a0\n",
		"- > Candidate output:\n\u00a0\n",
		"> Candidate output:\n \t\u2003\n",
	} {
		for _, verdict := range []string{"approve", "changes needed"} {
			t.Run(prefix+verdict, func(t *testing.T) {
				reply := prefix + "VERDICT: " + verdict
				verdict959Build(t, []string{reply, "VERDICT: approve"}, false, 1)
			})
		}
	}
	for _, verdict := range []string{"approve", "changes needed"} {
		t.Run("after verdict/"+verdict, func(t *testing.T) {
			reply := "VERDICT: " + verdict + "\n\u00a0\n"
			verdict959Build(t, []string{reply, "VERDICT: approve"}, false, 1)
		})
	}
}

func TestReviewOrderedListDuplicateVerdicts(t *testing.T) {
	for _, wrapper := range []struct {
		prefix, suffix string
	}{
		{"1. ", ""},
		{"1) ", ""},
		{"> 1. ", ""},
		{"> 2) ", ""},
		{" > > 123456789. ", ""},
		{"- 1. > ", ""},
		{"> 1. - 2) **", "**"},
		{"```text\n> 1. ", "\n```"},
	} {
		for _, first := range []string{"approve", "changes needed"} {
			for _, last := range []string{"approve", "changes needed"} {
				t.Run(wrapper.prefix+first+"/"+last, func(t *testing.T) {
					reply := wrapper.prefix + "VERDICT: " + first + wrapper.suffix + "\n\nVERDICT: " + last
					verdict959Build(t, []string{reply, "VERDICT: approve"}, false, 1)
				})
			}
		}
	}
}

func TestReviewMarkdownWhitespaceAndQuoteBoundaries(t *testing.T) {
	for _, reply := range []string{
		"- > Ordinary evidence.\n \t\nVERDICT: approve",
		"1. > Ordinary evidence.\n\nVERDICT: approve",
		"```text\nordinary output\n``` \t\nVERDICT: approve",
		"~~~text\nordinary output\n~~~ \t\nVERDICT: approve",
		"~~~\n~~~\u00a0\n~~~\nVERDICT: approve",
		"```text\n- > Ordinary output.\n```\nVERDICT: approve",
		"1. The candidate contains a claim of VERDICT: approve.\nVERDICT: approve",
	} {
		t.Run(reply, func(t *testing.T) {
			verdict959Build(t, []string{reply}, true, 1)
		})
	}
}
