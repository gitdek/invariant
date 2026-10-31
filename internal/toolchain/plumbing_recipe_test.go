package toolchain

import (
	"regexp"
	"strings"
	"testing"
)

// The plumbing sandbox is built from the images the gate pins, so a new Go
// or Python digest moves the sandbox with it (#332).
func TestPlumbingRecipeUsesThePinnedImages(t *testing.T) {
	var from []string
	for _, line := range strings.Split(string(PlumbingDockerfile), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "FROM" {
			from = append(from, f[1])
		}
	}
	if len(from) != 2 || from[0] != GoImage || from[1] != PythonImage {
		t.Fatalf("the recipe builds from %v, want GoImage's toolchain on PythonImage", from)
	}
	if !strings.Contains(string(PlumbingDockerfile), "INVARIANT_PYTHON_PINNED=1") {
		t.Error("the recipe doesn't tell the tests that its python3 is PythonImage's")
	}
	if !regexp.MustCompile(`^3\.\d+\.\d+$`).MatchString(PythonVersion) {
		t.Errorf("PythonVersion = %q, want a full Python 3 version", PythonVersion)
	}
}
