package toolchain

import (
	"os"
	"strings"
	"testing"
)

// GoVersion is the Go that GoImage holds: the comment that names GoImage's
// tag names GoVersion too, so a change to one is a change to the other.
// A repository's gate checks a part that builds on macOS with GoVersion,
// and its Linux job checks Go parts in GoImage (#929).
func TestGoVersionIsGoImages929(t *testing.T) {
	b, err := os.ReadFile("toolchain.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	at := strings.Index(text, "\tGoImage = ")
	if at < 0 {
		t.Fatal("toolchain.go doesn't declare GoImage")
	}
	comment := text[strings.LastIndex(text[:at], "// GoImage "):at]
	if !strings.Contains(comment, "(Go "+GoVersion+")") || !strings.Contains(comment, "golang:"+GoVersion[:strings.LastIndex(GoVersion, ".")]+"-") {
		t.Errorf("GoImage's comment doesn't name Go %s:\n%s", GoVersion, comment)
	}
}
