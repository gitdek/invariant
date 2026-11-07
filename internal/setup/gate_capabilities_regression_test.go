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

func TestRepositoryGateLiteralKeysCannotHidePolicy(t *testing.T) {
	w := workflow736(t)
	for name, workflow := range map[string]string{
		"merge key":                       strings.Replace(w, "    name: invariant/gate", "    <<: *disabled\n    name: invariant/gate", 1),
		"continued condition":             strings.Replace(w, "    name: invariant/gate", "    if: true\n      && false\n    name: invariant/gate", 1),
		"duplicate steps":                 strings.Replace(w, "    steps:\n", "    'steps': []\n    steps:\n", 1),
		"quoted step condition":           strings.Replace(w, "        run: |", "        'if' : false\n        run: |", -1),
		"duplicate event":                 strings.Replace(w, "  pull_request:\n", "  pull_request:\n  'pull_request':\n", 1),
		"PR alias":                        strings.Replace(w, "  pull_request:\n", "  pull_request: *unfiltered\n", 1),
		"PR empty map followed by filter": strings.Replace(w, "  pull_request:\n", "  pull_request: {}\n    paths: ['api/**']\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if hasRepositoryGate(workflow) {
				t.Fatal("uncertain workflow policy was accepted")
			}
		})
	}
	trigger := "on:\n  push:\n    branches: [main]\n  pull_request:\n"
	for _, value := range []string{
		"on: {push: {}, pull_request: {}}\n",
		"on: {push: {branches: [main]}, pull_request: {}}\n",
		"on:\n  - push\n  - pull_request\n",
		"'on' : ['push', \"pull_request\"] # literal events\n",
		"on:\n  'push' :\n    'branches' : [main]\n  'pull_request' : {}\n",
	} {
		if !hasRepositoryGate(strings.Replace(w, trigger, value, 1)) {
			t.Fatalf("literal triggers refused: %s", value)
		}
	}
	quoted := strings.Replace(w, "jobs:", "'jobs' :", 1)
	quoted = strings.Replace(quoted, "    name: invariant/gate", "    'name' : invariant/gate\n    'needs' : [ ]", 1)
	gh := &capabilityGitHub736{workflows: map[string]string{WorkflowPath: quoted}}
	if err := CheckRepository(t.Context(), gh, "fixture/repository", "main", true); err != nil {
		t.Fatalf("quoted job keys refused: %v", err)
	}
}

// Workflow and job defaults and environment reach the gate's own steps. A
// default working directory makes project and plan discovery find nothing,
// so the checks pass without checking, and BASH_ENV runs code before each
// script (#746's review).
func TestRepositoryGateRefusesInheritedDefaultsAndEnvironment(t *testing.T) {
	w := workflow736(t)
	job := func(keys string) string {
		return strings.Replace(w, "    name: invariant/gate", keys+"    name: invariant/gate", 1)
	}
	top := func(keys string) string { return strings.Replace(w, "permissions:\n", keys+"permissions:\n", 1) }
	for name, workflow := range map[string]string{
		"workflow working directory":   top("defaults:\n  run:\n    working-directory: docs\n"),
		"job working directory":        job("    defaults:\n      run:\n        working-directory: docs\n"),
		"quoted job defaults":          job("    'defaults' : {run: {working-directory: docs}}\n"),
		"workflow BASH_ENV":            top("env:\n  BASH_ENV: /tmp/hook\n"),
		"job BASH_ENV":                 job("    env:\n      BASH_ENV: /tmp/hook\n"),
		"workflow defaults after jobs": w + "defaults:\n  run:\n    working-directory: docs\n",
		"workflow env after jobs":      w + "'env': {BASH_ENV: /tmp/hook}\n",
		"job container":                job("    container:\n      image: alpine\n      env:\n        BASH_ENV: /tmp/hook\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(workflow, "working-directory") && !strings.Contains(workflow, "BASH_ENV") {
				t.Fatal("fixture no longer places the setting")
			}
			if hasRepositoryGate(workflow) {
				t.Fatal("a gate whose steps inherit defaults or environment counted as configured capability")
			}
		})
	}
}
