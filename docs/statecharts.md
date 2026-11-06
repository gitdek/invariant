# Reader statecharts and bounded graph data

Invariant's `internal/statechart` package computes reader charts from the complete bounded TLC graph, a lock and TLC-evaluated rule evidence. [#586's ratified plan](../.invariant/plans/issue-586.json) delivers this computation. The graph foundation in `internal/tlc` comes from [#575's ratified plan](../.invariant/plans/issue-575.json). [#603's ratified plan](../.invariant/plans/issue-603.json) connects that evidence to modeled proposals, revisions and amendments. [#608's ratified plan](../.invariant/plans/issue-608.json) adds standalone SVG rendering through the shared Go API. [#631's ratified plan](../.invariant/plans/issue-631.json) connects receipt archives to [dashboard evidence cards](dashboard.md#reader-evidence), script-free pages and standalone SVG. Raw state graphs remain separate detail.

## Modeled proposals

The drafting agent writes a pure `view.json` beside the model and `proposal.json`, using the grammar below. Both the check tool and the independent final check snapshot the exact model, sibling TLA+ includes, view, statements and bounds. The existing model gate still checks pins, invariants, deadlock, witnesses, properties, fairness and known bugs. After it passes, the reader dumps the complete TLC graph at those bounds, replays invariants through TLC and computes the chart for graphs through **500 reachable concrete states**, inclusive. Missing, malformed or dishonest views refuse with the evaluator's actionable states, model actions and view-rule IDs. Derived files supplied by an agent are never evidence; final checking recomputes from the final source files.

Above 500 states, passing model checks instead save a successful `too_large` limitation with the reachable count, capacity, module, configuration, Java image, jar hash and TLC version. The view must still be structurally and statically well-formed. Its lookups, totality and hiding are not evaluated at that size, and no chart is saved. Drafting keeps the guidance **thousands of states, not millions**: charts are drawn for models of up to 500 states, without asking models to shrink.

`Proposal.view` holds the exact view text and `Proposal.includes` maps each sibling `.tla` filename to its exact bytes. `Proposal.reader` has `version:1`, a `sha256:` fingerprint and exactly one of `chart` or `limitation`. The fingerprint binds the full model, include names and bytes, exact view, bounds, pinned statements and wording, tool/configuration provenance and all artifact contents. It is separate from the proposal's statement hash and adds no proof claim. A change to any bound input or the artifact requires a fresh check.

The view, includes and snapshot survive `formalization.json`, compressed proposal markers, completed-run recovery, `previous/` staging and amendments. Ratification writes the model and includes under the manifest's model directory, and `.invariant/view.json` and `.invariant/reader.json` beside the lock. Existing lock and amendment identities stay unchanged. Older stored proposals without reader evidence remain readable and writable; publishing a new modeled proposal, including a recovered draft, requires valid reader evidence. Input reads take only regular files, refusing symbolic and hard links without reading their contents.

Supported proposals render literal-safe Mermaid `stateDiagram-v2`: aliases are generated independently of labels, every displayed arrow comes from the computed chart and every initial chart state has an initial arrow. Tables retain exact rule wording, per-state unavailable actions, all hidden step occurrences and separately defined TLC and chart counts. Mermaid and Markdown escaping preserves literal labels and wording, including text that resembles markup. Above capacity the proposal keeps its statement or amendment table, existing checks and ratification instructions, with a short note that TLC reached the recorded number of states at the checked bounds, more than the 500 charts are drawn for. Plumbing proposals and plans of issues have no chart. A complete modeled comment that exceeds the publication limit is explicitly refused for human review, never clipped into a usable ratification proposal. The saved marker must also read back within the existing 4 MiB expanded limit, regardless of its compressed size; a refusal omits the oversized proposal payload.

## Verification receipts

[#615's ratified plan](../.invariant/plans/issue-615.json) connects the same graph, invariant replay and view evaluation to modeled verification, synthesis and factory gate receipts. Before checks begin, verification captures the actual model, its sibling TLA+ includes, the loaded lock and the optional `.invariant/view.json`. All bounded model checks and the reader use those captured source bytes. A saved proposal's `.invariant/reader.json` is never substituted for the final model. Synthesis supplies the original project's view and sibling includes during preparation and assembly, discards workspace reader artifacts and recomputes after the model changes.

Verification JSON has an optional `reader` object. New modeled receipts use version 1, with a separate `sha256:` fingerprint and `inputs`: `module`, `model_sha256`, `view_sha256`, and `includes`, a map of sibling filenames to byte hashes. The reader fingerprint binds these exact inputs, the lock's bounds and statements, tool/configuration provenance and computed contents. It does not change the verification fingerprint, proposal identity, lock, pass/fail result or code assurance.

Exactly one outcome is present:

| Field | Contents |
| :-- | :-- |
| `chart` | The full `statechart.Result`, including all state memberships, hidden occurrences, exact says words, unavailable actions, counts and bounds |
| `limitation` | `code: "too_large"`, `actual`, `limit: 500`, and `source` with module, bounds/configuration, Java image, jar hash and TLC version |
| `unavailable` | A diagnostic `code` and `message`, plus `source` tool/configuration provenance when a completed graph is available, without an invented or partial chart |

Every ordinary graph through 500 reachable concrete states is supported. Impossible-action statements require complete exploration and are computed before hiding. The 20-arrow target never truncates a chart. Above 500, the view is checked for well-formedness but its mappings and hiding are not evaluated. Missing views report `missing_view`; unsafe file links are refused without reading their contents. Malformed views, graph or invariant replay failures and SVG rendering failures retain separate reader diagnostics. A failed build can still carry a complete model chart, and remains failed. A failed design check carries no chart. Old reports without reader data still decode with their original evidence identity and explicitly explain that a view and a fresh verification are needed.

`verify -out DIR` and synthesis/factory `gate/` directories use the same writer:

| File | Contents |
| :-- | :-- |
| `receipt.json` | Complete verification report, including its reader record |
| `receipt.md` | Verification evidence plus literal-safe Mermaid and reader report, or a size/absence explanation |
| `reader.json` | The same standalone reader record, when one is present |
| `reader.svg` | A drawable chart and report from the native Go renderer, with the complete result as inert metadata |

The SVG works offline and contains no active content or external resources. Reusing an output directory removes obsolete reader JSON and SVG before writing current artifacts. Ordinary filesystem output errors are still errors. Existing whole-directory CI uploads and archives retain these files without workflow changes. Factory pull request bodies use the final gate receipt, even when a proposal has a different chart; plumbing receipts remain chart-free. Reader diagnostics and rendering add no proof claim and never upgrade or fail the verification verdict. The dashboard validates these companions and their source snapshot at the receipt commit before presenting the chart.

## Example inventory

All ten modeled examples keep their original model and ratified-lock bytes and bounds. Nine have a pure `.invariant/view.json`; language and synthesized siblings each carry their own view and receipt identity. Labels use the examples' ordinary vocabulary. Two-phase commit distinguishes preparing, a coordinator decision, all committed and all aborted; log buffers distinguish empty, buffered and awaiting retry; the connection pool distinguishes all connections free from clients holding connections. Only the pool requests hiding, for `Refuse` self-loops, which remain in the complete report. The common reader checks total mappings and hiding on complete exploration, never on a compacted graph or a sample.

| Example | Unchanged ratified bounds | Reachable states | Saved view |
| :-- | :-- | --: | :-- |
| `02-twophase-commit` | `RM = {r1, r2, r3}` | 288 | Yes |
| `02-twophase-commit-py` | `RM = {r1, r2, r3}` | 288 | Yes |
| `02-twophase-commit-py-proved` | `RM = {r1, r2, r3}` | 288 | Yes |
| `02-twophase-commit-synthesized` | `RM = {r1, r2, r3}` | 288 | Yes |
| `02-twophase-commit-ts` | `RM = {r1, r2, r3}` | 288 | Yes |
| `03-log-buffer` | `Producers = {p1, p2}`, `Capacity = 2`, `MaxLines = 2` | 87 | Yes |
| `03-log-buffer-py` | `Producers = {p1, p2}`, `Capacity = 2`, `MaxLines = 2` | 87 | Yes |
| `03-log-buffer-ts` | `Producers = {p1, p2}`, `Capacity = 2`, `MaxLines = 2` | 87 | Yes |
| `04-api-rate-limiter` | `Apis = {a1, a2}`, `Capacity = 2`, `MaxCalls = 5`, `MaxTime = 3`, `MaxWaiting = 2` | 19,489 | No; above chart capacity |
| `05-connection-pool` | `Clients = {c1, c2, c3}`, `Size = 2` | 13 | Yes |

The default acceptance test checks the inventory, protected model/lock hashes, pure view grammar and vocabulary. It does **not** claim a completed TLC exploration. Before merging this change, the merger must complete the following pinned-tool validation with Docker:

```sh
INVARIANT_DASHBOARD_READER_REAL=1 go test ./internal/dashboard \
  -run '^TestReader631ExampleViews$' -count=1 -timeout 30m
```

The opt-in cannot skip missing tools. It completely explores every example at the unchanged bounds with pinned TLC, checks the inventoried counts, runs the model gate and common reader, and requires a drawable chart for every qualifying sibling. Production receipt writing then carries those records through a whole-directory ZIP archive into visible dashboard images and evidence pages with separate sibling identities. The rate limiter must retain its explicit missing-view diagnostic. The merge's full gate must also publish chart-bearing verification receipts for all nine qualifying siblings, with the existing real Python and TLAPS suites required where applicable. Docker validation that cannot run in the plumbing sandbox is merger work in this same change, not evidence supplied by the offline grammar check. On 2026-11-05 it passed on #634's merged head, after #636's replay fix, for all nine qualifying siblings.

The dashboard's [presentation and cache policy](dashboard.md#reader-evidence) rests on D-0343, D-0347, D-0349 and D-0351. [D-0353](../decisions/journal/D-0353.jsonl) records it, refining [D-0351](../decisions/journal/D-0351.jsonl).

## Reader computation

`Compute(context.Context, Input, []byte) (*Result, error)` is a pure evaluator. `Input` contains `Graph tlc.GraphResult`, `Lock project.Lock`, `Invariants map[string]map[string]bool` (concrete ID to invariant name to truth), and `Temporal []tlc.TraceFile`. The byte slice is a JSON view. Inputs and results share no mutable data; repeated and concurrent calls serialize identically when the graph entries are reordered. Callers must not mutate inputs during a call.

Each concrete state belongs to exactly one `State`, partitioned by its reader label and sorted false-invariant names. The stable `id` encodes that pair, independent of graph order. `members` lists every concrete identity; `initial_members` lists precisely those that are initial, and top-level `initial` lists their chart IDs. Equal labels cannot erase distinct invariant violations.

Every concrete step occurrence belongs to one `Arrow`, partitioned by `(from chart ID, reader label, to chart ID)` before hiding. `members` contains the original `tlc.GraphStep` occurrences, including duplicates, and `count` is their length. There are no shortcut edges, inferred stutters or sampled members. State collections sort by chart ID, concrete members and initial lists by ID, arrows by source/label/target and step members by source/action/target. Rule-name lists are sorted and unique. Locked statements retain lock order and wording; temporal traces sort by their JSON, preserving each trace's internal order.

`unavailable_actions` lists original model action names that occur elsewhere in the reachable graph but at no member of that chart state. It is computed before hiding: an action enabled at even one member is available, including when all its occurrences are hidden.

The current limit is **500 reachable concrete states**, inclusive, regardless of how many chart states or arrows result. Larger graphs return `too_large` with `Actual` and `Limit`. The target of **20 visible arrows** is only advice (`arrow_target` and `over_arrow_target`); exceeding it never truncates evidence.

## Standalone SVG

`(*statechart.Result).SVG() (string, error)` renders a complete checked ordinary chart and its report as self-contained SVG XML. Call it on the result of `Compute`:

```go
chart, err := statechart.Compute(ctx, input, viewJSON)
if err != nil {
    return err
}
svg, err := chart.SVG()
if err != nil {
    return err
}
// The caller can save or serve svg as image/svg+xml.
```

Layout and XML serialization run entirely in Go, without host fonts, a browser, rendering executables, network access or external resources. Repeated and concurrent calls on the same input return identical bytes and never mutate any part of the result. Callers must not mutate it during rendering. The renderer checks structure and representability; the caller still owns the checked evidence, snapshot association, ratification and tool pins. Rendering a hand-built result does not authenticate it.

Every chart state, initial and visible arrow is drawn, including all self-loops, parallel arrows and reverse arrows. [#613's ratified plan](../.invariant/plans/issue-613.json) arranges states in left-to-right columns by their shortest visible-arrow distance from any initial state. Hidden arrows do not affect these distances. Stable neighbour-ordering passes reduce crossings within those columns; a forward chain has no crossings. Each rectangular state outline is sized from its own displayed text, independently of incident arrows and other states' labels. States are numbered in reading order, column by column and then top to bottom within a column, so initial states come first ([D-0354](../decisions/journal/D-0354.jsonl)). The drawing, each arrow's description, the initial indicators and the report use the same numbers, and the report lists states and arrows in that order.

Forward routes stay between adjacent columns. Same-column arrows and self-loops use separate side lanes, and back arrows return below the drawing. Each incidence has its own connection point; literal arrow labels occupy reserved areas on their own routes, clear of states and other labels. Vertical legs stay outside those label areas. Explicit newlines become positioned SVG lines, and long lines remain complete. The full report follows below the drawing in up to three balanced text columns at the same readable font size. State and occurrence captions use singular and plural wording. Large charts and long labels can still produce large images; the viewBox contains the entire drawing and report. The existing 500-concrete-state capacity applies, with no additional arrow cutoff or sampling; the 20-arrow target remains visible advice.

The report displays bounded-model qualifications, constants, verbatim locked wording and pins, state membership and initial membership, violated invariant rules, unavailable model actions, every visible and hidden concrete step occurrence (including duplicates), hiding view-rule IDs, independent counts and their exact meanings, source configuration and tool provenance. Temporal counterexamples include their ordered states, values and loop or stutter as trace facts; they do not become state violations. Neither the image nor the report adds a proof claim. `metadata#statechart-data` contains the entire `Result` as inert JSON text, preserving all fields and occurrences.

The inspectable XML contract is:

| Element | Meaning |
| :-- | :-- |
| `g[data-state-id]` | One per checked state, with one rectangular outline, its exact label in `title`, and visible text |
| `g[data-initial]` | One per initial chart ID, with a directed path to its state |
| `g[data-from][data-to][data-label]` | One per visible arrow, with its exact label in `title`, visible text and a directed path |
| `metadata#statechart-data` | Full checked result as JSON, XML-escaped as text |
| Root `title` and `desc` | Meaningful accessible text, linked by `aria-labelledby` and `aria-describedby`; the root has `role="img"` |

Element IDs are generated independently of reader labels and chart IDs. Text and attributes are XML-escaped, including markup-like text, quotes and URLs. Styling uses presentation attributes; arrowheads refer only to a local marker. There are no scripts, event handlers, stylesheets, links, foreign objects, embedded resources, DTDs or resource-loading attributes. Whitespace in rule wording and labels is preserved. Coordinates, ordering, colors, fonts and routing are implementation choices, not a pinned serialization format.

Nil or empty results, unsupported versions, missing or repeated initials, unknown endpoints, repeated state or arrow identities, inconsistent concrete membership, visibility or counts, and invalid reachability return an error and an empty string. Invalid UTF-8 and characters XML 1.0 cannot express also fail, including in metadata-only fields; nothing is silently replaced or dropped. Non-JSON temporal values fail too. Verification receipts save this SVG beside their JSON and Markdown. Modeled proposals retain Mermaid publication; the dashboard serves the same native SVG on modeled evidence cards.

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

SVG acceptance and regression tests inspect parsed XML for complete topology, actual endpoints, distinct routes, shortest-distance columns, branch ordering, independent state sizing, label and route clearance, compact complete reports, count grammar, literal multiline and Unicode text, accessible names, the full metadata snapshot, visible qualifications and local resource references. They render a 500-state, 999-arrow computed chart and exercise repeated and concurrent calls with no executables on PATH. Refusal cases include inconsistent structure and XML-inexpressible text in both drawn and metadata-only data. Factory acceptance checks compare the SVG's states, initials and labeled directed arrows with the unchanged published Mermaid:

```sh
go test -timeout 20m ./internal/statechart ./internal/factory
```

Reader acceptance and regression tests exercise total and typed mapping, complete concrete accounting, every hiding refusal, invariant signatures, temporal isolation, deterministic replay and concurrent evaluation, exact rule wording, detached snapshots, size boundaries and the advisory arrow target. Fake replay tests validate pins, generated TLA+ requests, complete identity/value joins, boolean evidence, cancellation and cleanup without changing the graph runner's fake contract. Value regressions cover intervals, reordered model-value sets, nested mappings and values TLC wraps over several lines, which the replay module joins onto one line so its junction lists still parse (#636), while refusing changed tuple order, types, string contents, interval endpoints and malformed values:

```sh
go test -timeout 20m ./internal/statechart ./internal/verify
```

Proposal tests exercise view repair, the independent final computation, fingerprint invalidation, persistence, recovery, amendments, ratification, literal rendering and above-capacity publication offline. The formalize integration test also checks the chart against pinned TLC:

```sh
go test -timeout 20m ./internal/formalize ./internal/factory ./cmd/invariant
go test -tags integration ./internal/formalize -run '^TestCheckRunsTheModelChecks$' -count=1 -timeout 5m
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

Proposal identity and amendments also rest on [D-0034](../decisions/journal/D-0034.jsonl) and [D-0045](../decisions/journal/D-0045.jsonl). [D-0345](../decisions/journal/D-0345.jsonl) records #603's proposal-view, artifact-binding and publication policy, including the nonblocking size limitation, refining [D-0343](../decisions/journal/D-0343.jsonl).

Standalone SVG implements #608's ratified plan on the graph and reader contracts in D-0335 and D-0343, under D-0105, D-0122 and D-0294. [D-0347](../decisions/journal/D-0347.jsonl) records the native-rendering decision, refining [D-0343](../decisions/journal/D-0343.jsonl).

The reachability layout implements #613's ratified plan on D-0347 and D-0343, with documentation maintained under D-0122 and D-0294. [D-0349](../decisions/journal/D-0349.jsonl) records the layout choice, refining [D-0347](../decisions/journal/D-0347.jsonl).

Verification receipts implement #615's ratified plan on D-0343, D-0345 and D-0347, under D-0013, D-0105, D-0122 and D-0294. [D-0351](../decisions/journal/D-0351.jsonl) records the receipt policy, refining [D-0343](../decisions/journal/D-0343.jsonl).
