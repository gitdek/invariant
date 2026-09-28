//go:build integration

package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// poolExplorer is an explorer for the connection pool that reports
// every step it tries, through Try (D-0090). skip, when set, is a condition
// on step, c and s under which Try leaves a step unreported.
func poolExplorer(skip string) string {
	if skip == "" {
		skip = "false"
	}
	return `package pool

import "fmt"

// The model's bounds: Size = 2, Clients = {c1, c2, c3}.
const (
	Size    = 2
	Clients = 3
)

// State is the model's held: Held[c] is 1 + the connection client c holds,
// or 0 when it holds none.
type State struct {
	Held [Clients]int
}

func Init() State { return State{} }

// load makes the code's pool from s.
func load(s State) *Pool {
	p := New(Size, Clients)
	for c := 0; c < Clients; c++ {
		if h := s.Held[c]; h != 0 {
			p.Held[c] = h
			p.Owner[h-1] = c + 1
		}
	}
	return p
}

// store reads the code's pool back into a State.
func store(p *Pool) State {
	var s State
	for c := 0; c < Clients; c++ {
		s.Held[c] = p.Held[c]
	}
	return s
}

func client(c int) map[string]any { return map[string]any{"$mv": fmt.Sprintf("c%d", c+1)} }

// Try tries each step for each client: Acquire of each connection, Refuse,
// which changes nothing, and Release. The code refuses what it must.
func Try(s State, tried func(step string, args []any, next State)) {
	skip := func(step string, c int) bool { return ` + skip + ` }
	for c := 0; c < Clients; c++ {
		args := []any{client(c)}
		for k := 0; k < Size; k++ {
			if skip("Acquire", c) {
				continue
			}
			p := load(s)
			p.Acquire(c, k)
			tried("Acquire", args, store(p))
		}
		if !skip("Refuse", c) {
			tried("Refuse", args, s)
		}
		if !skip("Release", c) {
			p := load(s)
			p.Release(c)
			tried("Release", args, store(p))
		}
	}
}

// Successors is every state Try reaches, for the pool's own tests.
func Successors(s State) []State {
	var out []State
	Try(s, func(_ string, _ []any, next State) {
		if next != s {
			out = append(out, next)
		}
	})
	return out
}

// Abstract is s as the spec's held: each client's set of connections.
func Abstract(s State) map[string]any {
	var held []any
	for c := 0; c < Clients; c++ {
		conns := []any{}
		if h := s.Held[c]; h != 0 {
			conns = append(conns, h)
		}
		held = append(held, []any{client(c), map[string]any{"$set": conns}})
	}
	return map[string]any{"held": map[string]any{"$fn": held}}
}
`
}

// poolWithTry is the connection pool with explorer in place of its own, and
// Size named as the size the code takes.
func poolWithTry(t *testing.T, explorer string) string {
	t.Helper()
	dir := copyProject(t, "testdata/examples/05-connection-pool", nil)
	if err := os.WriteFile(filepath.Join(dir, "pool", "explore.go"), []byte(explorer), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".invariant", "invariant.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m project.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.Parameters = []string{"Size"}
	if b, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A Go explorer with Try tries every step for every client in each of the
// pool's 13 states, refusals included, and the gate checks each attempt
// against the model, as it does a driver's. Agreement and one size larger
// still hold, and the code is still proved.
func TestAGoExplorerTriesEveryStep(t *testing.T) {
	r := run(t, poolWithTry(t, poolExplorer("")))
	c := r.Conformance
	if !r.Passed || c == nil || !c.Passed || !c.Exhaustive || c.Tried == nil || !c.Tried.Passed {
		t.Fatalf("passed %v, conformance %+v", r.Passed, c)
	}
	// Four attempts per client in each state: two Acquires, a Refuse and a
	// Release.
	if c.States != 13 || c.Tried.States != 13 || c.Tried.Attempts != 13*3*4 {
		t.Errorf("conformance %+v, tried %+v", c, c.Tried)
	}
	if a := r.Agreement; a == nil || !a.Passed || a.States != 13 {
		t.Errorf("agreement %+v", a)
	}
	if r.Larger == nil || !r.Larger.Passed || r.Larger.States != 73 {
		t.Errorf("larger %+v", r.Larger)
	}
	if r.Code == nil || !r.Code.Passed {
		t.Errorf("code %+v", r.Code)
	}
}

// Skipping a Release the model rules out, by a client that holds nothing, is
// the skip copythis-ad#33's driver made, and the gate fails it in Go too.
func TestAGoExplorerThatSkipsARuleFails(t *testing.T) {
	r := run(t, poolWithTry(t, poolExplorer(`step == "Release" && s.Held[c] == 0`)))
	c := r.Conformance
	if r.Passed || c == nil || c.Tried == nil || c.Tried.Passed || !strings.Contains(c.Tried.Untried, `<<"Release", c1>>`) {
		t.Fatalf("passed %v, conformance %+v, tried %+v", r.Passed, c, c.Tried)
	}
}

// Stopping at the pool's size, which the code takes, is a skip too: refusing
// an Acquire when every connection is out is a rule the code keeps.
func TestAGoExplorerThatStopsAtCapacityFails(t *testing.T) {
	r := run(t, poolWithTry(t, poolExplorer(`step == "Acquire" && s.Held[0] != 0 && s.Held[1] != 0 || step == "Acquire" && s.Held[0] != 0 && s.Held[2] != 0 || step == "Acquire" && s.Held[1] != 0 && s.Held[2] != 0`)))
	c := r.Conformance
	if r.Passed || c == nil || c.Tried == nil || c.Tried.Passed || !strings.Contains(c.Tried.Untried, `"Acquire"`) {
		t.Fatalf("passed %v, conformance %+v, tried %+v", r.Passed, c, c.Tried)
	}
}
