package synth

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

var kindMeaning = map[string]string{
	project.Spec:      "TLC explores the behaviors of this operator.",
	project.Invariant: "Must hold in every reachable state.",
	project.Witness:   "Some reachable state must satisfy it, so the invariants can't hold just because the model does nothing.",
	project.Bug: "A bug the invariants must catch. The gate adds this action to your Next and requires TLC to find the " +
		"named invariant violated, so your model must be able to do the things this bug would do damage with.",
}

// Prompt is the synthesis task: the request, what was ratified, the rules,
// and the skeleton.
func Prompt(p *project.Project, skeleton, request string, gateRuns int) string {
	pkg := filepath.Base(p.Manifest.Code)
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

	return fmt.Sprintf(`You're the synthesis step of Invariant, a code factory. People have ratified formal statements about a system. Your job is to write the TLA+ model and the Go code that satisfy them, and to show they do by passing the gate.

# The request

%s

# What was ratified

These statements are pinned by hash in `+"`.invariant/ratified.lock`"+`. Their definitions are already in `+"`%s`"+`. Don't change them, or anything they depend on. The gate hashes their text and fails if it changes. The lock, the manifest, the request and go.mod are protected: the gate always uses the originals.

%s
%s
TLC checks everything with these constants: %s.

# Your job

1. **The model**, in `+"`%s`"+`. The skeleton below holds the pinned definitions and a marked place for the model. Write `+"`Init`"+`, one operator per action, and `+"`Next`"+` there, above `+"`Spec`"+`. Use only the declared constants and variables.
2. **The code**, in `+"`%s/`"+` as Go package `+"`%s`"+`. It must implement your model exactly.
   - A comparable `+"`State`"+` struct that represents the spec's variables for the bounds above, one to one. Use fixed-size arrays, booleans and small integer types; no slices, maps or pointers.
   - `+"`func Init() State`"+`: the initial state.
   - One exported step function per TLA+ action. It takes the current state (and the action's parameters as ints, if any) and returns the next state. Directly above each one, a Gobra contract: `+"`// @ requires`"+` lines for the action's enabling condition and parameter bounds, and `+"`// @ ensures`"+` lines for its effect and for everything it leaves unchanged.
   - `+"`func Successors(s State) []State`"+`: the state after every action enabled in s, the way TLC expands Next. Put it alone in `+"`explore.go`"+`, without the Gobra header. Gobra can't verify Go's append.
   - Every other Go file starts with the line `+"`// +gobra`"+`, then a blank line, then the package clause, so that Gobra verifies it.
   - Standard library only. No cgo, goroutines, network or file access.
   - Tests in `+"`_test.go`"+` files are welcome. They run in a sandbox with no network.
3. **Check your work with the `+"`gate`"+` tool.** It runs every check and says exactly what failed. You have %d gate runs in total, so reread your files carefully before each run. You're done when the gate passes. Then reply with a short summary.

# What the gate checks

- **Pins:** the ratified definitions are unchanged.
- **Design:** TLC finds no invariant violation and no deadlock in any reachable state of `+"`Spec`"+`.
- **Reachability:** every witness is reachable.
- **Known bugs:** every bug action, added to Next, makes TLC find its expected violation.
- **Agreement:** exploring your Go code from `+"`Init()`"+` through `+"`Successors()`"+` reaches exactly as many distinct states as TLC finds in your model, at the same depth. The Go state space must match the model's one to one.
- **Code:** Gobra verifies every function in files marked `+"`// +gobra`"+`, including array bounds and integer overflow.
- **Build:** go vet and go test pass.

# Gobra, briefly

A contract is a block of comment lines directly above a function. A generic example, not from this project:

`+"```go"+`
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
`+"```"+`

- A loop needs its invariants on the lines directly above the `+"`for`"+`, such as `+"`// @ invariant 0 <= i && i <= N`"+`.
- Contracts can use `+"`forall`"+`, `+"`exists`"+`, `+"`==>`"+` and `+"`==`"+` on whole arrays (`+"`t.Count == s.Count`"+`).
- Calls to a function with a contract must satisfy its `+"`requires`"+`, so guard them.

# The skeleton of `+"`%s`"+`

`+"```tla"+`
%s`+"```"+`
`, strings.TrimSpace(request), p.Manifest.Module, statements.String(), kinds.String(), strings.Join(bounds, ", "),
		p.Manifest.Module, p.Manifest.Code, pkg, gateRuns, p.Manifest.Module, skeleton)
}
