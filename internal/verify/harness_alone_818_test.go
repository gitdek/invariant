package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The harness prints its result once, on a line of its own. Output that
// holds a result twice, or inside another line, holds none the gate takes:
// something else in the step printed one too (#818).
func TestAgreementResultIsTakenOnce818(t *testing.T) {
	for _, c := range []struct {
		name, out string
		states    int64
		depth     int
		ok        bool
	}{
		{"the harness's line", "=== RUN   TestInvariantAgreement\ninvariant-agreement states=288 depth=11\n--- PASS: TestInvariantAgreement (0.01s)\n", 288, 11, true},
		{"with carriage returns", "=== RUN\r\ninvariant-agreement states=3 depth=2\r\nPASS\r\n", 3, 2, true},
		{"two results", "invariant-agreement states=4242 depth=42\ninvariant-agreement states=288 depth=11\n", 0, 0, false},
		{"the same result twice", "invariant-agreement states=288 depth=11\ninvariant-agreement states=288 depth=11\n", 0, 0, false},
		{"a result inside a line", "log: invariant-agreement states=288 depth=11\n", 0, 0, false},
		{"a result with more after it", "invariant-agreement states=288 depth=11 and more\n", 0, 0, false},
		{"no result", "FAIL\n", 0, 0, false},
	} {
		states, depth, ok := agreement(c.out)
		if ok != c.ok || states != c.states || depth != c.depth {
			t.Errorf("%s: agreement = %d, %d, %v; want %d, %d, %v", c.name, states, depth, ok, c.states, c.depth, c.ok)
		}
	}
	forged := "@@invariant vet=0\nok\n@@invariant test=0\ninvariant-agreement states=4242 depth=42\ninvariant-agreement states=288 depth=11\n@@invariant agree=0\n"
	if _, e, err := parseSandbox(forged, true); err != nil || e.OK || e.States != 0 {
		t.Errorf("a step that printed two results was taken: %+v, %v", e, err)
	}
	if e, err := parseLarger(forged[strings.Index(forged, "invariant-agreement"):]); err != nil || e.OK {
		t.Errorf("a larger run that printed two results was taken: %+v, %v", e, err)
	}
}

// A package's own test files ran in the step before the harness. They aren't
// compiled into the harness's step, where a TestMain of theirs would run in
// its process: here one prints a result of its own and replaces the
// recording once the harness has written it. The step is run as the sandbox
// runs it, with the harness's directory in place of the container's.
func TestTheHarnessRunsWithoutThePackagesTests818(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and explores the two-phase commit example")
	}
	for _, tool := range []string{"sh", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on PATH", tool)
		}
	}
	const forge = `package twophase

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	traces := os.Getenv("INVARIANT_TRACES")
	if traces != "" {
		fmt.Println("invariant-agreement states=4242 depth=42")
	}
	code := m.Run()
	if traces != "" {
		os.WriteFile(traces, []byte(` + "`" + `{"attempts":[],"init":[0],"states":[{"forged":true}]}` + "`" + `), 0o644)
	}
	os.Exit(code)
}
`
	run := func(t *testing.T, step string) (string, string) {
		t.Helper()
		work := t.TempDir()
		src, agree, traces := filepath.Join(work, "src"), filepath.Join(work, "agree"), filepath.Join(work, "traces.json")
		if err := copyTree(filepath.Join("..", "..", "examples", "02-twophase-commit"), src); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(agree, 0o755); err != nil {
			t.Fatal(err)
		}
		for path, text := range map[string]string{
			filepath.Join(src, "twophase", "zz_forge_test.go"):     forge,
			filepath.Join(agree, "zz_invariant_agreement_test.go"): explorerTest(filepath.Join(src, "twophase"), "twophase"),
		} {
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("sh", "-c", strings.ReplaceAll(step, "/agree/", agree+"/"))
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "PKG=twophase", "INVARIANT_TRACES="+traces, "GOFLAGS=-mod=mod", "GOWORK=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the step didn't run: %v\n%s", err, out)
		}
		recording, _ := os.ReadFile(traces)
		return string(out), string(recording)
	}
	// With the package's tests left in place, its TestMain prints a result
	// and replaces the recording: the fixture does what the audit found.
	out, recording := run(t, strings.Replace(agreementStep, `rm -f "./$PKG"/*_test.go && `, "", 1))
	if !strings.Contains(out, "invariant-agreement states=4242 depth=42") || !strings.Contains(recording, `"forged":true`) {
		t.Fatalf("the fixture's TestMain didn't forge a result and a recording:\n%s", out)
	}
	if _, _, ok := agreement(out); ok {
		t.Error("two results were printed, and one was taken")
	}
	// As the gate runs it, only the harness is compiled.
	out, recording = run(t, agreementStep)
	states, depth, ok := agreement(out)
	if !ok || states == 4242 || states <= 0 || depth <= 0 || !strings.Contains(out, "@@invariant agree=0") {
		t.Fatalf("the harness's own result wasn't taken: %d, %d, %v\n%s", states, depth, ok, out)
	}
	if strings.Contains(recording, "forged") || !strings.HasPrefix(recording, `{"attempts":[`) {
		t.Errorf("the recording isn't the harness's: %.80s", recording)
	}
}
