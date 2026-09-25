---
id: D-0013
title: Slice plan, and slice 1 acceptance criteria
date: 2026-09-25
door: two-way
status: proposed
proposed_by: Claude
source: kickoff design review (Claude Code session 2f96fee2)
---

# D-0013 · Slice plan, and slice 1 acceptance criteria (proposed)

**Proposal.** Build in three slices:

1. **The gate**, proven on a hand-built two-phase commit. CLI only: no model APIs, no GitHub.
2. **Synthesis.** The factory rebuilds the two-phase commit implementation from the ratified statements and passes the same gate.
3. **GitHub.** Issue, then decision request, then ratification, then a pull request with its receipt, then auto-merge.

## Slice 1 is done when

1. `invariant verify examples/02-twophase-commit` runs TLC on the committed spec and config. It exits non-zero on any invariant violation, deadlock or TLC error, and prints the checked bounds (the number of resource managers).
2. The ratified statements are pinned by hash: `TCConsistent` (no resource manager commits while another aborts) and `TypeOK`. Changing either one fails verification.
3. The invariants are shown to be non-vacuous. TLC confirms that a state where every resource manager has committed is reachable, and so is a state where every one has aborted.
4. A shipped mutant, in which the coordinator commits before every resource manager is prepared, makes TLC report a `TCConsistent` violation. The counterexample is saved as a JSON trace.
5. Each coordinator and resource-manager step is a Go function whose Gobra contract mirrors the matching TLA+ action (subject to [D-0011](D-0011-ratification-scope.md)), and Gobra verifies all of them.
6. `go test ./...` and `go vet ./...` pass.
7. The receipt, in Markdown and JSON, is generated only from tool output. It includes states explored, search depth, bounds, functions verified, statement hashes and tool versions.
8. TLC and Gobra run from Docker images pinned by digest, so a local run and a CI run produce the same receipt.

**Why.** [D-0004](log.md) turns on auto-merge from the first factory PR, so the gate has to exist, and be trusted, before the factory does. A hand-built first example gives the gate known-good and known-bad inputs before any model writes code against it. The trace from criterion 4 is also the data for the Trace Explorer (D-0008, D-0015).

**What would reopen it.** Gobra can't verify the step functions ([D-0003](D-0003-go-with-gobra.md)), or the owner wants synthesis to come earlier.
