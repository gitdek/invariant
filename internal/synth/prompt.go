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
	for _, k := range []string{project.Spec, project.Invariant, project.Witness, project.Bug} {
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
			return fmt.Sprintf(`2. **The code**, in `+"`%s/`"+` as Go package `+"`%s`"+`. It must implement your model exactly.
   - A comparable `+"`State`"+` struct that represents the spec's variables for the bounds above, one to one. Use fixed-size arrays, booleans and small integer types; no slices, maps or pointers.
   - `+"`func Init() State`"+`: the initial state.
   - One exported step function per TLA+ action. It takes the current state (and the action's parameters as ints, if any) and returns the next state. Directly above each one, a Gobra contract: `+"`// @ requires`"+` lines for the action's enabling condition and parameter bounds, and `+"`// @ ensures`"+` lines for its effect and for everything it leaves unchanged.
   - `+"`func Successors(s State) []State`"+`: the state after every action enabled in s, the way TLC expands Next. Put it alone in `+"`explore.go`"+`, without the Gobra header. Gobra can't verify Go's append.
   - Every other Go file starts with the line `+"`// +gobra`"+`, then a blank line, then the package clause, so that Gobra verifies it.
   - Standard library only. No cgo, goroutines, network or file access.
   - Tests in `+"`_test.go`"+` files are welcome. They run in a sandbox with no network.
3. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.
`, p.Manifest.Code, filepath.Base(p.Manifest.Code), gateRuns)
		},
		checks: `- **Agreement:** exploring your Go code from ` + "`Init()`" + ` through ` + "`Successors()`" + ` reaches exactly as many distinct states as TLC finds in your model, at the same depth. The Go state space must match the model's one to one.
- **Code:** Gobra verifies every function in files marked ` + "`// +gobra`" + `, including array bounds and integer overflow.
- **Build:** go vet and go test pass.`,
		primer: `# Gobra, briefly

A contract is a block of comment lines directly above a function. A generic example, not from this project:

` + "```go" + `
// +gobra

package counter

const N = 3

type State struct {
	Count [N]int
	Done  bool
}

// Inc mirrors the TLA+ action Inc(i).
// @ requires 0 <= i && i < N
// @ requires !s.Done && s.Count[i] < 10
// @ ensures t.Count[i] == s.Count[i] + 1
// @ ensures forall j int :: 0 <= j && j < N && j != i ==> t.Count[j] == s.Count[j]
// @ ensures t.Done == s.Done
func Inc(s State, i int) (t State) {
	t = s
	t.Count[i] = s.Count[i] + 1
	return t
}
` + "```" + `

- A loop needs its invariants on the lines directly above the ` + "`for`" + `, such as ` + "`// @ invariant 0 <= i && i <= N`" + `.
- Contracts can use ` + "`forall`" + `, ` + "`exists`" + `, ` + "`==>`" + ` and ` + "`==`" + ` on whole arrays (` + "`t.Count == s.Count`" + `).
- Calls to a function with a contract must satisfy its ` + "`requires`" + `, so guard them.
`,
	},
	"typescript": {
		name:      "TypeScript",
		protected: " and package.json",
		task: func(p *project.Project, gateRuns int) string {
			return fmt.Sprintf(`2. **The code**, in TypeScript in `+"`%s/`"+`, as a state machine that implements your model exactly. Node 24 runs `+"`.ts`"+` files directly by stripping their types, so use only erasable syntax: types, interfaces and `+"`as`"+` are fine, but not `+"`enum`"+`, `+"`namespace`"+` or constructor parameter properties. Import local files with their `+"`.ts`"+` extension. No dependencies: Node's standard library only, and no network.
   - A module, such as `+"`%s/machine.ts`"+`, that exports:
     - `+"`type State`"+`: plain data (numbers, booleans, strings, arrays and plain objects) that represents the spec's variables for the bounds above, one to one.
     - `+"`init(): State`"+`: the initial state.
     - One function per TLA+ action. It takes the state, and the action's parameters if any, and returns the next state, or `+"`null`"+` when the action isn't enabled. It never changes its argument.
     - `+"`successors(s: State): State[]`"+`: the state after every enabled action, the way TLC expands Next.
     - `+"`key(s: State): string`"+`: a canonical string for a state, so that equal states have equal keys.
   - Tests beside it, such as `+"`%s/machine.test.ts`"+`, with `+"`node:test`"+` and `+"`node:assert`"+`. They run with `+"`node --test`"+`.
3. **The conformance driver**, `+"`%s`"+`. It explores your state machine completely, breadth first from `+"`init()`"+` through `+"`successors()`"+`. For every step the code can take, it records one run: the path from the initial state to the step's start, then the state the step reaches. It writes the runs, each state in the spec's vocabulary (see below), to the file named by `+"`process.env.INVARIANT_TRACES`"+`, as `+"`{\"traces\": [[state, ...], ...]}`"+`.
4. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.
`, p.Manifest.Code, p.Manifest.Code, p.Manifest.Code, p.Manifest.Conformance, gateRuns)
		},
		checks: `- **Conformance:** TLC checks every run your driver recorded. Its first state must satisfy Init, and every step must be a Next step. Because the driver explores everything, it must also visit every state TLC finds in your model: the code and the model reach exactly the same states.
- **Build:** ` + "`node --test`" + ` passes, and the driver runs.`,
		primer: encodingPrimer,
	},
	"python": {
		name:      "Python",
		protected: "",
		task: func(p *project.Project, gateRuns int) string {
			pkg := p.Manifest.Code
			return fmt.Sprintf(`2. **The code**, in Python, as package `+"`%s/`"+`, with the standard library only and no network. Nagini proves the core.
   - `+"`%s/__init__.py`"+`.
   - `+"`%s/core.py`"+`, whose first line is `+"`# +nagini`"+`, so that Nagini verifies it. It holds:
     - the state as a class whose fields represent the spec's variables for the bounds above, one to one, as ints, bools and fixed-length lists. Its `+"`__init__`"+` mirrors Init.
     - one function per TLA+ action, which changes the state in place. Its contract restates the action: `+"`Requires`"+` for the permissions it needs, the parameters' bounds and the enabling condition, and `+"`Ensures`"+` for the permissions it returns, the effect, and everything it leaves unchanged.
   - `+"`%s/explore.py`"+`, plain Python that Nagini doesn't check: `+"`key(s)`"+` for a canonical tuple, `+"`copy(s)`"+`, and `+"`successors(s)`"+` for the state after every enabled action, the way TLC expands Next.
   - Tests in `+"`test_%s.py`"+` at the project's root, with `+"`unittest`"+`.
3. **The conformance driver**, `+"`%s`"+`. It explores the core completely, breadth first from a new state through `+"`successors()`"+`. For every step the code can take, it records one run: the path from the initial state to the step's start, then the state the step reaches. It writes the runs, each state in the spec's vocabulary (see below), to the file named by the environment variable `+"`INVARIANT_TRACES`"+`, as `+"`{\"traces\": [[state, ...], ...]}`"+`.
4. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.
`, pkg, pkg, pkg, pkg, filepath.Base(pkg), p.Manifest.Conformance, gateRuns)
		},
		checks: `- **Code:** Nagini verifies every function in files marked ` + "`# +nagini`" + `: every contract, every list index, and that each function holds exactly the permissions it claims.
- **Conformance:** TLC checks every run your driver recorded. Its first state must satisfy Init, and every step must be a Next step. Because the driver explores everything, it must also visit every state TLC finds in your model: the code and the model reach exactly the same states.
- **Build:** the package compiles, and ` + "`unittest`" + ` passes.`,
		primer: encodingPrimer + `
# Nagini, briefly

Nagini verifies a typed subset of Python. Contracts are calls at the top of a function's body. A generic example, not from this project:

` + "```python" + `
# +nagini
from typing import List

from nagini_contracts.contracts import *

N = 2


class State:
    def __init__(self) -> None:
        self.count = [0, 0]  # type: List[int]
        self.done = False
        Ensures(Acc(self.count) and Acc(list_pred(self.count)) and len(self.count) == N)
        Ensures(Acc(self.done))
        Ensures(Forall(int, lambda i: Implies(0 <= i and i < N, self.count[i] == 0)))
        Ensures(not self.done)


def inc(s: State, i: int) -> None:
    """Mirrors the TLA+ action Inc(i)."""
    Requires(Acc(s.count) and Acc(list_pred(s.count)) and len(s.count) == N)
    Requires(Acc(s.done))
    Requires(0 <= i and i < N)
    Requires(not s.done and s.count[i] < 3)
    Ensures(Acc(s.count) and Acc(list_pred(s.count)) and len(s.count) == N)
    Ensures(Acc(s.done))
    Ensures(s.count[i] == Old(s.count[i]) + 1)
    Ensures(Forall(int, lambda j: Implies(0 <= j and j < N and j != i, s.count[j] == Old(s.count[j]))))
    Ensures(s.done == Old(s.done))
    s.count[i] = s.count[i] + 1
` + "```" + `

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
