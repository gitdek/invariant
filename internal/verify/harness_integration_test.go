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

// ticketsDriver is a driver for testdata/tickets built on Invariant's harness
// (D-0085). skip, when set, is a condition under which a step returns null,
// leaving it untried.
func ticketsDriver(skip string) string {
	if skip == "" {
		skip = "false"
	}
	return `import { explore } from "./invariant-explore.ts";
import { Tickets } from "./src/tickets.ts";

const Capacity = 2;
const Clients = 3;

const clients = Array.from({ length: Clients }, (_, i) => "c" + (i + 1));
const args = clients.map((c) => [{ $mv: c }]);

// The code, made again from a state: a pool at the bounds, holding held.
function make(held: string[]): Tickets {
  const t = new Tickets(Capacity);
  for (const c of held) t.take(c);
  return t;
}

explore<string[]>({
  initial: [[]],
  steps: [
    { name: "Take", args, take: (held, c) => { if (` + strings.ReplaceAll(skip, "OP", `"Take"`) + `) return null; const t = make(held); t.take(c.$mv); return t.held(); } },
    { name: "Give", args, take: (held, c) => { if (` + strings.ReplaceAll(skip, "OP", `"Give"`) + `) return null; const t = make(held); t.give(c.$mv); return t.held(); } },
  ],
  abstract: (held) => ({ held: { $set: held.map((c) => ({ $mv: c })) } }),
});
`
}

