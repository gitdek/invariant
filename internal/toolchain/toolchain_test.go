package toolchain

import (
	"strings"
	"testing"
)

// The Nagini sandbox copies its Java runtime from the image TLC runs in, so
// bumping one pin without the other would split them.
func TestNaginiUsesThePinnedJava(t *testing.T) {
	if !strings.Contains(string(NaginiDockerfile), "FROM --platform=linux/amd64 "+JavaImage+" AS java") {
		t.Errorf("nagini.Dockerfile must take its Java runtime from %s", JavaImage)
	}
}
