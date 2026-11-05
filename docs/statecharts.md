# Reader statecharts and bounded graph data

Invariant's `internal/statechart` package computes reader charts from the complete bounded TLC graph, a lock and TLC-evaluated rule evidence. [#586's ratified plan](../.invariant/plans/issue-586.json) delivers this computation. The graph foundation in `internal/tlc` comes from [#575's ratified plan](../.invariant/plans/issue-575.json). Proposal rendering is not implemented; the dashboard retains its existing display and trace behavior.

## Reader computation

`Compute(context.Context, Input, []byte) (*Result, error)` is a pure evaluator. `Input` contains `Graph tlc.GraphResult`, `Lock project.Lock`, `Invariants map[string]map[string]bool` (concrete ID to invariant name to truth), and `Temporal []tlc.TraceFile`. The byte slice is a JSON view. Inputs and results share no mutable data; repeated and concurrent calls serialize identically when the graph entries are reordered. Callers must not mutate inputs during a call.

Each concrete state belongs to exactly one `State`, partitioned by its reader label and sorted false-invariant names. The stable `id` encodes that pair, independent of graph order. `members` lists every concrete identity; `initial_members` lists precisely those that are initial, and top-level `initial` lists their chart IDs. Equal labels cannot erase distinct invariant violations.

Every concrete step occurrence belongs to one `Arrow`, partitioned by `(from chart ID, reader label, to chart ID)` before hiding. `members` contains the original `tlc.GraphStep` occurrences, including duplicates, and `count` is their length. There are no shortcut edges, inferred stutters or sampled members. State collections sort by chart ID, concrete members and initial lists by ID, arrows by source/label/target and step members by source/action/target. Rule-name lists are sorted and unique. Locked statements retain lock order and wording; temporal traces sort by their JSON, preserving each trace's internal order.

`unavailable_actions` lists original model action names that occur elsewhere in the reachable graph but at no member of that chart state. It is computed before hiding: an action enabled at even one member is available, including when all its occurrences are hidden.

The current limit is **500 reachable concrete states**, inclusive, regardless of how many chart states or arrows result. Larger graphs return `too_large` with `Actual` and `Limit`. The target of **20 visible arrows** is only advice (`arrow_target` and `over_arrow_target`); exceeding it never truncates evidence.

## JSON view version 1

The root has exactly `version`, `state` and `step`:

```json
{
  "version": 1,
  "state": [
    {"id": "ready", "when": {"eq": [{"var": "phase"}, "\"idle\""]}, "label": "Ready"},
    {"id": "remaining", "when": true, "label": "Working"}
  ],
  "step": [
    {"id": "poll", "when": {"eq": [{"action": true}, "Poll"]}, "label": "Check progress", "hide": true},
    {"id": "remaining", "when": true, "label": "Continue"}
  ]
}
```

Both mappings are nonempty ordered rule arrays. Each rule has exactly `id`, `when`, `label`, and optionally `hide` for step rules. IDs and literal labels must be nonblank strings; IDs are unique within each mapping. `hide` must be boolean and defaults to false. State rules cannot hide states. The first matching rule wins; every concrete state and step must match.

| Expression | Meaning |
| :-- | :-- |
| `true`, `false` | Boolean literals |
| `{"eq":[term,term]}` | Equality of exactly two like-typed terms |
| `{"all":[when,...]}` | Conjunction; the empty array is true |
| `{"any":[when,...]}` | Disjunction; the empty array is false |
| `{"not":when}` | Boolean negation |

A term is a literal string or boolean, or exactly one lookup. State rules allow `{"var":"name"}`. Step rules allow `{"from":"name"}`, `{"to":"name"}` and `{"action":true}`. Variable lookups return exact TLC TLA+ **text**, including quotes and whitespace, as strings; the action lookup returns the original model action name. There is no numeric coercion. Missing variables refuse. Boolean expressions evaluate all operands, so an operand error cannot disappear behind a true disjunction or false conjunction. Later rules are not evaluated after a match, but all rules are structurally and statically type-checked before any mapping runs.

Views must be UTF-8 JSON, at most 1 MiB, with expression nesting at most 64 levels. Duplicate keys at any depth, unknown fields, trailing content, unsupported versions and ambiguous operators refuse, including in unreachable rules. The language has no imports, calls, loops, interpolation, mutation, environment, clock, randomness or host I/O. Text that resembles host code stays literal data. Views cannot supply graph, invariant or temporal evidence.

## Rule evidence

`EvaluateRules(context.Context, GraphDumper, dir string, graph tlc.GraphResult, lock project.Lock) (map[string]map[string]bool, error)` supplies the invariant matrix. `GraphDumper` has the existing `tlc.Runner.DumpGraph` signature, so a `tlc.Runner` satisfies it directly. The caller supplies the same immutable model snapshot used for the graph and the trusted lock and pinned toolchain.

The adapter validates graph completeness and counts, lock bounds and configured checks, then checks every locked statement's `tla.PinHash` against the source. It generates a temporary module extending the source, with one initial snapshot per concrete state, all original variable values, a fresh identity variable and a fresh record of invariant values. Only an explicit unchanged successor is permitted. TLC evaluates the original invariant operators; none are translated into Go. The replay uses the original constants and its own specification, with no invariant or property checks that could stop at the first false rule. A lock without invariants returns an empty row per concrete state without a replay.

The adapter calls `DumpGraph` once and inherits the runner's admission, isolation, memory limits and cancellation cleanup. Bare TLC model values receive fresh private constant aliases in the replay and its configuration; all original constants remain unchanged. It validates successful completion, the expected replay configuration and matching tool/version provenance. Every replay identity must join exactly once to an original state with the same variables. Comparison parses typed TLC values, ignoring layout whitespace outside strings and the order of set members, record fields and finite-function entries, including nested values. Tuple order, scalar types, function key types and quoted string contents remain exact. Printed integer intervals such as `1..2` are supported without expanding their members; equivalent enumerated integer sets compare equal. Malformed values and duplicate mapping keys refuse. Each rule record must contain exactly one boolean for every locked invariant. Unknown, duplicate, missing, changed or nonboolean evidence refuses with no matrix. Temporary source files are removed on every exit, and original model files, configuration and graph stay intact.

`Compute` requires this complete matrix, even when the graph itself checked some invariants. It refuses false values for invariants that the graph claims passed. Properties cannot appear in the state matrix. Temporal counterexamples must name locked properties, match the source module and TLC version, contain an ordered trace with a loop or stutter, and not contradict properties checked by the successful graph run. They remain trace facts in `report.temporal`; they never split states or become invented state violations. The report retains their JSON data without rounding integers.

These APIs validate evidence consistency; they do not authenticate an arbitrary caller's Go values or replace the gate. The caller owns the graph/source snapshot association, the lock's ratification and the tool pins. The pure evaluator cannot check source hashes itself. For a complete diagnostic graph containing failing invariants, dump without invariant/property checks, then evaluate rules separately. Verification still uses its original checks.

## Refusals and report

Every failed computation returns a nil result. `*Refusal` implements `error` with `Code`, `Message`, concrete `States`, concrete `Steps`, view `Rules`, and size `Actual`/`Limit`. Its diagnostic includes those fields. Cancellation preserves `context.Cause` through `errors.Is` and supplies no result.

| Code | Refused input |
| :-- | :-- |
| `invalid_view` | Malformed, unsupported, oversized or statically mistyped view |
| `invalid_graph` | Failed/inconsistent graph run, bad counts, identities, initials, endpoints, variables or concrete reachability |
| `invalid_evidence` | Incomplete or inconsistent lock, configuration, invariant matrix, replay or temporal facts |
| `unmapped_state`, `unmapped_step` | A concrete item matches no view rule |
| `evaluation` | A rule lookup fails, with its concrete item and rule ID |
| `partial_arrow` | Some occurrences in one source/label/target bucket hide while others show, even across original actions |
| `unreachable_chart` | Hiding disconnects a chart state from every chart initial |
| `empty_outgoing` | A chart state with outgoing buckets before hiding loses its last visible arrow |
| `too_large` | More than 500 concrete states |

Hiding checks run in the table's order starting at `partial_arrow`. Refusals include offending concrete states, step occurrences and hiding rule IDs. A genuinely terminal state needs no synthetic arrow. Every fully hidden bucket appears in `report.hidden` with all members, multiplicity and sorted `hiding_rules`.

`report.rules` preserves all locked statements verbatim, including whitespace in `Says`. `report.source` records `module`, detached `config` (including bounds), `tool` (image and jar hash) and `tlc_version`. `report.counts` defines independent quantities, with the same explanations in `report.count_meanings`:

| Count | Meaning |
| :-- | :-- |
| `tlc_states` | Distinct reachable concrete states |
| `tlc_steps` | All dumped step occurrences, including duplicates and loops |
| `tlc_initial_states` | Distinct concrete initial states |
| `tlc_generated_states` | TLC initial generation, including duplicates, plus dumped steps |
| `chart_states` | Reader-label/violated-invariant partitions |
| `chart_arrows` | Visible source/label/target buckets |
| `chart_arrows_before_hiding` | All source/label/target buckets |
| `shown_steps`, `hidden_steps` | Concrete occurrences in visible and fully hidden buckets |

Shown plus hidden steps always equals `tlc_steps`; generated states are not steps.

## Complete graph foundation

`ParseGraphDOT(string) (*Graph, error)` reads the pinned TLC `dot,actionlabels` format. `Graph.States` contains `GraphState` entries with a fingerprint `ID`, the complete decoded `Label`, and a `Values` map from variable names to exact TLA+ text. Values preserve string escapes, whitespace and continuation indentation. `Graph.Initial` lists every initial fingerprint. `Graph.Steps` contains every `GraphStep{From, Action, To}` occurrence in dump order, including self-loops, repeated entries and different actions between the same endpoints. Labels never replace identities, and no states or steps are sampled or deduplicated.

The parser consumes the entire document and rejects truncated or malformed records, missing labels or initial states, duplicate state declarations or variable assignments, inconsistent variable sets, and steps with undeclared endpoints. Parsing alone establishes structural validity, not a completed search.

`Runner.DumpGraph(context.Context, dir, module string, Config) (*GraphResult, error)` runs TLC with the supplied specification, constants, invariants and temporal properties. Deadlock checking remains enabled. The returned result contains:

| Field | Meaning |
| :-- | :-- |
| `Graph` | All parsed states, initials and dumped steps |
| `Run` | The completed TLC `Result`, including version, outcome, generated and distinct states, and search depth |
| `StateCount`, `StepCount` | Independently counted graph states and step occurrences |
| `Module`, `Config` | The module name and a detached configuration snapshot; `Config.Constants` includes the supplied bounds |
| `Tool.Image`, `Tool.JarSHA256` | The supplied Java image and lowercase SHA-256 of the jar snapshot actually mounted for the run |

Completion requires balanced tool-message frames, version and initial-generation records, success, final statistics, search depth and finish records in order. Errors override an earlier success message. The final queue must be empty; distinct states must match `StateCount`, and distinct initial states must match `Graph.Initial`. Total generated states must equal initial states generated plus `StepCount`. Initial generation can include duplicates, so it is counted separately from distinct initial states. Generated states are never reported as the graph's step count. Numeric overflow, malformed or missing records, tool failures and cancellation return an error and no graph. Progress messages count toward nothing, including message 2269, which TLC can print while it computes initial states ([D-0338](../decisions/journal/D-0338.jsonl)).

Both dump methods create a fresh temporary directory under the supplied model directory for configuration and output, and remove it on return. They consume only their own regular output file, refusing absent output or a symlink. Caller files such as `Invariant.cfg` and `states.dot` stay intact. `DumpGraph` also copies and hashes the jar there before running it. The caller still supplies the checksum-verified toolchain and digest-pinned Java image; the API records the actual jar hash rather than assuming the expected pin. The configuration snapshot has independent maps and slices.

Graph runs share the runner's TLC admission budget and container and Java heap limits. Canceled admission waits launch no container; running cancellation retains the caller's cause and joins Docker cleanup before releasing capacity. A canceled run never supplies graph evidence. The supplied context bounds the dump's lifetime.

These results describe the explored model within the supplied bounds, with TLC's fingerprint identities and guarantees. They are not an unbounded proof. The existing `Runner.Dump` returns raw DOT without claiming the completion guarantee. The dashboard continues to parse and compact its own display graph and match traces as before; its compaction does not alter this reusable graph.

## Tests

Reader acceptance and regression tests exercise total and typed mapping, complete concrete accounting, every hiding refusal, invariant signatures, temporal isolation, deterministic replay and concurrent evaluation, exact rule wording, detached snapshots, size boundaries and the advisory arrow target. Fake replay tests validate pins, generated TLA+ requests, complete identity/value joins, boolean evidence, cancellation and cleanup without changing the graph runner's fake contract. Value regressions cover intervals, reordered model-value sets and nested mappings, while refusing changed tuple order, types, string contents, interval endpoints and malformed values:

```sh
go test -timeout 20m ./internal/statechart ./internal/verify
```

`TestReader586PinnedTLC` always exercises offline computation. Its opt-in real case evaluates two independently failing invariants and a structured payload, then checks that chart partitioning preserves both failures and all steps. `TestReader586ReplayValuesPinnedTLC` adds opt-in replay regressions for a one-state interval model, `RM = {z, a}` model values and a structured payload containing those values. Missing tools fail the opt-in cases. `TestReader586RealIntegration` and `TestReader586ReplayValuesRealIntegration` invoke them in CI's existing integration shards:

```sh
INVARIANT_STATECHART_REAL=1 go test ./internal/verify -run '^TestReader586(ReplayValues)?PinnedTLC$' -count=1 -timeout 5m
go test -tags integration ./internal/verify -run '^TestReader586(ReplayValues)?RealIntegration$' -count=1 -timeout 5m
```

The pinned-format fixtures, refusal cases, lifecycle tests and dashboard compatibility checks run without Docker:

```sh
go test -timeout 20m ./internal/tlc ./internal/dashboard ./internal/verify
```

To run the real graph case locally with Docker and the checksum-verified pinned TLC:

```sh
INVARIANT_TLC_GRAPH_REAL=1 go test ./internal/verify -run '^TestGraph575PinnedTLC$' -count=1 -timeout 5m
```

The existing integration shards discover `TestGraph575RealIntegration` under the `integration` tag. It calls the same real case, covering multiple initial states, structured values, loops, parallel actions, counts and provenance, and fails if the tools cannot run:

```sh
go test -tags integration ./internal/verify -run '^TestGraph575RealIntegration$' -count=1 -timeout 5m
go test -tags integration ./internal/verify/ ./internal/conformance/
```

This work rests on [D-0016](../decisions/journal/D-0016.jsonl), [D-0049](../decisions/journal/D-0049.jsonl), [D-0105](../decisions/journal/D-0105.jsonl), [D-0122](../decisions/journal/D-0122.jsonl) and [D-0294](../decisions/journal/D-0294.jsonl). [D-0335](../decisions/journal/D-0335.jsonl) records the graph-evidence policy. TLC ends its dump's cluster with `{rank = same; ...}` layout groups, one for each search depth, and the parser reads each strictly: every ID must name a state already listed.

[D-0343](../decisions/journal/D-0343.jsonl) records #586's JSON-view and rule-evidence policy, refining [D-0335](../decisions/journal/D-0335.jsonl).
