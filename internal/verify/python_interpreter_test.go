package verify_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

// The collector runs in toolchain.PythonImage, so a test that passes under
// another minor version proves little about production. Where python3 is
// meant to be the image's (INVARIANT_PYTHON_PINNED=1, as in the plumbing
// sandbox and CI's second run of the Python tests), a mismatch fails (#332).
func TestPythonInterpreterMatchesImage(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_PINNED") != "1" {
		t.Log("INVARIANT_PYTHON_PINNED isn't set, so python3 may be any version here")
		return
	}
	out, err := exec.Command("python3", "-c", "import platform; print(platform.python_version())").Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(out))
	if pythonMinor(got) != pythonMinor(toolchain.PythonVersion) {
		t.Fatalf("python3 is %s, but the pinned image runs %s", got, toolchain.PythonVersion)
	}
}

func pythonMinor(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}