// ticketsWithHarness is testdata/tickets with driver in place of its own, and
// Capacity named as the size the code takes.
func ticketsWithHarness(t *testing.T, driver string) string {
	t.Helper()
	dir := tickets(t, nil)
	if err := os.WriteFile(filepath.Join(dir, "conformance.ts"), []byte(driver), 0o644); err != nil {
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
	m.Parameters = []string{"Capacity"}
	if b, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A driver on the harness tries both steps for every client in each of the
// pool's seven states, refusals included, and the gate says so.
func TestAHarnessDriverTriesEveryStep(t *testing.T) {
	r := run(t, ticketsWithHarness(t, ticketsDriver("")))
	c := r.Conformance
	if !r.Passed || c == nil || c.Tried == nil || !c.Tried.Passed || c.Tried.States != 7 || c.Tried.Attempts != 42 {
		t.Fatalf("passed %v, conformance %+v, tried %+v", r.Passed, c, c.Tried)
	}
	// The harness counts one size larger too.
	if r.Larger == nil || !r.Larger.Passed || r.Larger.States != 15 {
		t.Errorf("larger %+v", r.Larger)
	}
}

// Skipping a Give the model rules out, from a client that holds no ticket,
// is the skip copythis-ad#33's driver made, and the gate fails it.
func TestAHarnessDriverThatSkipsARuleFails(t *testing.T) {
	r := run(t, ticketsWithHarness(t, ticketsDriver(`OP === "Give" && !held.includes(c.$mv)`)))
	tr := r.Conformance.Tried
	if r.Passed || tr == nil || tr.Passed || !strings.Contains(tr.Untried, `<<"Give", c1>>`) {
		t.Fatalf("passed %v, tried %+v", r.Passed, tr)
	}
}

// Stopping at the capacity the code takes is a skip too: refusing a Take
// when the pool is full is a rule the code keeps.
func TestAHarnessDriverThatStopsAtCapacityFails(t *testing.T) {
	r := run(t, ticketsWithHarness(t, ticketsDriver(`OP === "Take" && held.length >= Capacity`)))
	tr := r.Conformance.Tried
	if r.Passed || tr == nil || tr.Passed || !strings.Contains(tr.Untried, `"Take"`) {
		t.Fatalf("passed %v, tried %+v", r.Passed, tr)
	}
}

const pyTickets = `class Tickets:
    """A pool of tickets. Its capacity is chosen when it's made."""

    def __init__(self, capacity):
        self.capacity = capacity
        self.holders = set()

    def take(self, client):
        if client in self.holders or len(self.holders) >= self.capacity:
            return False
        self.holders.add(client)
        return True

    def give(self, client):
        if client not in self.holders:
            return False
        self.holders.discard(client)
        return True

    def held(self):
        return sorted(self.holders)
`

const pyTicketsTest = `import unittest

from src.tickets import Tickets


class TestTickets(unittest.TestCase):
    def test_full_pool_refuses(self):
        t = Tickets(1)
        self.assertTrue(t.take("c1"))
        self.assertFalse(t.take("c2"))
        self.assertTrue(t.give("c1"))
        self.assertTrue(t.take("c2"))
`

// pyTicketsDriver is a Python driver on the harness. skip, when set, is a
// condition under which a step returns None, leaving it untried.
func pyTicketsDriver(skip string) string {
	if skip == "" {
		skip = "False"
	}
	return `from invariant_explore import Step, explore
from src.tickets import Tickets

Capacity = 2
Clients = 3

clients = ["c%d" % (i + 1) for i in range(Clients)]
args = [[{"$mv": c}] for c in clients]


def make(held):
    t = Tickets(Capacity)
    for c in held:
        t.take(c)
    return t


def take(held, c):
    if ` + strings.ReplaceAll(skip, "OP", `"Take"`) + `:
        return None
    t = make(held)
    t.take(c["$mv"])
    return tuple(t.held())


def give(held, c):
    if ` + strings.ReplaceAll(skip, "OP", `"Give"`) + `:
        return None
    t = make(held)
    t.give(c["$mv"])
    return tuple(t.held())


explore(
    initial=[()],
    steps=[Step("Take", take, args), Step("Give", give, args)],
    abstract=lambda held: {"held": {"$set": [{"$mv": c} for c in held]}},
)
`
}

// pyTicketsProject builds the tickets pool in Python, with its driver on
// the harness and Capacity named as the size the code takes.
func pyTicketsProject(t *testing.T, driver string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pytickets")
	model, err := os.ReadFile(filepath.Join("testdata", "tickets", "Tickets.tla"))
	if err != nil {
		t.Fatal(err)
	}
	for file, text := range map[string]string{
		".invariant/specs/Tickets.tla": string(model), "src/tickets.py": pyTickets,
		"test_tickets.py": pyTicketsTest, "conformance.py": driver,
	} {
		path := filepath.Join(dir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := project.Manifest{Name: "pytickets", Module: ".invariant/specs/Tickets.tla", Code: "src", Language: "python",
		Conformance: "conformance.py", Exhaustive: true, Parameters: []string{"Capacity"}}
	lock := project.Lock{Decision: "a test", Bounds: map[string]string{"Capacity": "2", "Clients": "{c1, c2, c3}"}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Clients take and give back tickets."},
		{Name: "TypeOK", Kind: project.Invariant, Says: "Only clients hold tickets."},
		{Name: "AtMostCapacity", Kind: project.Invariant, Says: "No more tickets are out than the pool has."},
		{Name: "Full", Kind: project.Witness, Says: "Every ticket can be out."},
		{Name: "TakeWhenFull", Kind: project.Bug, Says: "A client takes a ticket when none is left.", Expect: "AtMostCapacity"},
	}}
	for file, v := range map[string]any{"invariant.json": manifest, "ratified.lock": lock} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".invariant", file), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAPythonHarnessDriverTriesEveryStep(t *testing.T) {
	r := run(t, pyTicketsProject(t, pyTicketsDriver("")))
	c := r.Conformance
	if !r.Passed || c == nil || c.Tried == nil || !c.Tried.Passed || c.Tried.States != 7 || c.Tried.Attempts != 42 {
		t.Fatalf("passed %v, build %+v, conformance %+v, tried %+v", r.Passed, r.Build, c, c.Tried)
	}
	if r.Larger == nil || !r.Larger.Passed || r.Larger.States != 15 {
		t.Errorf("larger %+v", r.Larger)
	}
}

func TestAPythonHarnessDriverThatSkipsARuleFails(t *testing.T) {
	r := run(t, pyTicketsProject(t, pyTicketsDriver(`OP == "Give" and c["$mv"] not in held`)))
	tr := r.Conformance.Tried
	if r.Passed || tr == nil || tr.Passed || !strings.Contains(tr.Untried, `<<"Give", c1>>`) {
		t.Fatalf("passed %v, tried %+v", r.Passed, tr)
	}
}
