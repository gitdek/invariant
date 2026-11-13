package tla

import (
	"strings"
	"testing"
)

func TestReplayResolvesExistentialAliasesAndRefusesHiddenArguments(t *testing.T) {
	source := `---- MODULE ReplayActions ----
EXTENDS Naturals
CONSTANT People
VARIABLE n
Init == n = 0
Put(p) == n' = p
Alias == \E p \in People : Put(p)
Next == Alias
Break(p) == n' = p + 1
Bug == \E p \in People : Break(p)
====
`
	m, err := Read(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	normal, buggy, err := m.ReplayActions("Bug")
	if err != nil || len(normal) != 1 || normal[0].Name != "Put" || len(normal[0].Binders) != 1 || len(buggy) != 1 || buggy[0].Name != "Break" || buggy[0].Args[0] != "p" {
		t.Fatalf("aliases lost their parameters: %+v %+v %v", normal, buggy, err)
	}
	for _, bug := range []string{`Bug == \E p \in People : n' = p + 1`, `Bug == Bug`, `Bug == \E p \in People : (\E p \in People : Break(p))`} {
		m, err := Read(strings.Replace(source, `Bug == \E p \in People : Break(p)`, bug, 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := m.ReplayActions("Bug"); err == nil {
			t.Errorf("accepted ambiguous arguments: %s", bug)
		}
	}
}
