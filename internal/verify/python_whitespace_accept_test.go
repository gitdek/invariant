package verify

import (
	"fmt"
	"strings"
	"testing"
)

// #258: pip splits requirement text from its options on ASCII spaces only.
// Treating a tab or Unicode whitespace as that boundary repairs an input pip
// rejects and changes its meaning when producing the canonical install files.
// Both dependency APIs must reject every case before even inspecting Docker,
// in the root, a referenced requirement and a referenced constraint, while
// leaving every application's input byte unchanged.
func TestPythonDependenciesRejectNonSpaceOptionWhitespace(t *testing.T) {
	pyDepsRejectInvalidInputs(t, pyDepsRejectedOptionWhitespaceFixtures())
}

// The real pinned-pip differential consumes this same rejected corpus. These
// are all Python str.isspace characters except ASCII space and characters
// handled by str.splitlines. The latter have separate separator regressions:
// a requirement, line break and standalone --hash need not be refused by pip.
// U+001F belongs here even though Go's unicode.IsSpace does not recognize it.
func pyDepsRejectedOptionWhitespaceFixtures() []pyDepsInvalidInputCase {
	hash := "--hash=sha256:" + strings.Repeat("a", 64)
	var cases []pyDepsInvalidInputCase
	for _, separator := range []struct {
		name string
		char rune
	}{
		{"tab", '\u0009'},
		{"unit separator", '\u001f'},
		{"no-break space", '\u00a0'},
		{"ogham space mark", '\u1680'},
		{"en quad", '\u2000'},
		{"em quad", '\u2001'},
		{"en space", '\u2002'},
		{"em space", '\u2003'},
		{"three-per-em space", '\u2004'},
		{"four-per-em space", '\u2005'},
		{"six-per-em space", '\u2006'},
		{"figure space", '\u2007'},
		{"punctuation space", '\u2008'},
		{"thin space", '\u2009'},
		{"hair space", '\u200a'},
		{"narrow no-break space", '\u202f'},
		{"medium mathematical space", '\u205f'},
		{"ideographic space", '\u3000'},
	} {
		cases = append(cases, pyDepsInvalidInputCase{
			name: fmt.Sprintf("U+%04X %s", separator.char, separator.name),
			raw:  "packaging==24.2" + string(separator.char) + hash + "\n",
		})
	}
	return append(cases,
		pyDepsInvalidInputCase{"ASCII space then tab", "packaging==24.2 \t" + hash + "\n"},
		pyDepsInvalidInputCase{"tab after continuation", "packaging==24.2\\\n\t" + hash + "\n"},
	)
}

// Keep the supported forms working while tightening the option boundary.
// Leading tabs are ordinary indentation; a tab attached to --hash is not an
// ASCII-space boundary. These accepted fixtures also reach the pinned parser
// oracle through the shared canonical-staging assertion when opted in.
func TestPythonDependenciesAcceptASCIIOptionSpaces(t *testing.T) {
	firstHash, secondHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	pyDepsAssertAcceptedSemanticInput(t,
		"\tpackaging==24.2 --hash=sha256:"+firstHash+"\n"+
			"idna==3.10    --hash=sha256:"+firstHash+"   --hash=sha256:"+secondHash+"\n"+
			"typing-extensions==4.12.2; python_version >= '3.0'  --hash=sha256:"+firstHash+"\n"+
			"requests==2.32.3\\\n    --hash=sha256:"+firstHash+"\n",
		[]string{
			"packaging==24.2 --hash=sha256:" + firstHash,
			"idna==3.10 --hash=sha256:" + firstHash + " --hash=sha256:" + secondHash,
			"typing-extensions==4.12.2; python_version >= '3.0' --hash=sha256:" + firstHash,
			"requests==2.32.3 --hash=sha256:" + firstHash,
		})
}
