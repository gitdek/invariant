package setup

import (
	"strings"
	"testing"
)

func TestRepositoryGate740Preflight(t *testing.T) {
	w := workflow736(t)
	trigger := "on:\n  push:\n    branches: [main]\n  pull_request:\n"
	if !strings.Contains(w, trigger) {
		t.Fatal("fixture no longer contains the reviewed trigger block")
	}
	job := func(keys string) string {
		return strings.Replace(w, "    name: invariant/gate", keys+"    name: invariant/gate", 1)
	}
	on := func(value string) string { return strings.Replace(w, trigger, value+"\n", 1) }
	refuse := map[string]string{
		"single quoted false if":        job("    'if': false\n"),
		"double quoted false if":        job("    \"if\": false\n"),
		"space before if colon":         job("    if : false\n"),
		"quoted spaced expression":      job("    'if' : ${{ false }}\n"),
		"conditional job":               job("    \"if\" : github.event_name == 'push'\n"),
		"quoted ignored failure":        job("    'continue-on-error': true\n"),
		"spaced ignored failure":        job("    continue-on-error : true\n"),
		"scalar dependency":             job("    needs: prerequisite\n"),
		"sequence dependency":           job("    needs: [prerequisite]\n"),
		"block dependency":              job("    needs:\n      - prerequisite\n"),
		"quoted dependency key":         job("    'needs' : [prerequisite]\n"),
		"expression dependency":         job("    needs: ${{ fromJSON('[]') }}\n"),
		"alias dependency":              job("    needs: *dependencies\n"),
		"no triggers":                   on(""),
		"push only":                     on("on: [push]"),
		"pull request only":             on("on: [pull_request]"),
		"manual only":                   on("on: workflow_dispatch"),
		"wrong pull request event":      on("on: [push, pull_request_target]"),
		"comment is not a PR trigger":   on("on: [push] # pull_request"),
		"PR paths":                      on("on:\n  push:\n  pull_request:\n    paths: ['api/**']"),
		"PR ignored paths":              on("on:\n  push:\n  pull_request:\n    paths-ignore: ['docs/**']"),
		"PR branches":                   on("on:\n  push:\n  pull_request:\n    branches: [release]"),
		"PR ignored branches":           on("on:\n  push:\n  pull_request:\n    branches-ignore: [main]"),
		"PR activity subset":            on("on:\n  push:\n  pull_request:\n    types: [closed]"),
		"quoted PR filtering":           on("'on':\n  push:\n  'pull_request' :\n    'paths' : ['api/**']"),
		"inline PR filtering":           on("on: {push: {}, pull_request: {paths: ['api/**']}}"),
		"trigger alias":                 on("on: *events"),
		"trigger expression":            on("on: ${{ fromJSON('[\"push\",\"pull_request\"]') }}"),
		"duplicate trigger declaration": on("on: [push, pull_request]\non: [push]"),
		"duplicate job condition":       job("    'if': false\n    if: true\n"),
	}
	for name, workflow := range refuse {
		t.Run(name, func(t *testing.T) {
			gh := &capabilityGitHub736{workflows: map[string]string{WorkflowPath: workflow}}
			err := CheckRepository(t.Context(), gh, "fixture/repository", "main", true)
			if err == nil {
				t.Fatal("configured preflight accepted a gate whose checks may never run")
			}
			for _, want := range []string{"upgrade", "invariant init", "review", "commit", "main"} {
				if !strings.Contains(strings.ToLower(err.Error()), want) {
					t.Errorf("refusal lacks %q guidance: %v", want, err)
				}
			}
		})
	}
	for name, workflow := range map[string]string{
		"generated":             w,
		"literal event list":    on("on: [push, pull_request]"),
		"quoted event key":      on("'on': [push, pull_request]"),
		"spaced event key":      on("on : [push, pull_request]"),
		"literal event mapping": on("on:\n  push:\n  pull_request:"),
		"empty dependencies":    job("    needs: []\n"),
		"quoted true condition": job("    'if' : true\n    'continue-on-error' : false\n"),
		"surrounding step":      strings.Replace(w, "    steps:\n", "    steps:\n      - name: Custom diagnostic\n        run: echo repository-specific\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			gh := &capabilityGitHub736{workflows: map[string]string{WorkflowPath: workflow}}
			if err := CheckRepository(t.Context(), gh, "fixture/repository", "main", true); err != nil {
				t.Fatalf("reviewable unconditional gate was refused: %v", err)
			}
		})
	}
	// Modeled-only repositories retain Check's legacy name/merge requirements;
	// capability checks are imposed only by the committed configured contract.
	gh := &capabilityGitHub736{workflows: map[string]string{WorkflowPath: "name: gate\non: [pull_request]\njobs:\n  gate:\n    name: invariant/gate\n    steps:\n      - run: echo legacy\n"}}
	if err := CheckRepository(t.Context(), gh, "fixture/repository", "main", false); err != nil {
		t.Fatalf("legacy modeled setup changed: %v", err)
	}
}
