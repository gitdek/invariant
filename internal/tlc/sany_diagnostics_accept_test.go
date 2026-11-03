package tlc_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// These minimal -tool transcripts exercise the boundary at which TLC's
// structured messages surround SANY's unframed diagnostics. The indentation
// diagnostic is the one reported in #450; no Java or Docker is needed to
// check how Invariant preserves it.
const sany450Closing = "Parsing or semantic analysis failed."

func sany450Message(code, severity int, body string) string {
	return fmt.Sprintf("@!@!@STARTMSG %d:%d @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, severity, body, code)
}

func sany450Start() string {
	return sany450Message(2262, 0, "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)") +
		sany450Message(2220, 0, "Starting SANY...")
}

func sany450End() string {
	return sany450Message(2219, 0, "SANY finished.")
}

func sany450Finished() string {
	return sany450Message(2186, 0, "Finished in 120ms")
}

func sany450WantInOrder(t *testing.T, got string, parts ...string) {
	t.Helper()
	end := 0
	for _, part := range parts {
		at := strings.Index(got[end:], part)
		if at < 0 {
			t.Errorf("message lacks %q after the preceding diagnostic:\n%s", part, got)
			return
		}
		end += at + len(part)
	}
}

func sany450NoProgress(t *testing.T, message string) {
	t.Helper()
	for _, noise := range []string{"@!@!@", "TLC2 Version", "Starting SANY", "SANY finished", "Parsing file", "Semantic processing of module", "Finished in"} {
		if strings.Contains(message, noise) {
			t.Errorf("failure message includes routine output %q:\n%s", noise, message)
		}
	}
}

func TestSANYParseDiagnosticsPreserveOtherTLCResults(t *testing.T) {
	const diagnosis = "Item at line 320, col 5 to line 320, col 5 of module WatcherRecovery is not properly indented inside conjunction or disjunction list item at line 314, col 5 to line 320, col 5 of module WatcherRecovery"
	printed := "Parsing file /work/WatcherRecovery.tla\n\n***Parse Error***\n" + diagnosis + "\n\n"
	out := sany450Start() + printed + sany450End() + sany450Message(3002, 1, sany450Closing) + sany450Finished()
	r := tlc.Parse(out, 150)
	if r.Outcome != tlc.Failed || r.ExitCode != 150 {
		t.Errorf("outcome = %q, exit code = %d; want failed, 150", r.Outcome, r.ExitCode)
	}
	sany450WantInOrder(t, r.Message, "***Parse Error***", diagnosis, sany450Closing)
	if strings.Count(r.Message, diagnosis) != 1 || strings.Count(r.Message, sany450Closing) != 1 {
		t.Errorf("SANY's diagnosis and TLC's closing line must each appear once:\n%s", r.Message)
	}
	if !reflect.DeepEqual(r.Printed, []string{strings.TrimSpace(printed)}) {
		t.Errorf("Printed changed: got %q, want the original unframed text", r.Printed)
	}
	sany450NoProgress(t, r.Message)

	// The new extraction must be specific to SANY failures. In particular,
	// a model can print text that looks like a SANY error after SANY finishes.
	warnings := "Parsing file /work/Model.tla\nSemantic processing of module Model\n\n*** Warnings: 1\n\nline 4, col 1 to line 4, col 4 of module Model\nMultiple declarations or definitions for symbol x.\n"
	modelPrint := "***Parse Error***\n" + diagnosis + "\n" + sany450Closing + "\n"
	runtimeFailure := "Attempted to divide by zero."
	runtimeContext := "The error occurred when TLC was evaluating the following expression:\nline 9, col 7 to line 9, col 11 of module Model"
	crashLines := make([]string, 25)
	for i := range crashLines {
		crashLines[i] = fmt.Sprintf("unframed crash detail %02d", i+1)
	}
	for _, tc := range []struct {
		name    string
		output  string
		exit    int
		outcome tlc.Outcome
		message string
		printed []string
	}{
		{
			name: "runtime failure with warnings and model prints",
			output: sany450Start() + warnings + sany450End() +
				sany450Message(2189, 0, "Computing initial states...") + modelPrint +
				sany450Message(1000, 1, runtimeFailure) + sany450Message(2103, 1, runtimeContext) + sany450Finished(),
			exit: 1, outcome: tlc.Failed, message: runtimeFailure + "\n" + runtimeContext,
			printed: []string{strings.TrimSpace(warnings), strings.TrimSpace(modelPrint)},
		},
		{
			name: "bad configuration",
			output: "unrelated launcher output\n" +
				sany450Message(5002, 1, "The configuration file did not specify the initial state predicate.") + sany450Finished(),
			exit: 150, outcome: tlc.Failed, message: "The configuration file did not specify the initial state predicate.",
			printed: []string{"unrelated launcher output"},
		},
		{
			name:    "closing message without SANY diagnostics",
			output:  sany450Start() + "Parsing file /work/Model.tla\n" + sany450End() + sany450Message(3002, 1, sany450Closing) + sany450Finished(),
			exit:    150,
			outcome: tlc.Failed, message: sany450Closing, printed: []string{"Parsing file /work/Model.tla"},
		},
		{
			name:    "unframed crash uses the existing last twenty lines",
			output:  strings.Join(crashLines, "\n") + "\n",
			exit:    1,
			outcome: tlc.Failed, message: strings.Join(crashLines[5:], "\n"), printed: []string{strings.Join(crashLines, "\n")},
		},
		{
			name: "successful run with warnings and model prints",
			output: sany450Start() + warnings + sany450End() +
				sany450Message(2189, 0, "Computing initial states...") + modelPrint +
				sany450Message(2193, 0, "Model checking completed. No error has been found.") + sany450Finished(),
			exit: 0, outcome: tlc.Passed, message: "",
			printed: []string{strings.TrimSpace(warnings), strings.TrimSpace(modelPrint)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tlc.Parse(tc.output, tc.exit)
			if got.Outcome != tc.outcome || got.ExitCode != tc.exit || got.Message != tc.message {
				t.Errorf("got outcome %q, exit %d, message %q; want %q, %d, %q", got.Outcome, got.ExitCode, got.Message, tc.outcome, tc.exit, tc.message)
			}
			if !reflect.DeepEqual(got.Printed, tc.printed) {
				t.Errorf("Printed = %q, want %q", got.Printed, tc.printed)
			}
		})
	}
}

func TestSANYSemanticErrorsKeepLocationsAndPrintOrder(t *testing.T) {
	// More than twenty lines of diagnostics, in deliberately non-numeric
	// source order and spanning modules: neither tailing nor sorting is valid.
	diagnostics := []string{
		"line 28, col 9 to line 28, col 15 of module Model\n\nUnknown operator: `Missing'.",
		"line 6, col 5 to line 6, col 9 of module Helpers\n\nUnknown operator: `First'.",
		"line 32, col 9 to line 32, col 14 of module Model\n\nUnknown operator: `Second'.",
		"line 11, col 5 to line 11, col 9 of module Helpers\n\nUnknown operator: `Third'.",
		"line 38, col 9 to line 38, col 14 of module Model\n\nUnknown operator: `Fourth'.",
		"line 19, col 5 to line 19, col 9 of module Helpers\n\nUnknown operator: `Fifth'.",
	}
	printed := "Parsing file /work/Model.tla\nParsing file /work/Helpers.tla\n" +
		"Semantic processing of module Helpers\nSemantic processing of module Model\n" +
		"Semantic errors:\n\n*** Errors: 6\n\n" + strings.Join(diagnostics, "\n\n\n") + "\n\n"
	out := sany450Start() + printed + sany450End() + sany450Message(3002, 1, sany450Closing) + sany450Finished()
	r := tlc.Parse(out, 150)
	if r.Outcome != tlc.Failed || r.ExitCode != 150 {
		t.Errorf("outcome = %q, exit code = %d; want failed, 150", r.Outcome, r.ExitCode)
	}
	sany450WantInOrder(t, r.Message, append(append([]string{}, diagnostics...), sany450Closing)...)
	for _, diagnostic := range append(append([]string{}, diagnostics...), sany450Closing) {
		if strings.Count(r.Message, diagnostic) != 1 {
			t.Errorf("diagnostic must occur once: %q\nMessage:\n%s", diagnostic, r.Message)
		}
	}
	if !reflect.DeepEqual(r.Printed, []string{strings.TrimSpace(printed)}) {
		t.Errorf("Printed changed: got %q, want the original unframed text", r.Printed)
	}
	sany450NoProgress(t, r.Message)
}
