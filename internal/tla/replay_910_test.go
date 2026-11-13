package tla

import (
	"strings"
	"testing"
)

// A bug whose definition names no operation the reader can call is refused
// with what the reader made of it (#910): a draft's check gives the reason
// to the drafter, who can't act on "must name its parameterized operations"
// when the definition does name one and binds it in a form the reader
// doesn't take.
func TestABugThatNamesNoOperationSaysWhatWasRead910(t *testing.T) {
	module := func(bug string) string {
		return "---- MODULE Buffer ----\nEXTENDS Sequences\nCONSTANTS Msgs\nVARIABLES buf\n\nvars == <<buf>>\n\nInit == buf = <<>>\n\n" +
			"Put(m) == buf' = Append(buf, m)\n\nPutTwo(m, n) == buf' = Append(Append(buf, m), n)\n\nNext == \\E m \\in Msgs : Put(m)\n\n" +
			bug + "\n\nSpec == Init /\\ [][Next]_vars\n====\n"
	}
	for bug, want := range map[string]string{
		`Bug == \E m, n \in Msgs : PutTwo(m, n)`:       `an \E binds "m"; want "x \in S"`,
		`Bug == \E m \in Msgs : buf' = Append(buf, m)`: "isn't a named step",
	} {
		m, err := Read(module(bug), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = m.ReplayActions("Bug")
		if err == nil || !strings.HasPrefix(err.Error(), "replay-unsupported-action: Bug must name its parameterized operations: ") || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v; want it refused with %q", bug, err, want)
		}
	}
	for _, bug := range []string{`Bug == \E m \in Msgs, n \in Msgs : PutTwo(m, n)`, `Bug == \E m \in Msgs : \E n \in Msgs : PutTwo(m, n)`} {
		m, err := Read(module(bug), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, faulty, err := m.ReplayActions("Bug")
		if err != nil || len(faulty) != 1 || faulty[0].Name != "PutTwo" || len(faulty[0].Binders) != 2 || len(faulty[0].Args) != 2 {
			t.Errorf("%s: %+v, %v; want PutTwo with both of its arguments bound", bug, faulty, err)
		}
	}
}
