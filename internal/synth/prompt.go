package synth

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

var kindMeaning = map[string]string{
	project.Spec:      "TLC explores the behaviors of this operator.",
	project.Invariant: "Must hold in every reachable state.",
	project.Witness:   "Some reachable state must satisfy it, so the invariants can't hold just because the model does nothing.",
	project.Bug: "A bug the invariants must catch. The gate adds this action to your Next and requires TLC to find the " +
		"named invariant violated, so your model must be able to do the things this bug would do damage with.",
	project.Property: "Must hold of every behavior, forever, under the fairness statements. TLC checks each one alone, " +
		"so your model must make progress wherever the fairness says it will.",
	project.Fairness: "An assumption the properties rest on: whenever its action can happen, it eventually does. The action is " +
		"pinned with it. Keep it a part of your Next, because the gate checks that every step of it is a Next step.",
}

// Prompt is the synthesis task: the request, what was ratified, the rules,
// and the module to start from. With draft set, the module holds a draft
// model the factory wrote with the statements, not just a skeleton. The code
// is in the project's language: Go proved with Gobra, Python proved with
// Nagini, or TypeScript explored completely (D-0038, D-0039).
func Prompt(p *project.Project, skeleton, request string, gateRuns int, draft, amend bool) string {
	modelTask := "The skeleton below holds the pinned definitions and a marked place for the model. Write `Init`, one operator per action, and `Next` there, above `Spec`. Use only the declared constants and variables."
	moduleHeading := "The skeleton of"
	if draft {
		modelTask = "It's already drafted in the module below, and TLC has checked it against the statements. Keep it unless the code needs it changed, and change only `Init`, the actions and `Next`."
		moduleHeading = "The module"
	}
	var statements strings.Builder
	statements.WriteString("| Statement | Kind | What it says |\n| --- | --- | --- |\n")
	for _, s := range p.Lock.Statements {
		says := s.Says
		if s.Kind == project.Bug {
			says += fmt.Sprintf(" It must violate %s.", s.Expect)
		}
		fmt.Fprintf(&statements, "| `%s` | %s | %s |\n", s.Name, s.Kind, says)
	}
	var kinds strings.Builder
	for _, k := range []string{project.Spec, project.Invariant, project.Witness, project.Bug, project.Property, project.Fairness} {
		if (k == project.Property || k == project.Fairness) && len(p.Of(k)) == 0 {
			continue
		}
		fmt.Fprintf(&kinds, "- **%s**: %s\n", k, kindMeaning[k])
	}
	var bounds []string
	for name, value := range p.Lock.Bounds {
		bounds = append(bounds, fmt.Sprintf("`%s = %s`", name, value))
	}
	sort.Strings(bounds)

	lang := languages[p.Manifest.Language]
	if lang.name == "" {
		lang = languages["go"]
	}
	intro := fmt.Sprintf("Your job is to write the TLA+ model and the %s code that satisfy them, and to show they do by passing the gate.", lang.name)
	if len(p.Manifest.Existing) > 0 {
		lang = existingLanguage
		intro = "The code they're about already exists, and nobody changes it. Your job is to keep the TLA+ model faithful to that code, to write a conformance driver that runs it as it is, and to show the code keeps the statements by passing the gate."
	}
	amendment := ""
	if amend {
		amendment = `
# The code already exists

This is an amendment. The project's code is already in your workspace, and it implements the statements people ratified before this request. They've now ratified the amended statements above. Change the code, and the model if it needs it, to satisfy the amended statements. Keep what doesn't need to change: its structure, its names and its tests. The gate checks every statement in the lock, old and new, so a change that breaks an unchanged statement fails.
`
	}
	return fmt.Sprintf(`You're the synthesis step of Invariant, a code factory. People have ratified formal statements about a system. %s

# The request

%s
%s
# What was ratified

These statements are pinned by hash in `+"`.invariant/ratified.lock`"+`. Their definitions are already in `+"`%s`"+`. Don't change them, or anything they depend on. The gate hashes their text and fails if it changes. The lock, the manifest, the request%s are protected: the gate always uses the originals.

%s
%s
TLC checks everything with these constants: %s.

# Your job

1. **The model**, in `+"`%s`"+`. %s
%s
# What the gate checks

- **Pins:** the ratified definitions are unchanged.
- **Design:** TLC finds no invariant violation and no deadlock in any reachable state of `+"`Spec`"+`.
- **Reachability:** every witness is reachable.
- **Known bugs:** every bug action, added to Next, makes TLC find its expected violation.
%s
%s
# %s `+"`%s`"+`

`+"```tla"+`
%s`+"```"+`
`, intro, strings.TrimSpace(request), amendment, p.Manifest.Module, lang.protected, statements.String(), kinds.String(), strings.Join(bounds, ", "),
		p.Manifest.Module, modelTask, lang.task(p, gateRuns), lang.checks, lang.primer, moduleHeading, p.Manifest.Module, skeleton)
}

