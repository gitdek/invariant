---
id: D-0026
title: Slice 2 plan and acceptance criteria
date: 2026-09-25
door: two-way
status: decided
decided_by: Claude
source: slice 2 build (Claude Code session 2f96fee2), carrying out D-0013 and D-0024
---

# D-0026 · Slice 2 plan and acceptance criteria

**Goal** (from [D-0013](D-0013-slice-plan.md)). The factory rebuilds the two-phase commit implementation from the ratified statements alone, and passes the same gate.

## The gate, hardened for a factory that writes the model

Once the factory writes the TLA+ model as well as the code, the slice 1 gate has gaps. These close them:

1. **Pins cover dependencies.** A pin hashes a statement and every definition it depends on, up to the factory's `Init` and `Next`. `TypeOK` can't be weakened by redefining `Messages`.
2. **The spec is pinned.** `Spec == Init /\ [][Next]_vars` is a ratified statement, so the factory's model has to be `Init` and `Next`.
3. **Known bugs are actions.** A text-replacement mutant stops applying as soon as the model is written differently. A bug is now a pinned TLA+ action. The gate checks `Init /\ [][Next \/ Bug]_vars` and requires the expected violation.
4. **Agreement.** The Go package exports `Init()` and `Successors()`. The gate explores them breadth first and requires exactly TLC's distinct-state count and depth. That ties the code to the model mechanically, instead of by review (the ratified choice in [D-0011](D-0011-ratification-scope.md)).
5. **Sandboxes.** TLC and Gobra run with no network. Builds, tests and exploration run in `golang:1.27-alpine`, pinned by digest, on a throwaway copy, with no network and none of the host's environment ([D-0014](log.md)).
6. **Gobra's scope is explicit.** Gobra verifies the files marked `// +gobra`. Gobra can't verify Go's `append`, so the exploration code lives in plain Go, and the receipt names it as unverified.

## Slice 2 is done when

1. `invariant synthesize examples/02-twophase-commit` builds a skeleton that holds only the pinned definitions. The model and the Go package are gone.
2. A coding agent receives the skeleton, the request and the ratified statements. It works in a scratch directory outside the repository, with file tools and the gate as its only tools: no shell, no web.
3. Every gate run checks a project assembled from the original lock, manifest, request and `go.mod`, plus the agent's model and code. Nothing the agent does to protected files reaches the gate, and any such edit is reported.
4. The agent gets at most four gate runs: one attempt and three repairs ([D-0000](sources/2026-09-25-kickoff-brief.md)). Each run is capped by an estimated-cost budget, a turn limit and a wall-clock timeout.
5. After the agent stops, Invariant runs the gate itself and writes the receipt. The synthesis passes only if that final gate does.
6. The synthesis log records every gate run's outcome, the agent's turns and its estimated cost.
7. Integration tests show the hardened gate rejects a weakened invariant, a changed dependency, a vacuous model, a model too narrow to show a known bug's damage, and code that disagrees with the model.
