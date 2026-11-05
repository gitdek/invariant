# Bounded graph data

Invariant's `internal/tlc` package exposes the complete graph dumped by a bounded TLC search, before the dashboard compacts it. This is the graph foundation delivered by [#575's ratified plan](../.invariant/plans/issue-575.json). Proposal rendering is not implemented.

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

Completion requires balanced tool-message frames, version and initial-generation records, success, final statistics, search depth and finish records in order. Errors override an earlier success message. The final queue must be empty; distinct states must match `StateCount`, and distinct initial states must match `Graph.Initial`. Total generated states must equal initial states generated plus `StepCount`. Initial generation can include duplicates, so it is counted separately from distinct initial states. Generated states are never reported as the graph's step count. Numeric overflow, malformed or missing records, tool failures and cancellation return an error and no graph.

Both dump methods create a fresh temporary directory under the supplied model directory for configuration and output, and remove it on return. They consume only their own regular output file, refusing absent output or a symlink. Caller files such as `Invariant.cfg` and `states.dot` stay intact. `DumpGraph` also copies and hashes the jar there before running it. The caller still supplies the checksum-verified toolchain and digest-pinned Java image; the API records the actual jar hash rather than assuming the expected pin. The configuration snapshot has independent maps and slices.

Graph runs share the runner's TLC admission budget and container and Java heap limits. Canceled admission waits launch no container; running cancellation retains the caller's cause and joins Docker cleanup before releasing capacity. A canceled run never supplies graph evidence. The supplied context bounds the dump's lifetime.

These results describe the explored model within the supplied bounds, with TLC's fingerprint identities and guarantees. They are not an unbounded proof. The existing `Runner.Dump` returns raw DOT without claiming the completion guarantee. The dashboard continues to parse and compact its own display graph and match traces as before; its compaction does not alter this reusable graph.

## Tests

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

This work rests on [D-0016](../decisions/journal/D-0016.jsonl), [D-0049](../decisions/journal/D-0049.jsonl), [D-0105](../decisions/journal/D-0105.jsonl), [D-0122](../decisions/journal/D-0122.jsonl) and [D-0294](../decisions/journal/D-0294.jsonl). The merger must record the new graph-evidence policy as a decision; this plumbing build cannot write `decisions/`.
