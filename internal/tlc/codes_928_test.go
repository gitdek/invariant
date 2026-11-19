package tlc

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// operator-error.txt is a real run of the pinned TLC, in its pinned image,
// on a step Add(TRUE) of
//
//	Add(n) == n <= Limit - count /\ count' = count + n
//
// with Limit = 3, extending Naturals. Naturals' <= is given a boolean, which
// TLC reports with its own code for the error, 2266, and its general status,
// 255, which its system errors share (#928).
func TestParseKeepsTLCsCodes928(t *testing.T) {
	r := Parse(fixture(t, "operator-error.txt"), 255)
	if r.Outcome != Failed || r.ExitCode != 255 {
		t.Fatalf("Outcome = %s, ExitCode = %d; want error, 255", r.Outcome, r.ExitCode)
	}
	if want := []int{2266, 2103}; !reflect.DeepEqual(r.Codes, want) {
		t.Fatalf("Codes = %v; want %v, the codes of the messages Message gives", r.Codes, want)
	}
	if !strings.HasPrefix(r.Message, "The first argument of <= should be an integer, but instead it is:\nTRUE\nThe error occurred when TLC was evaluating the nested\n") {
		t.Fatalf("Message = %q", r.Message)
	}
	b, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(b), `"Codes":[2266,2103]`) {
		t.Fatalf("a failed result's codes aren't kept with it: %s, %v", b, err)
	}
}

// Only a failure keeps codes, so every other result is what it was, and
// serializes as it did. SANY's failure keeps TLC's code; its diagnostics,
// which TLC writes outside its messages, have none.
func TestParseKeepsCodesForFailuresOnly928(t *testing.T) {
	for _, c := range []struct {
		name string
		exit int
	}{{"passed.txt", 0}, {"violated.txt", 12}, {"property-loop.txt", 13}} {
		r := Parse(fixture(t, c.name), c.exit)
		b, err := json.Marshal(r)
		if r.Outcome == Failed || r.Codes != nil || err != nil || strings.Contains(string(b), `"Codes"`) {
			t.Errorf("%s: outcome %s kept codes %v: %s, %v", c.name, r.Outcome, r.Codes, b, err)
		}
	}
	sany := "@!@!@STARTMSG 2262:0 @!@!@\nTLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)\n@!@!@ENDMSG 2262 @!@!@\n" +
		"@!@!@STARTMSG 2220:0 @!@!@\nStarting SANY...\n@!@!@ENDMSG 2220 @!@!@\nParsing file /work/Counter.tla\n" +
		"Semantic errors:\n\n*** Errors: 1\n\nline 3, col 33 to line 3, col 33 of module Counter\n\nCould not find declaration or definition of symbol '-.'.\n\n" +
		"@!@!@STARTMSG 2219:0 @!@!@\nSANY finished.\n@!@!@ENDMSG 2219 @!@!@\n" +
		"@!@!@STARTMSG 3002:1 @!@!@\nParsing or semantic analysis failed.\n@!@!@ENDMSG 3002 @!@!@\n"
	r := Parse(sany, 150)
	if r.Outcome != Failed || !reflect.DeepEqual(r.Codes, []int{3002}) || !strings.Contains(r.Message, "Could not find declaration or definition of symbol '-.'") {
		t.Fatalf("SANY's failure: outcome %s, codes %v, message %q", r.Outcome, r.Codes, r.Message)
	}
}