// language is what the prompt says for one target language.
type language struct {
	name      string
	protected string // the language's protected files, as the prompt lists them
	task      func(p *project.Project, gateRuns int) string
	checks    string // the gate's code-level checks
	primer    string // what the agent needs to know about the language's tools
}

var languages = map[string]language{
	"go": {
		name:      "Go",
		protected: " and go.mod",
		task: func(p *project.Project, gateRuns int) string {
			return fmt.Sprintf(`2. **The code**, in `+"`%s/`"+` as Go package `+"`%s`"+`. It's the system alone, written the way someone would ship it, and it must do what your model's system does (D-0068).
   - **No bound is in it.** Sizes are parameters: a capacity passed to a constructor, a slice made at that size, an operation that refuses when there's no room. No constant, array length or loop limit in it comes from the bounds above. The one exception is a machine limit that keeps arithmetic from overflowing, far above any bound.
   - **Only the system's own state is in it.** The model's environment and history stay out of the code: who acts, how many times, and what was sent, received or written. Those belong to the explorer.
   - **Each operation decides for itself whether it runs.** Where the model's action can't run, it refuses: it returns false and changes nothing. Where the action can run, it takes it.
   - **Each operation has a Gobra contract that holds at every size,** directly above it. It has `+"`// @ requires`"+` lines for the permissions and arguments it needs, and `+"`// @ ensures`"+` lines for whether it ran, its effect, and everything it leaves unchanged. No contract mentions a bound.
   - Every Go file except `+"`explore.go`"+` starts with the line `+"`// +gobra`"+`, then a blank line, then the package clause, so that Gobra verifies it.
3. **The explorer,** alone in `+"`explore.go`"+`, without the Gobra header. It's the environment, and the only place the bounds live.
   - **The bounds are constants in their own `+"`const`"+` block.** Each one is named exactly after its TLA+ constant. A number is its value. A set of model values is its size, such as `+"`Producers = 2`"+` for `+"`{p1, p2}`"+`.
   - **A comparable `+"`State`"+`** holds the spec's variables at those bounds, one to one, in fixed-size arrays, booleans and small ints. The system's part is copied out of the code. The environment's and history's parts are the explorer's own.
   - **`+"`func Init() State`"+`**, the model's initial state.
   - **`+"`func Try(s State, tried func(step string, args []any, next State))`"+` tries every step your model's Next names, with every argument Next passes it.** For each, make the code's value from s at the bounds, call its operation, read its state back, and update the environment's part. Then call `+"`tried`"+` with the step's name exactly as Next names it, its arguments in the spec's encoding (see below), and the state it reached, which is s itself when the code refuses. `+"`\\E r \\in RM : RMPrepare(r)`"+` is `+"`tried(\"RMPrepare\", []any{map[string]any{\"$mv\": \"r1\"}}, next)`"+`, and the same for each participant. Steps of the environment alone, such as a failed send or a Done, are steps too, with no call into the code. Invariant's gate explores your code from `+"`Init()`"+` through `+"`Try`"+`, breadth first, and records every attempt.
   - **Only the environment's bounds let `+"`Try`"+` leave a step out,** such as a producer's third line when two is the bound. Never leave one out because of the state the code is in: a step the model rules out is one the code must refuse, and the gate checks that you tried it. A step of the environment that can't happen yet, such as a Done before the end, reports s.
   - **`+"`func Abstract(s State) map[string]any`"+`** is s in the spec's vocabulary: one entry per variable of the spec, in the encoding below.
   - **Sizes the code takes are its parameters.** A capacity the code is made with isn't the environment's bound, so `+"`Try`"+` still tries the step past it and sees the code refuse. Name those sizes, as the TLA+ constants they are, in `+"`\"parameters\"`"+` in `+"`.invariant/invariant.json`"+`: it's the one field of the manifest that's yours to write. Only numbers belong there, such as a capacity: a set of participants the code is made with needs no entry, because the gate never grows a set when it checks your steps.
4. **Standard library only.** No cgo, goroutines, network or file access. Tests in `+"`_test.go`"+` files are welcome. They run in a sandbox with no network.
5. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.
`, p.Manifest.Code, filepath.Base(p.Manifest.Code), gateRuns)
		},
		checks: `- **Every step tried:** in every state the gate reaches through ` + "`Try`" + `, you tried every step Next names, with every argument. A step may go untried only where the environment's bounds alone rule it out.
- **Conformance:** TLC checks every attempt that changed the state: it must be a Next step. The gate explores every state the code can reach, so it must also visit every state TLC finds in your model: the code and the model reach exactly the same states.
- **Agreement:** the exploration reaches exactly as many distinct ` + "`State`" + ` values as TLC finds states in your model, at the same depth.
- **One size larger:** the gate makes every bound one size larger in ` + "`explore.go`" + ` and explores again, counting. It must reach exactly as many states as TLC's model does there, as deep.
- **Code:** Gobra verifies every function in files marked ` + "`// +gobra`" + `, including slice bounds and integer overflow, at every size.
- **Build:** go vet and go test pass.`,
		primer: encodingPrimer + goEncoding + `
# Gobra, briefly

A contract is a block of comment lines directly above a function. Here's code with no model bounds in it, which Gobra proves at every capacity. It's a log buffer's core, a ring of any capacity:

` + "```go" + `
// +gobra

package logbuffer

// MaxCapacity keeps index arithmetic far from overflow. It's the machine's
// limit, not the model's.
const MaxCapacity = 1 << 30

type Line struct{ P, N int }

// Buffer is a ring of slots holding N lines, the oldest at Head.
type Buffer struct {
	Slots []Line
	Head  int
	N     int
}

// @ requires acc(&b.Slots, _) && acc(&b.Head, _) && acc(&b.N, _)
// @ decreases
// @ pure
func (b *Buffer) Ok() bool {
	return 0 < len(b.Slots) && len(b.Slots) <= MaxCapacity &&
		0 <= b.Head && b.Head < len(b.Slots) && 0 <= b.N && b.N <= len(b.Slots)
}

// @ ghost
// @ requires 0 <= x && x < 2*n && 0 < n
// @ ensures 0 <= r && r < n
// @ decreases
// @ pure func wrap(x, n int) (r int) { return x < n ? x : x - n }

// At is the i-th oldest line, which the contracts speak of.
// @ ghost
// @ requires acc(&b.Slots, _) && acc(&b.Head, _) && acc(&b.N, _) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j], _)
// @ requires 0 <= i && i < b.N
// @ decreases
// @ pure func (b *Buffer) At(i int) Line { return b.Slots[wrap(b.Head+i, len(b.Slots))] }

// Ship takes the oldest line out, and refuses when there's none.
// @ requires acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures ok == (old(b.N) > 0)
// @ ensures ok ==> l == old(b.At(0)) && b.N == old(b.N) - 1
// @ ensures ok ==> forall i int :: { b.At(i) } 0 <= i && i < b.N ==> b.At(i) == old(b.At(i + 1))
// @ ensures !ok ==> b.N == old(b.N) && b.Head == old(b.Head)
func (b *Buffer) Ship() (l Line, ok bool) {
	if b.N == 0 {
		return l, false
	}
	l = b.Slots[b.Head]
	b.Head = b.Head + 1
	if b.Head == len(b.Slots) {
		b.Head = 0
	}
	b.N = b.N - 1
	return l, true
}
` + "```" + `

Its ` + "`New(capacity)`" + ` follows the same pattern, and ` + "`Write(l)`" + ` refuses when the buffer is full, the same way. What makes contracts like these verify:

- **Spell out permissions field by field,** as above. With overflow checks on, a pure function can't read an int field through a predicate's unfolding.
- **Keep index arithmetic linear.** A remainder by a length, ` + "`(head+i) % len(slots)`" + `, can keep the solver busy for many minutes. Wrap an index with an ` + "`if`" + ` in code, and with a ghost conditional, ` + "`c ? a : b`" + `, in specs. Go itself has no such expression.
- **Say what a value holds through a ghost view,** such as ` + "`At(i)`" + ` above, and write each contract in its terms.
- **Avoid loops over slices where a ring or a direct index will do.** A loop needs invariants on the lines directly above the ` + "`for`" + `, such as ` + "`// @ invariant 0 <= i && i <= b.N`" + `, and the solver must be able to use them. Go's ` + "`copy`" + ` is specified only for slices that don't overlap.
- **An operation that may be refused** returns whether it ran, and its contract says both outcomes, as ` + "`Ship`" + ` does. Keep ` + "`requires`" + ` to what every call from the explorer meets, such as permissions and arguments inside the bounds. When the model's action can't run is the operation's to check, not its caller's.
- Calls to a function with a contract must satisfy its ` + "`requires`" + `, so the explorer passes only arguments the model's Next can pass.
`,
	},
	"typescript": {
		name:      "TypeScript",
		protected: " and package.json",
		task: func(p *project.Project, gateRuns int) string {
			return fmt.Sprintf(`2. **The code**, in TypeScript in `+"`%s/`"+`. It's the system alone, written the way someone would ship it, and it must do what your model's system does (D-0068). Node 24 runs `+"`.ts`"+` files directly by stripping their types, so use only erasable syntax: types, interfaces and `+"`as`"+` are fine, but not `+"`enum`"+`, `+"`namespace`"+` or constructor parameter properties. Import local files with their `+"`.ts`"+` extension. No dependencies: Node's standard library only, and no network.
   - **No bound is in it.** Sizes, limits and clocks are parameters or inputs: a capacity or a limit passed in when it's made, the time passed to each call, and an operation that refuses when the model wouldn't take its action. No constant in it comes from the bounds above.
   - **Only the system's own state is in it.** The model's environment and history stay out of the code: who acts, how many times, how far the clock runs, and what was sent, received or written. Those belong to the driver.
   - A module, such as `+"`%s/machine.ts`"+`, that exports the system, as a class or as functions over its state. Its operations are what the model's actions stand for. It offers a way to read its state, and to make it again from that state.
   - Tests beside it, such as `+"`%s/machine.test.ts`"+`, with `+"`node:test`"+` and `+"`node:assert`"+`. They run with `+"`node --test`"+`.
3. **The conformance driver**, `+"`%s`"+`. It's the environment, and the only place the bounds live.
   - **The bounds are constants at its top,** each named exactly after its TLA+ constant. A number is its value, and a set of model values is its size.
   - **It explores with Invariant's harness,** `+"`invariant-explore.ts`"+`, which is beside it in your workspace, and which the gate writes in again when it runs. Import `+"`explore`"+` from `+"`./invariant-explore.ts`"+` and give it the initial nodes, one step for each step your model's Next names, and `+"`abstract(node)`"+`, the node's state in the spec's vocabulary (see below). A node holds the system's part, read out of the code, and the environment's own. The harness explores breadth first, tries every step with every argument in every node, records each attempt, and counts one size larger when the gate asks.
   - **Name each step exactly as Next does,** with every argument list Next passes it, in the spec's encoding: `+"`\\E a \\in Apis : MakeCall(a)`"+` is `+"`{ name: \"MakeCall\", args: [[{ $mv: \"a1\" }], [{ $mv: \"a2\" }]], take }`"+`. Steps of the environment alone, such as a tick of the clock or a Done, are steps too.
   - **`+"`take(node, ...args)`"+` makes the code from the node, calls the operation, and reads the state back,** then updates the environment's part. It returns the node the step reaches, which is a node equal to the one it got when the code refuses. It returns `+"`null`"+` only where the environment's bounds rule the step out, such as a sixth call when five is the bound. Never return `+"`null`"+` because of the state the code is in: a step the model rules out is one the code must refuse, and the gate checks that you tried it. A step of the environment that can't happen yet, such as a Done before the end, returns the node it got.
   - **Sizes the code takes are its parameters.** A capacity the code is made with isn't the environment's bound, so the driver still tries the step past it and sees the code refuse. Name those sizes, as the TLA+ constants they are, in `+"`\"parameters\"`"+` in `+"`.invariant/invariant.json`"+`: it's the one field of the manifest that's yours to write. Only numbers belong there, such as a capacity: a set of participants the code is made with needs no entry, because the gate never grows a set when it checks your steps.
4. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.
`, p.Manifest.Code, p.Manifest.Code, p.Manifest.Code, p.Manifest.Conformance, gateRuns)
		},
		checks: `- **Conformance:** TLC checks every run your driver recorded. Its first state must satisfy Init, and every step must be a Next step. Because the driver explores everything, it must also visit every state TLC finds in your model: the code and the model reach exactly the same states.
- **One size larger:** the gate makes every bound one size larger in your driver and runs it counting. It must reach exactly as many states as TLC's model does there, as deep.
- **Every step tried:** in every state your driver reaches, it tried every step Next names, with every argument. A step may go untried only where the environment's bounds alone rule it out.
- **Build:** ` + "`node --test`" + ` passes, and the driver runs.`,
		primer: encodingPrimer,
	},
	"python": {
		name:      "Python",
		protected: "",
		task: func(p *project.Project, gateRuns int) string {
			pkg := p.Manifest.Code
			return fmt.Sprintf(`2. **The code**, in Python, as package `+"`%s/`"+`, with the standard library only and no network. It's the system alone, written the way someone would ship it, and it must do what your model's system does (D-0068). Nagini proves the core.
   - `+"`%s/__init__.py`"+`.
   - `+"`%s/core.py`"+`, whose first line is `+"`# +nagini`"+`, so that Nagini verifies it. It holds the system:
     - **no bound is in it.** Sizes and limits are parameters: a capacity passed to `+"`__init__`"+`, a list that grows to it, and an operation that refuses when there's no room. No constant in it comes from the bounds above.
     - **only the system's own state is in it.** The model's environment and history stay out of the code: who acts, how many times, and what was sent, received or written. Those belong to the explorer.
     - **each operation has a contract that holds at every size.** `+"`Requires`"+` states the permissions it needs and when it may run. `+"`Ensures`"+` states the permissions it returns, its effect, and everything it leaves unchanged. No contract mentions a bound.
   - `+"`%s/explore.py`"+`, plain Python that Nagini doesn't check. It's the environment, and the only place the bounds live. The bounds are constants at its top, each named exactly after its TLA+ constant. A number is its value, and a set of model values is its size. It gives the initial nodes, `+"`abstract(node)`"+` for a node's state in the spec's vocabulary, and one `+"`Step`"+` from Invariant's harness for each step your model's Next names, as the driver below needs them. A node holds the system's part, read out of the core, and the environment's own.
   - Tests in `+"`test_%s.py`"+` at the project's root, with `+"`unittest`"+`.
3. **The conformance driver**, `+"`%s`"+`. It explores with Invariant's harness, `+"`invariant_explore`"+`, which is in your workspace and on the driver's path when the gate runs it: `+"`from invariant_explore import Step, explore`"+`. It calls `+"`explore(initial, steps, abstract)`"+` with what `+"`explore.py`"+` gives. The harness explores breadth first, tries every step with every argument in every node, records each attempt, and counts one size larger when the gate asks.
   - **Name each step exactly as Next does,** with every argument list Next passes it, in the spec's encoding: `+"`\\E a \\in Apis : MakeCall(a)`"+` is `+"`Step(\"MakeCall\", take, [[{\"$mv\": \"a1\"}], [{\"$mv\": \"a2\"}]])`"+`. Steps of the environment alone, such as a tick of the clock or a Done, are steps too.
   - **`+"`take(node, *args)`"+` makes the core from the node, calls the operation, and reads the state back,** then updates the environment's part. It returns the node the step reaches, which is a node equal to the one it got when the core refuses. It returns `+"`None`"+` only where the environment's bounds rule the step out, such as a sixth call when five is the bound. Never return `+"`None`"+` because of the state the core is in: a step the model rules out is one the core must refuse, and the gate checks that you tried it. A step of the environment that can't happen yet, such as a Done before the end, returns the node it got.
   - **Sizes the code takes are its parameters.** A capacity the core is made with isn't the environment's bound, so the driver still tries the step past it and sees the core refuse. Name those sizes, as the TLA+ constants they are, in `+"`\"parameters\"`"+` in `+"`.invariant/invariant.json`"+`: it's the one field of the manifest that's yours to write. Only numbers belong there, such as a capacity: a set of participants the code is made with needs no entry, because the gate never grows a set when it checks your steps.
4. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.
`, pkg, pkg, pkg, pkg, filepath.Base(pkg), p.Manifest.Conformance, gateRuns)
		},
		checks: `- **Code:** Nagini verifies every function in files marked ` + "`# +nagini`" + `: every contract, every list index, and that each function holds exactly the permissions it claims.
- **Conformance:** TLC checks every run your driver recorded. Its first state must satisfy Init, and every step must be a Next step. Because the driver explores everything, it must also visit every state TLC finds in your model: the code and the model reach exactly the same states.
- **One size larger:** the gate makes every bound one size larger in ` + "`explore.py`" + ` and runs your driver counting. It must reach exactly as many states as TLC's model does there, as deep.
- **Every step tried:** in every state your driver reaches, it tried every step Next names, with every argument. A step may go untried only where the environment's bounds alone rule it out.
- **Build:** the package compiles, and ` + "`unittest`" + ` passes.`,
		primer: encodingPrimer + `
# Nagini, briefly

Nagini verifies a typed subset of Python. Contracts are calls at the top of a function's body. Here's code with no model bounds in it, which Nagini proves at every capacity. It's a queue, not from this project:

` + "```python" + `
# +nagini
from typing import List

from nagini_contracts.contracts import *


class Queue:
    """A queue whose capacity is chosen when it's made."""

    def __init__(self, capacity: int) -> None:
        Requires(capacity > 0)
        self.capacity = capacity
        self.items = []  # type: List[int]
        Ensures(Acc(self.capacity) and Acc(self.items) and Acc(list_pred(self.items)))
        Ensures(self.capacity == capacity and len(self.items) == 0)

    def put(self, x: int) -> bool:
        Requires(Acc(self.capacity, 1/2) and Acc(self.items, 1/2) and Acc(list_pred(self.items)))
        Requires(len(self.items) <= self.capacity)
        Ensures(Acc(self.capacity, 1/2) and Acc(self.items, 1/2) and Acc(list_pred(self.items)))
        Ensures(len(self.items) <= self.capacity)
        Ensures(Implies(Old(len(self.items)) < self.capacity, Result() and ToSeq(self.items) == Old(ToSeq(self.items)) + PSeq(x)))
        Ensures(Implies(Old(len(self.items)) >= self.capacity, not Result() and ToSeq(self.items) == Old(ToSeq(self.items))))
        if len(self.items) < self.capacity:
            self.items.append(x)
            return True
        return False

    def get(self) -> int:
        Requires(Acc(self.capacity, 1/2) and Acc(self.items) and Acc(list_pred(self.items)))
        Requires(0 < len(self.items) and len(self.items) <= self.capacity)
        Ensures(Acc(self.capacity, 1/2) and Acc(self.items) and Acc(list_pred(self.items)))
        Ensures(len(self.items) == Old(len(self.items)) - 1)
        Ensures(Result() == Old(ToSeq(self.items))[0])
        Ensures(ToSeq(self.items) == Old(ToSeq(self.items)).drop(1))
        x = self.items[0]
        self.items = self.items[1:]
        return x
` + "```" + `

- **Say what a list holds through its sequence,** ` + "`ToSeq(items)`" + `, and write each contract in its terms. ` + "`PSeq(x)`" + ` is a one-element sequence, ` + "`+`" + ` joins sequences, and ` + "`.drop(n)`" + ` and ` + "`.take(n)`" + ` cut them.
- **An operation that may be refused** returns whether it ran, and its contract says both outcomes. The model's action is the one that ran.
- Every function states the permissions it needs in ` + "`Requires`" + ` and gives them back in ` + "`Ensures`" + `: ` + "`Acc(s.field)`" + ` for a field, and ` + "`Acc(list_pred(s.items))`" + ` for a list's contents.
- ` + "`Old(e)`" + ` is e's value on entry. ` + "`Forall(int, lambda i: ...)`" + ` and ` + "`Implies(a, b)`" + ` write quantified conditions.
- Nagini checks every list index, so bound each one in ` + "`Requires`" + `.
- Annotate every function and field. Keep to ints, bools and lists of them in the core. No dicts, sets or tuples, and no loops unless you give them invariants with ` + "`Invariant(...)`" + `.
- The code also runs as ordinary Python: the gate supplies a stand-in for ` + "`nagini_contracts`" + ` whose calls do nothing.
`,
	},
}

