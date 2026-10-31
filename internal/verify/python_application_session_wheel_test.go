package verify

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// A wheel's filename can spell its distribution with dots, underscores or
// capitals; each normalizes to the name the installer records.
func TestPythonApplicationDependenciesAcceptDottedWheelNames(t *testing.T) {
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	image := digest("b")
	files := []project.ComponentFile{{Path: "requirements.txt", SHA256: digest("a")}}
	evidence := func(filename string) *toolchain.PythonDependencies {
		return &toolchain.PythonDependencies{
			Image: image, Base: toolchain.PythonImage, Platform: "linux/amd64", Recipe: digest("c"),
			Inputs:          []toolchain.PythonInput{{Path: "requirements.txt", SHA256: digest("a")}},
			CanonicalInputs: []toolchain.PythonInput{{Path: "constraints.txt", SHA256: digest("d")}, {Path: "requirements.txt", SHA256: digest("e")}},
			Artifacts:       []toolchain.PythonArtifact{{Name: "example-pkg", Version: "1.0", Filename: filename, SHA256: digest("f")}},
		}
	}
	for _, name := range []string{"example.pkg-1.0-py3-none-any.whl", "example_pkg-1.0-py3-none-any.whl", "Example.Pkg-1.0-py3-none-any.whl"} {
		if err := pythonApplicationDependencies(files, "", image, evidence(name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := pythonApplicationDependencies(files, "", image, evidence("other.pkg-1.0-py3-none-any.whl")); err == nil {
		t.Error("accepted a wheel for another distribution")
	}
}
