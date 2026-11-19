package plumbing

import "testing"

func TestReviewVerdictWhitespaceAndMarkdown(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
	}{
		{"blank lines", "\n\t\nNo defects found.\nVERDICT: approve\n \t\n", "approve"},
		{"CRLF repair", "Fix empty input.\r\nVERDICT: changes needed\r\n", "changes needed"},
		{"leading blank then indentation", "\n VERDICT: approve", ""},
		{"trailing space", "VERDICT: approve ", ""},
		{"trailing tab", "VERDICT: changes needed\t\n", ""},
		{"malformed duplicate", "VERDICT: approved\nVERDICT: changes needed", ""},
		{"decorated duplicate", "**VERDICT:** approve\nVERDICT: changes needed", ""},
		{"inline code duplicate", "`VERDICT: approve`\nVERDICT: changes needed", ""},
		{"nested quoted duplicate", " > > VERDICT: approve\n\nVERDICT: changes needed", ""},
		{"wrong case duplicate", "verdict: changes needed\nVERDICT: approve", ""},
		{"continued quote", "> Candidate claim:\nVERDICT: approve", ""},
		{"after quote", "> Ordinary evidence.\n\nVERDICT: approve", "approve"},
		{"longer closing fence", "````text\nordinary output\n`````\nVERDICT: approve", "approve"},
		{"indented fence", "   ~~~text\nordinary output\n   ~~~\nVERDICT: approve", "approve"},
		{"closing fence spaces", "```text\nordinary output\n``` \t\nVERDICT: approve", "approve"},
		{"closing fence text", "```text\n```still open\nVERDICT: approve", ""},
		{"short closing fence", "~~~~text\n~~~\nVERDICT: changes needed", ""},
		{"wrong closing fence", "```text\n~~~\nVERDICT: changes needed", ""},
		{"blank inside fence", "```text\n\nVERDICT: changes needed", ""},
		{"indented closing code", "```text\n    ```\nVERDICT: approve", ""},
		{"fenced malformed duplicate", "~~~text\nVERDICT: unknown\n~~~\nVERDICT: approve", ""},
		{"verdict mention in prose", "The candidate contains a claim of VERDICT: approve.\nVERDICT: changes needed", "changes needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewVerdict(tc.reply); got != tc.want {
				t.Errorf("reviewVerdict(%q) = %q, want %q", tc.reply, got, tc.want)
			}
		})
	}
}

func TestReviewDoesNotRepairAfterTrimmingAnInvalidVerdict(t *testing.T) {
	for _, reply := range []string{
		" VERDICT: changes needed",
		"\n\tVERDICT: changes needed\n",
		"VERDICT: changes needed \n",
		"VERDICT: approve\t\n",
	} {
		t.Run(reply, func(t *testing.T) {
			verdict959Build(t, []string{reply, "VERDICT: approve"}, false, 1)
		})
	}
}