// existingLanguage is what the prompt says for a project that checks
// existing TypeScript code as it is (D-0054).
var existingLanguage = language{
	name:      "TypeScript",
	protected: " and package.json",
	task: func(p *project.Project, gateRuns int) string {
		rel := "the project's directory"
		up := "../../"
		if _, r, err := verify.PackageRoot(p.Dir); err == nil {
			rel, up = "`"+r+"`", strings.Repeat("../", strings.Count(r, "/")+1)
		}
		quoted := make([]string, len(p.Manifest.Existing))
		for i, e := range p.Manifest.Existing {
			quoted[i] = "`" + e + "`"
		}
		return fmt.Sprintf(`2. **The driver**, `+"`%s`"+`. The code it checks already exists: %s. Copies are in `+"`existing/`"+`, for you to read. The project sits at %s in its package, so import the code by its path from there, such as `+"`%s%s/...`"+`. The gate requires the driver to import every piece of code the project checks, and runs it with the package's own locked dependencies, in a sandbox with no network.
   - Run the real code. Create what it needs in memory, and give it its own injected clock where it takes one, so that time only moves when the driver moves it. Never use real time, the network, or files outside a temporary directory.
   - Make `+"`INVARIANT_RUNS`"+` runs of `+"`INVARIANT_STEPS`"+` steps each, from the environment. Choose each step at random, from a small generator seeded with `+"`INVARIANT_SEED`"+`, so every run is reproducible. Each step calls one of the code's operations, the ones the model's actions stand for, with arguments inside the bounds. When the code refuses an operation, nothing changes, and the driver carries on.
   - Record the state before the first step and after every step, in the spec's vocabulary: an object with one field per variable. Read every value back from the code, through its own methods or its storage, and never from what the driver expected to happen.
   - Never skip an operation because of the state the code is in. A step the model rules out is one the code must never take, so try it wherever a person or a worker could, and let the code refuse it. Skipping it hides exactly the bug this check exists to find.
   - Stay within the bounds. Never take an operation the bounds rule out, such as a third attempt when two is the bound. The bounds are the only reason to skip.
   - Write the runs to the file `+"`process.env.INVARIANT_TRACES`"+` names, as `+"`{\"traces\": [[state, ...], ...]}`"+`.
   - Helper files beside the driver are fine. Add no dependencies: use what the package already depends on, and Node's standard library.
3. **Keep the model faithful to the code.** The code is fixed, and the model isn't. If the gate shows the code taking a step the model doesn't allow, and allowing it keeps every ratified statement true, change the model's actions to allow it. If allowing it would break a ratified statement, stop changing things: the code has a bug, or a statement is wrong, and people decide which. Say so in your final message, with the step.
4. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.
`, p.Manifest.Conformance, strings.Join(quoted, ", "), rel, up, p.Manifest.Existing[0], gateRuns)
	},
	checks: `- **Conformance:** TLC checks every run your driver recorded. Its first state must satisfy Init, and every step that changes the state must be a Next step.
- **Existing code:** the driver imports every piece of code the project checks, and it runs in a sandbox with the package's locked dependencies and no network.`,
	primer: encodingPrimer,
}

