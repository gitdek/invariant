package setup

import (
	"slices"
	"strings"
	"testing"
)

func TestRepositoryGateCapabilitiesStayInEnabledJob(t *testing.T) {
	w := workflow736(t)
	for name, workflow := range map[string]string{
		"disabled job":   strings.Replace(w, "    name: invariant/gate", "    if: false\n    name: invariant/gate", 1),
		"ignored job":    strings.Replace(w, "    name: invariant/gate", "    continue-on-error: true\n    name: invariant/gate", 1),
		"scope comment":  strings.Replace(w, `          "$RUNNER_TEMP/invariant" scope`, `          # "$RUNNER_TEMP/invariant" scope`, 1),
		"no plans":       strings.ReplaceAll(w, "git ls-files -z '.invariant/plans/issue-*.json'", "printf ''"),
		"no guard":       strings.ReplaceAll(w, `if [ "${#targets[@]}" -gt 0 ]; then`, "if true; then"),
		"masked failure": strings.Replace(w, `check-repository "${args[@]}" HEAD`, `check-repository "${args[@]}" HEAD || true`, 1),
		"early exit":     strings.Replace(w, "          args=(-repo", "          exit 0\n          args=(-repo", 1),
		"false scope":    strings.Replace(w, "if: github.event_name", "if: false && github.event_name", 1),
		"wrong base":     strings.ReplaceAll(w, "BASE: ${{ github.event.pull_request.base.sha || github.sha }}", "BASE: HEAD"),
	} {
		t.Run(name, func(t *testing.T) {
			if hasRepositoryGate(workflow) {
				t.Fatal("incomplete gate counted as configured capability")
			}
		})
	}
	custom := strings.Replace(w, "    steps:\n", "    steps:\n      - name: Custom diagnostic\n        run: echo repository-specific\n", 1)
	if !hasRepositoryGate(custom) {
		t.Fatal("custom step hid the complete gate")
	}
}

func TestRepositoryGateQuotesProjectPaths(t *testing.T) {
	w := workflow736(t)
	files := []string{"models/with spaces/.invariant/invariant.json", ".invariant/plans/issue-736.json"}
	for _, command := range []string{"verify", "ratification"} {
		t.Run(command, func(t *testing.T) {
			calls, err, out := script736(t, findStep736(t, w, command), files, "pull_request", "feature", "")
			if err != nil {
				t.Fatalf("generated step failed: %v\n%s", err, out)
			}
			if len(calls) != 1 {
				t.Fatalf("expected one invocation, got %v", calls)
			}
			if !slices.Contains(calls[0], "models/with spaces") {
				t.Fatalf("project name split into arguments: %v", calls)
			}
		})
	}
}
