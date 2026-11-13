package tlc

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tla"
)

// A path that takes a step whose body hides a choice is refused, and one
// that doesn't take it is read as before (#857).
func TestReplayEdgesRefuseAStepThatHidesAChoice857(t *testing.T) {
	normal := []tla.Step{{Name: "Put", Args: []string{"p"}, Binders: []tla.Binder{{Var: "p", Domain: "People"}}}, {Name: "Nested", Hidden: true}}
	buggy := []tla.Step{{Name: "Break", Args: []string{"p"}, Binders: []tla.Binder{{Var: "p", Domain: "People"}}}}
	rows := func(first string) []string {
		return []string{"<<\"invariant-replay-v1\", 0, {" + first + "}>>\n<<\"invariant-replay-v1\", 1, {<<\"Break\", <<p1>>, TRUE>>}>>"}
	}
	steps, err := replayEdges(rows(`<<"Put", <<p1>>, FALSE>>`), 2, normal, buggy)
	if err != nil || len(steps) != 2 || steps[0].Action != "Put" || !steps[1].Bug {
		t.Fatalf("a path of named steps: %+v, %v", steps, err)
	}
	if _, err := replayEdges(rows(`<<"Nested", <<>>, FALSE>>`), 2, normal, buggy); err == nil || !strings.Contains(err.Error(), "replay-unsupported-action: edge 0 takes Nested, whose body chooses part of its next state") {
		t.Fatalf("a path through a step that hides a choice: %v", err)
	}
	// A second row for an edge is refused wherever it came from, so a model
	// that prints a row of its own can't have it stand for the check's.
	twice := append(rows(`<<"Put", <<p1>>, FALSE>>`), `<<"invariant-replay-v1", 0, {<<"Put", <<p2>>, FALSE>>}>>`)
	if _, err := replayEdges(twice, 2, normal, buggy); err == nil || !strings.Contains(err.Error(), "duplicate or invalid index") {
		t.Fatalf("two rows for one edge: %v", err)
	}
}
