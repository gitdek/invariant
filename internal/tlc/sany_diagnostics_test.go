package tlc

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func sanyFrame(code int, body string) string {
	return fmt.Sprintf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
}

func TestParseSANYMultilineContext(t *testing.T) {
	const diagnosis = "***Parse Error***\n" +
		"Encountered \"====\" at line 9, column 1 and token \"/\\\"\n\n" +
		"Was expecting a TLA+ expression.\n\n" +
		"Residual stack trace follows:\n" +
		"  Expression starting at line 7, column 9.\n" +
		"  Definition starting at line 7, column 1."
	const abort = "Fatal errors while parsing TLA+ spec in file Model.tla\n\n" +
		"*** Abort messages: 1\n\nCould not parse module Model from file Model.tla"
	const closing = "Parsing or semantic analysis failed."
	const before = "***Parse Error***\nlauncher text outside SANY"
	const after = "***Parse Error***\nmodel text outside SANY"
	first := "Parsing file /work/Model.tla\n" + diagnosis
	second := abort + "\n"
	out := before + "\n" + sanyFrame(codeSANYStart, "Starting SANY...") + first + "\n" +
		sanyFrame(codeRunning, "checker status") + second + sanyFrame(codeSANYEnd, "SANY finished.") +
		after + "\n" + sanyFrame(codeSANYFailed, closing) + sanyFrame(1000, "Additional TLC context.")
	r := Parse(out, 150)
	want := diagnosis + "\n\n" + abort + "\n" + closing + "\nAdditional TLC context."
	if r.Outcome != Failed || r.Message != want {
		t.Fatalf("Outcome = %s, Message = %q; want error with %q", r.Outcome, r.Message, want)
	}
	if want := []string{before, first, strings.TrimSpace(second), after}; !reflect.DeepEqual(r.Printed, want) {
		t.Errorf("Printed = %q; want %q", r.Printed, want)
	}
}

func TestParseSANYErrorsBesideWarnings(t *testing.T) {
	const warning = "*** Warnings: 1\n\nline 4, col 1 to line 4, col 4 of module Model\n" +
		"Multiple declarations or definitions for symbol x.\n"
	const first = "Semantic errors:\n\n*** Errors: 1\n\n" +
		"line 12, col 9 to line 12, col 15 of module Helpers\n\nUnknown operator: `Missing'."
	const second = "Semantic errors:\n\n*** Errors: 1\n\n" +
		"line 6, col 5 to line 6, col 9 of module Model\n\nUnknown operator: `Other'."
	const closing = "Parsing or semantic analysis failed."
	printed := "Parsing file /work/Model.tla\nSemantic processing of module Helpers\n" + warning +
		first + "\n" + warning + "Semantic processing of module Model\n" + second + "\n" +
		"Linting of module Model\nSemantic warnings:\n" + warning
	out := sanyFrame(codeSANYStart, "Starting SANY...") + printed +
		sanyFrame(codeSANYEnd, "SANY finished.") + sanyFrame(codeSANYFailed, closing)
	r := Parse(out, 150)
	if want := first + "\n" + second + "\n" + closing; r.Outcome != Failed || r.Message != want {
		t.Fatalf("Outcome = %s, Message = %q; want error with %q", r.Outcome, r.Message, want)
	}
	if want := []string{strings.TrimSpace(printed)}; !reflect.DeepEqual(r.Printed, want) {
		t.Errorf("Printed = %q; want %q", r.Printed, want)
	}
}

func TestParseSANYDiagnosticsNeedFailure(t *testing.T) {
	const diagnostics = "***Parse Error***\nline 9, col 1 of module Model\nExpected an expression.\n"
	for _, tc := range []struct {
		name    string
		output  string
		exit    int
		outcome Outcome
		message string
	}{
		{
			name:   "no SANY phase",
			output: diagnostics + sanyFrame(codeSANYFailed, "Parsing or semantic analysis failed."),
			exit:   150, outcome: Failed, message: "Parsing or semantic analysis failed.",
		},
		{
			name: "unrelated failure",
			output: sanyFrame(codeSANYStart, "Starting SANY...") + diagnostics +
				sanyFrame(codeSANYEnd, "SANY finished.") + sanyFrame(1000, "Unrelated TLC failure."),
			exit: 1, outcome: Failed, message: "Unrelated TLC failure.",
		},
		{
			name: "success",
			output: sanyFrame(codeSANYStart, "Starting SANY...") + diagnostics +
				sanyFrame(codeSANYEnd, "SANY finished.") + sanyFrame(codeSuccess, "No error has been found."),
			exit: 0, outcome: Passed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Parse(tc.output, tc.exit)
			if r.Outcome != tc.outcome || r.ExitCode != tc.exit || r.Message != tc.message {
				t.Errorf("got %s, exit %d, message %q; want %s, exit %d, message %q",
					r.Outcome, r.ExitCode, r.Message, tc.outcome, tc.exit, tc.message)
			}
			if want := []string{strings.TrimSpace(diagnostics)}; !reflect.DeepEqual(r.Printed, want) {
				t.Errorf("Printed = %q; want %q", r.Printed, want)
			}
		})
	}
}
