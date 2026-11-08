package setup

import (
	"maps"
	"strings"
	"testing"
)

func TestRepositoryGatesRetainsCompetingChecks742(t *testing.T) {
	capable := writtenByInit(t)
	for _, name := range []string{"name: invariant/gate", "'name': 'invariant/gate'", `"name": "invariant/gate" # competing check`} {
		t.Run(name, func(t *testing.T) {
			competing := strings.Replace(testsOnly, "name: test", name, 1)
			if hasRepositoryGate(competing) {
				t.Fatal("fixture must lack the configured gate checks")
			}
			workflows := map[string]string{
				WorkflowPath: capable, ".github/workflows/competing.yaml": competing,
				".github/workflows/tests.yml": testsOnly, ".github/workflows/named.yml": namedForTheGate,
			}
			want := map[string]string{WorkflowPath: capable, ".github/workflows/competing.yaml": competing}
			captured := RepositoryGates(workflows)
			if !maps.Equal(captured, want) {
				t.Fatalf("gate authority lost a competing check or retained unrelated jobs: %v", captured)
			}
			workflows[".github/workflows/competing.yaml"] = strings.Replace(competing, "go test ./...", "echo success", 1)
			if maps.Equal(captured, RepositoryGates(workflows)) {
				t.Fatal("changing a competing check preserved gate authority")
			}
			delete(workflows, ".github/workflows/competing.yaml")
			if maps.Equal(captured, RepositoryGates(workflows)) {
				t.Fatal("removing a competing check preserved gate authority")
			}
			without := RepositoryGates(workflows)
			workflows[".github/workflows/competing.yaml"] = competing
			if maps.Equal(without, RepositoryGates(workflows)) {
				t.Fatal("adding a competing check preserved gate authority")
			}
		})
	}
}