// goEncoding says how a Go explorer writes the spec's vocabulary.
const goEncoding = `
In Go, ` + "`Abstract`" + ` builds these values from ` + "`map[string]any`" + ` and ` + "`[]any`" + `: a set is ` + "`map[string]any{\"$set\": []any{...}}`" + `, a function is ` + "`map[string]any{\"$fn\": []any{[]any{key, value}, ...}}`" + `, and a model value is ` + "`map[string]any{\"$mv\": \"p1\"}`" + `. ` + "`Try`" + ` passes each step's arguments the same way.
`

// encodingPrimer explains how a conformance driver writes a state.
const encodingPrimer = `
# The spec's vocabulary

A driver writes each state as a JSON object with one entry per variable of the spec. Values say what they are, because TLA+ can't tell a set from a sequence:

- strings, integers and booleans as themselves;
- a record as a plain object, such as ` + "`{\"type\": \"Prepared\", \"rm\": {\"$mv\": \"r1\"}}`" + `;
- a set as ` + "`{\"$set\": [...]}`" + `, and a sequence or tuple as ` + "`{\"$seq\": [...]}`" + `;
- a function as ` + "`{\"$fn\": [[key, value], ...]}`" + `;
- a model value from the bounds, such as ` + "`p1`" + `, as ` + "`{\"$mv\": \"p1\"}`" + `.

A bare array is an error. Write a small ` + "`abstract(s)`" + ` function that turns a state into this form, and use it for every state the driver records.
`
