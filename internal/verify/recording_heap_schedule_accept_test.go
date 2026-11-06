package verify

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

var recording684StressNames = []string{
	"TestGateRecordingPeakHeap",
	"TestGateRefusedRecordingPeakHeap",
	"TestPythonApplicationConversionPeakHeap",
}

var recording684OrdinaryNames = []string{
	"TestPythonApplicationConversionValidation",
	"TestGateRecordingOwnershipAndCompatibility",
	"TestPythonApplicationRecordingTransport",
	"TestPythonRecordingStateCollisions",
	"TestPythonRecordingModuleEncoding",
	"TestPythonRecordingFailedExecutionDiagnostic",
	"TestPythonRecordingShortWriteAndStorageFailure",
	"TestRecordingCompatibilityCleanup",
	"TestRecordingLinkedOutput",
}

// Ask Go for both actual test inventories, as CI's tagged-minus-ordinary
// selection does. Merely skipping a default-registered stress test must fail.
// Listing never executes the large recordings or the real-verifier suites.
func TestRecordingHeapStressIntegrationInventory684(t *testing.T) {
	ordinary := recording684Inventory(t, "")
	tagged := recording684Inventory(t, "integration")
	for _, name := range recording684StressNames {
		if ordinary[name] != 0 || tagged[name] != 1 {
			t.Errorf("%s: ordinary registrations=%d, integration registrations=%d; want 0 and 1 so CI selects it exactly once", name, ordinary[name], tagged[name])
		}
	}
	for _, name := range recording684OrdinaryNames {
		if ordinary[name] != 1 || tagged[name] != 1 {
			t.Errorf("functional test %s: ordinary registrations=%d, integration registrations=%d; want 1 in each", name, ordinary[name], tagged[name])
		}
	}
	for name, count := range ordinary {
		if count != 1 || tagged[name] != 1 {
			t.Errorf("integration must retain ordinary test %s exactly once: ordinary=%d integration=%d", name, count, tagged[name])
		}
	}
	for name, count := range tagged {
		if count != 1 {
			t.Errorf("duplicate integration inventory entry %s: %d", name, count)
		}
	}
}

func recording684Inventory(t *testing.T, tag string) map[string]int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-p=1", "-mod=readonly", "-tags="+tag, "-list=.", ".")
	// An ambient GOFLAGS=-tags=integration would hide a broken default suite.
	// Keep the existing shared cache; discovery needs no network or new tools.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOFLAGS", "GOTOOLCHAIN", "GOMAXPROCS", "GOPROXY", "GOSUMDB":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GOFLAGS=", "GOTOOLCHAIN=local", "GOMAXPROCS=2", "GOPROXY=off", "GOSUMDB=off")
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list real tests with tags=%q: %v\n%s", tag, err, output)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(line)
		if len(strings.Fields(name)) == 1 && (strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Example") || strings.HasPrefix(name, "Fuzz")) {
			counts[name]++
		}
	}
	if len(counts) == 0 {
		t.Fatalf("no tests discovered with tags=%q:\n%s", tag, output)
	}
	return counts
}

// Reuse the existing subprocess observer to execute the original assertions,
// not copies of their logic. The exact selector excludes us and every stress
// entry point, even when this acceptance test is itself built with integration.
func TestRecordingFunctionalDefaultCoverage684(t *testing.T) {
	ordinary := recording684Inventory(t, "")
	for _, name := range recording684OrdinaryNames {
		if ordinary[name] != 1 {
			t.Fatalf("required default functional test %s is registered %d times, want once", name, ordinary[name])
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selection := "^(" + strings.Join(recording684OrdinaryNames, "|") + ")$"
	output := pa435RunTests(t, executable, os.Environ(), selection)
	seen := map[string][]string{}
	for _, event := range pa435TestEvents(output) {
		seen[event.name] = append(seen[event.name], event.action)
	}

	want := append([]string(nil), recording684OrdinaryNames...)
	want = append(want,
		"TestPythonApplicationConversionValidation/errors_and_validation",
		"TestPythonApplicationConversionValidation/interleaved_sampled_lifecycles",
		"TestPythonApplicationRecordingTransport/explored=false/strict-failure=false",
		"TestPythonApplicationRecordingTransport/explored=true/strict-failure=false",
		"TestPythonApplicationRecordingTransport/explored=true/strict-failure=true",
		"TestPythonRecordingShortWriteAndStorageFailure/sampled",
		"TestPythonRecordingShortWriteAndStorageFailure/explored",
		"TestGateRecordingOwnershipAndCompatibility/agreement_alone",
	)
	for _, route := range []string{"go", "typescript", "python", "existing"} {
		prefix := "TestGateRecordingOwnershipAndCompatibility/" + route
		want = append(want, prefix, prefix+"/direct_bytes")
		for _, mode := range []string{"ok", "late-bad", "build-failed", "driver-failed", "missing", "empty", "malformed", "stage-error", "cancel-producer", "cancel-wait", "cancel-check", "unrelated-error"} {
			if mode == "unrelated-error" && route != "go" || mode == "build-failed" && route == "existing" {
				continue
			}
			want = append(want, prefix+"/"+mode)
		}
	}
	for _, name := range want {
		if !reflect.DeepEqual(seen[name], []string{"run", "pass"}) {
			t.Errorf("%s must execute its original assertions once and pass serially, got %v", name, seen[name])
		}
	}
	for name, actions := range seen {
		if !reflect.DeepEqual(actions, []string{"run", "pass"}) {
			t.Errorf("functional coverage skipped, failed, duplicated or became parallel: %s %v", name, actions)
		}
		for _, stress := range recording684StressNames {
			if name == stress || strings.HasPrefix(name, stress+"/") {
				t.Errorf("ordinary functional check ran stress entry point %s", name)
			}
		}
	}
	if t.Failed() {
		t.Logf("functional child output:\n%s", output)
	}
}
