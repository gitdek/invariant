package formalize

import (
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/synth"
)

// Prompt is the formalization task: the request, what to write, the rules
// the gate enforces, and a worked example.
func Prompt(req Request, checks int) string {
	previous := stepPrompt(req.ParentPlan)
	if req.Previous != nil && req.Previous.drafted() {
		previous += "\n# Your previous draft\n\nPeople asked for a revision. The complete previous draft, including its computed reader and comparison evidence, is in `previous/proposal.json`. Separate model, sibling includes and view files are in `previous/` for editing. These staged files are context, never checker authority. Their comments are in the discussion above. " +
			"Change what they asked for, and keep what they didn't mention.\n"
	}
	if authority := req.CheckContext(); authority.Baseline != nil {
		previous += "\n# Captured comparison baseline\n\nThe checker compares the complete candidate with " + authority.Baseline.Name + ". Read its captured model, includes and ratified.lock in `baseline/`. Those files are read-only context: editing them cannot change the checker's caller-owned baseline. "
		if authority.Baseline.View == "" {
			previous += "This legacy source has no view: supply a pure `baseline/view.json`; the checker validates it against the captured model and original bounds through model checks, complete TLC exploration, invariant replay and hiding checks. Repair that supplied view if its reader checks refuse. "
		} else {
			previous += "The captured view is fixed. If its reader checks refuse, " + authority.baselineViewRepair() + ". "
		}
		previous += "Each checker session reuses only successful complete baseline checks for identical sources and tools. Every candidate is checked again and each comparison is rebuilt; independent final validation freshly checks the baseline from caller-owned sources. Do not write comparison.json or reader.json. Equal charts do not establish behavioral equivalence; view changes and different checking scopes are qualified explicitly. Preserve faithful bounds even above the 500-state chart capacity.\n"
	}
	language := req.Language
	if language == "" {
		language = "go"
	}
	if c := req.Current; c != nil {
		language = c.Manifest.Language
		if language == "" {
			language = "go"
		}
		previous += fmt.Sprintf(`
# The project this changes

This issue changes an existing project, `+"`%s`"+`. Its captured ratification is already in the current directory and stays the baseline throughout this amendment. `+"`%s.tla`"+` holds its statements and its model, and `+"`proposal.json`"+` lists its statements as ratified (%s). Edit both files, and change only what the issue asks for:

- Add, change or remove statements as the issue requires. Keep every other statement's text and meaning word for word. The factory compares your draft with the ratified lock and shows people exactly what changed, and it calls out every statement you remove or change.
- Keep the module's name. Change the bounds, the model (`+"`Init`"+`, the actions and `+"`Next`"+`) and helper definitions as the change needs.
- The ratified statements are decisions already made, so don't ask about them again. Ask only about what the issue leaves open.
- If the issue isn't a change this project can take, set `+"`unsupported`"+` to one sentence saying why.
`, c.Dir, c.ModuleName(), c.Previous())
	}
	writes := "writes the code, " + Languages[language] + ","
	if e := req.Existing; e != nil {
		writes = "writes a driver that runs existing code, as it is,"
		previous += `
# The code this checks

This issue asks the factory to check code that already exists, without changing it. Copies are in ` + "`existing/`" + `, for you to read: ` + backticked(e.Paths) + `. Read the code, and its tests beside it. The factory never changes this code. A conformance driver will run it instead, and TLC will check every step it takes against your model. So:

- Draft the rules this code is meant to keep, as invariants: the ones its behavior, its tests and the issue show. Add known bugs for the mistakes that matter most, the kind the code's own tests guard against.
- Make the model a small, faithful picture of what the code does in the part the issue names: its states, and every step it can take. The code is checked against the model step by step. A model that allows less than the code does will fail, and one that allows more will let bugs through. Model only the state the rules need, in words a driver can read back from the code, such as each job's status.
- Keep the bounds small, such as one or two jobs, two workers and two attempts. A driver will run the real code within them.
- Don't read intent into the code. Where the code, its tests and the issue disagree, or where it's unclear what the code should do, ask a fork. Checking what the code does against rules nobody chose would prove nothing.
- ` + "`package`" + ` isn't used for existing code. Set it to the slug without dashes.
`
	}
	return fmt.Sprintf(`You're the formalization step of Invariant, a code factory. Someone with write access to %s asked the factory to take on issue #%d. Nothing gets built until a person ratifies formal statements that say what must be true. Then the factory %s and checks it against exactly those statements. Your job is to draft the statements in TLA+, with a draft model that shows they hang together. When the issue leaves a real decision open, your job is to ask instead of guessing.

# The request

%s%s
# What to write

Write three files in the current directory.

1. **`+"`<Module>.tla`"+`**: one TLA+ module, named in CamelCase after what it models. For example, `+"`BoundedBuffer.tla`"+` holds `+"`---- MODULE BoundedBuffer ----`"+`. In it:
   - `+"`CONSTANTS`"+` for the sizes and sets the model is checked with, and `+"`VARIABLES`"+` for the state.
   - `+"`vars == <<...>>`"+`, listing every variable.
   - The statements people will ratify (see below), and any helper definitions they use.
   - A draft model: `+"`Init`"+`, one operator per action, and `+"`Next`"+`. Model what the issue asks for and nothing more. The code will be written to match it.
   - `+"`Spec == Init /\\ [][Next]_vars`"+`, last.

   Invariant reads the definitions it pins the way TLC's parser does, and refuses a module it can't read exactly. So put the header on the module's first line, start every top-level definition at column 0 as `+"`Name == ...`"+` or `+"`Name(x) == ...`"+`, and indent its continuation lines. Write no `+"`INSTANCE`"+`, `+"`LOCAL`"+` or theorem, and no definition of an operator symbol; put a definition that is local to another in its `+"`LET`"+`; and don't name a record's field `+"`LET`"+` or `+"`IN`"+`. A module may extend Naturals, Integers, Sequences, FiniteSets, Bags and TLC, and regular sibling .tla files, which are read the same way and pinned with the statements that use them; the check snapshots their exact bytes too. Extend Integers if a statement uses `+"`\\div`"+`, which TLC rounds differently without it.
2. **`+"`proposal.json`"+`**, shaped like the example at the end. `+"`name`"+` is the project in a few plain words, `+"`slug`"+` a short kebab-case directory name, `+"`module`"+` the module's name and `+"`package`"+` a short lowercase package name for the code. `+"`bounds`"+` gives every CONSTANT a value, as a TLC config would. Undeclared names in a set, like `+"`p1`"+`, are model values.
3. **`+"`view.json`"+`**: a pure reader view, beside the model. It supplies literal labels and optional step hiding, never graph edges, rule truth or computed artifacts. The factory computes Mermaid from TLC evidence; do not write Mermaid or reader.json yourself.

The view has exactly `+"`version:1`"+`, `+"`state`"+` and `+"`step`"+`. Both mappings are nonempty ordered arrays of rules with `+"`id`"+`, `+"`when`"+` and `+"`label`"+`. IDs are unique per array; IDs and labels are nonblank strings. Only step rules may add boolean `+"`hide`"+` (default false). The first matching rule wins, and every state and step must match. For example:

`+"```json"+`
{"version":1,"state":[{"id":"all","when":true,"label":"Buffer"}],"step":[{"id":"all","when":true,"label":"Update buffer"}]}
`+"```"+`

Predicates are booleans, `+"`{\"eq\":[term,term]}`"+` for two like-typed terms, `+"`{\"all\":[predicate,...]}`"+`, `+"`{\"any\":[predicate,...]}`"+` or `+"`{\"not\":predicate}`"+`. Terms are literal strings or booleans, or a lookup: state rules use `+"`{\"var\":\"name\"}`"+`; step rules use `+"`{\"from\":\"name\"}`"+`, `+"`{\"to\":\"name\"}`"+` or `+"`{\"action\":true}`"+`. Lookups return exact TLC text as strings, including quotes around TLA+ strings. There is no numeric coercion, code execution, interpolation or I/O. Unknown fields, duplicate JSON keys, missing variables and mixed types refuse. All rules are statically checked, even after a catch-all. Hiding must cover a whole reader arrow, keep every chart state reachable from an initial state and keep an outgoing arrow wherever one existed; the check names the states, actions and view rules to repair.

Charts are drawn for models of up to 500 states. Above that capacity, passing model checks save a successful too_large limitation with the reachable count and checked bounds. Still supply a well-formed view; its mappings and hiding are not evaluated above capacity. Keep faithful bounds; do not shrink the model to obtain a chart.

# The statements

Each statement is a definition in the module, with a kind and `+"`says`"+`: one plain sentence that someone who doesn't read TLA+ can agree to. The statements are the point of all this. A person ratifies them, they're pinned by hash, and the code is proved against them.

- **spec**: exactly one, `+"`Spec`"+`, as above.
- **invariant**: holds in every reachable state. Always include `+"`TypeOK`"+`, which gives every variable its type. Then add the safety properties the issue implies: what must never go wrong. Keep each one small and about one thing.
- **witness**: a state predicate that some reachable state must satisfy. Add one for each outcome the system must be able to reach, so the invariants can't hold just because nothing happens.
- **bug**: an action a plausible wrong implementation would take, with `+"`expect`"+` naming the invariant it must break. The gate adds it to `+"`Next`"+` and requires TLC to find exactly that invariant violated, which shows the invariants have teeth. Add at least one, for the invariant that matters most. Define it in the module, but leave it out of `+"`Next`"+`.
- **property**: a temporal formula that must hold of every behavior, forever. Add one only when the issue says something must eventually happen, such as `+"`\\A c \\in Commands : c \\in pending ~> c \\in answered`"+`: every command that's asked is eventually answered. TLC checks each property alone, under the fairness statements. A bug may expect a property instead of an invariant: TLC must then find a behavior, under the same fairness, that breaks it.
- **fairness**: what the properties assume about progress. Each is one condition: `+"`WF_vars(A)`"+`, if `+"`A`"+` stays possible it eventually happens, or `+"`SF_vars(A)`"+`, if `+"`A`"+` is possible again and again it eventually happens, optionally for each `+"`x`"+` in a set, `+"`\\A x \\in S : WF_vars(A(x))`"+`. Make it about the system's own steps, never about people or the outside world: nothing can promise that a person acts. Leave fairness out when there are no properties.

The gate enforces these rules:

- Statements may depend on helper definitions, which are pinned along with them. They must not depend on `+"`Init`"+`, `+"`Next`"+` or the model's actions, which can still change. A fairness statement is the exception: the action it names is pinned with it, so define that action in full and make it a part of `+"`Next`"+`. The gate checks that every step of it is a `+"`Next`"+` step.
- Fairness goes in fairness statements, never in `+"`Spec`"+`.
- TLC always checks for deadlock: every reachable state needs an enabled action. If the system can legitimately finish, give it an action for that, such as one that leaves a finished state unchanged.
- TLC reports the first invariant it finds violated, in the order the statements are listed. List a bug's expected invariant before any other invariant the bug would also break.
- Keep the bounds small enough that TLC checks every state quickly: thousands of states, not millions.

# Don't guess

Look for every place where the issue allows materially different behavior that a person should choose, for example what happens at a limit, on a failure or a conflict, or in what order things happen. Don't pick one. Ask:

`+"```json"+`
"forks": [
  {"id": "F1", "question": "When ..., what should happen?", "options": [
    {"id": "A", "says": "..."},
    {"id": "B", "says": "..."}
  ]}
]
`+"```"+`

Each option says in one plain sentence what the system would do and supplies a preview. Leave top-level statements empty. Inside EVERY option add a preview object with fork and option matching its containing IDs, choices mapping every previously authorized fork ID to its option ID, and model holding an ordinary draft with name, slug, module, package, bounds and candidate statements, plus exact module_text, view text and includes (sibling .tla filenames mapped to source text). Each model is isolated, so options may reuse module names. Do not put nested forks, plans, ratification, receipts, fingerprints or reader output in a preview. All alternatives are unratified, model-only candidates; checking them neither chooses an answer nor starts a build.

check-context.json describes the caller-owned parent and prior choices staged before your run. Include all effective prior choices, including authorized revisions declared below, and no unanswered fork in choices. The illustrated alternative is identified separately by fork/option. Do not invent an answer for another unanswered fork: model only this option's behavior and the decisions already made. The checker retains its own authority independently of files you write.

The check tool independently checks EVERY option's inline snapshot and reports all failures together, under one whole-request check allowance. Repair every option failure; no partial option set can publish. The final check recomputes every option before publication. Keep faithful bounds even when a chart exceeds 500 states.

Don't ask about anything that doesn't change what must be true, such as names, data structures or the checking bounds. Choose those yourself. Never ask again about something already decided above. Plumbing, PRD and authenticated step_fit questions stay chart-free.

If a person's later comment in the discussion changes a decision above, follow the comment, and record the change in `+"`revised`"+`, so the record says what was decided:

`+"```json"+`
"revised": [{"fork": "F1", "option": "A", "by": "gitdek"}]
`+"```"+`

`+"`by`"+` is the login of the person whose comment asked for it. Never change a decision nobody asked to change.

If the issue isn't something this factory can build as a new, self-contained project, set `+"`unsupported`"+` to one sentence saying why, and leave everything else empty. If it's a change to code that isn't a state machine, say so: marked `+"`Kind: plumbing`"+`, the factory plans it instead (D-0105).

# Check your draft

The `+"`check`"+` tool pins your statements and runs the gate's model checks on your files, then checks view.json and computes the reader chart or size limitation. TLC must find no invariant violated and no deadlock, every witness must be reachable, every property must hold, every fairness statement's action must be a `+"`Next`"+` step, and every bug must be caught. Repair any view refusal as well as model failures. You have %d checks, so reread your files before each one. If you're proposing statements, you're done when the check passes. The factory independently recomputes from your final files. Then reply with a two-sentence summary.

%s

# An example

A complete draft for a different request: "Two processes share a printer. Only one may print at a time."

`+"`Mutex.tla`"+`:

`+"```tla"+`
---- MODULE Mutex ----
CONSTANTS Procs
VARIABLES pc

vars == <<pc>>

TypeOK == pc \in [Procs -> {"idle", "waiting", "printing"}]

OnePrinter == \A p, q \in Procs : p # q => ~(pc[p] = "printing" /\ pc[q] = "printing")

SomeonePrints == \E p \in Procs : pc[p] = "printing"

Init == pc = [p \in Procs |-> "idle"]

Request(p) == pc[p] = "idle" /\ pc' = [pc EXCEPT ![p] = "waiting"]

Enter(p) ==
    /\ pc[p] = "waiting"
    /\ \A q \in Procs : pc[q] # "printing"
    /\ pc' = [pc EXCEPT ![p] = "printing"]

Leave(p) == pc[p] = "printing" /\ pc' = [pc EXCEPT ![p] = "idle"]

Next == \E p \in Procs : Request(p) \/ Enter(p) \/ Leave(p)

\* A known bug: a process starts printing without checking whether another one is.
EnterUnchecked == \E p \in Procs : pc[p] = "waiting" /\ pc' = [pc EXCEPT ![p] = "printing"]

Spec == Init /\ [][Next]_vars
====
`+"```"+`

`+"`proposal.json`"+`:

`+"```json"+`
{
  "name": "shared printer",
  "slug": "shared-printer",
  "module": "Mutex",
  "package": "printer",
  "bounds": {"Procs": "{p1, p2}"},
  "statements": [
    {"name": "Spec", "kind": "spec", "says": "The system starts in Init, and every step is a Next step."},
    {"name": "OnePrinter", "kind": "invariant", "says": "Two processes never print at the same time."},
    {"name": "TypeOK", "kind": "invariant", "says": "Every process is always idle, waiting or printing."},
    {"name": "SomeonePrints", "kind": "witness", "says": "A process can get to print."},
    {"name": "EnterUnchecked", "kind": "bug", "says": "A process starts printing without checking whether another one is.", "expect": "OnePrinter"}
  ],
  "forks": []
}
`+"```"+`
`, req.Repo, req.Issue, writes, req.Markdown(), previous, checks, synth.WriteAsYouGo("the module first, then `proposal.json`", "checks"))
}

// backticked lists paths in code spans: `a`, `b` and `c`.
func backticked(paths []string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = "`" + p + "`"
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}
