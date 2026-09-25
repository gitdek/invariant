<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/logo-animated-dark.svg">
    <img src="docs/brand/logo-animated.svg" alt="Invariant: a state orbiting a fixed point that never moves" width="480">
  </picture>
</p>

<p align="center">
  <strong>A code factory that doesn't guess.</strong><br>
  You ratify what must be true. Invariant proves every pull request against it before merging.
</p>

<p align="center">
  <img alt="Status: pre-alpha" src="https://img.shields.io/badge/status-pre--alpha-6e7781?style=flat-square">
  <img alt="Go 1.27" src="https://img.shields.io/badge/Go-1.27-00ADD8?style=flat-square&logo=go&logoColor=white">
  <img alt="Design: TLA+ / TLC" src="https://img.shields.io/badge/design-TLA%2B%20%2F%20TLC-0CA678?style=flat-square">
  <img alt="Code: Gobra" src="https://img.shields.io/badge/code-Gobra-0CA678?style=flat-square">
  <img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-6e7781?style=flat-square">
</p>

<br>

AI coding agents don't ask clarifying questions. They pick an interpretation and ship it with total confidence. Formal verification alone doesn't fix that. A proof only shows the code matches the property someone wrote down, and if the agent wrote that property, the proof just certifies the agent's guess.

Invariant splits the work where it belongs:

| People decide | Invariant proves |
| :-- | :-- |
| What must always be true: invariants, safety properties, and the bounds they're checked at | That the code satisfies them, using a model checker for the design and a verifier for the code |
| Every fork the factory can't resolve on its own | Only what it was asked to prove. Ratified statements are pinned, and the factory can't edit them |

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/how-it-works-dark.svg">
  <img src="docs/assets/how-it-works.svg" alt="Issue, then Formalize, then People ratify, then Synthesize, then Gate, then Merge. Counterexamples from the gate loop back to synthesis." width="100%">
</picture>

1. **Formalize.** Invariant drafts the properties an issue implies. Writing a formal spec forces every ambiguity into the open. You can't model a buffer, for example, without deciding what happens when it's full.
2. **Ask.** When Invariant hits a fork it can't resolve, it posts the question back on the issue as a decision request. It doesn't pick an answer for you.
3. **Ratify.** A person approves each statement. The approval is recorded along with where the statement came from, and the statement is pinned by hash.
4. **Synthesize.** Invariant writes the Go implementation and the TLA+ model. It keeps repairing them against counterexamples until the gate passes.
5. **Gate and merge.** CI re-runs every check on the committed artifacts. The pull request merges only if every check passes.

## Watch it catch a bug

<p align="center">
  <img src="docs/assets/counterexample.svg" alt="Replay of a real TLC counterexample. r1 prepares and the coordinator records its vote; r2 aborts on its own; the buggy coordinator commits anyway, on r1's vote alone; r1 commits. TCConsistent is violated in 5 steps." width="100%">
</p>

Passing TLC only means something if the invariants could have failed. So the gate plants known bugs in the model and requires TLC to catch each one. This is the real counterexample for the two-phase commit example's planted bug: a coordinator that commits after hearing from one resource manager instead of all three. TLC finds the shortest path to an inconsistent commit. In five steps, r2 aborts on its own, the coordinator commits anyway on r1's vote alone, and r1 commits.

## One action, one contract

<p align="center">
  <img src="docs/assets/model-and-code.svg" alt="The TLA+ action RMPrepare beside the Go function RMPrepare. Its Gobra requires clauses restate the action's enabling condition, and its ensures clauses restate the effect and everything left unchanged." width="100%">
</p>

The factory owns both the model and the code, and each Go function's contract restates exactly one TLA+ action. `requires` is the action's enabling condition. `ensures` is its effect, including everything the action leaves unchanged. TLC checks the model, Gobra checks the code, and a test confirms that both reach the same 288 states.

## The gate

| Check | Tool | Fails when |
| :-- | :-- | :-- |
| Pins | Invariant | A ratified statement's text, or anything it depends on, has changed |
| Design | TLA+ · TLC | An invariant is violated, or the system can deadlock, within the ratified bounds |
| Reachability | TLA+ · TLC | A ratified witness can't be reached, so the invariants might hold only because nothing happens |
| Known bugs | TLA+ · TLC | A ratified bug, added to the model as an extra action, slips past the invariants |
| Agreement | Invariant | The code, explored from its initial state, doesn't reach exactly the states TLC found in the model |
| Code | Gobra | A function doesn't verify against its contract, or an index or integer operation could fail |
| Build | `go test` · `go vet` | Anything is red. Runs in a sandbox with no network and no secrets. |
| Scope | Invariant | The diff adds dependencies, uses cgo, or edits CI config or pinned specs. This check arrives with the GitHub integration. |

Every pull request carries a receipt that CI generates from tool output. It lists the states explored and the bounds they cover, the functions verified, the statement hashes, and the tool versions. The factory never writes its own receipt.

## Try it

With Go and Docker installed, run:

```bash
go run ./cmd/invariant verify examples/02-twophase-commit
```

It prints a receipt. This is the real one for the [two-phase commit example](examples/02-twophase-commit):

<p align="center">
  <img src="docs/assets/receipt.svg" alt="Invariant receipt for two-phase commit. Every check passed: 4 of 4 pins match; TLC found no violations or deadlocks in 288 distinct states; 2 of 2 witnesses were reached; 1 of 1 known bugs was caught; Gobra verified 10 of 10 functions; go vet and go test passed." width="100%">
</p>

<details>
<summary>The receipt as text</summary>

| Check | Result | Evidence |
| :-- | :-- | :-- |
| Pinned statements | ✅ 6 of 6 match | recorded in D-0027 |
| Design · TLC | ✅ no violations, no deadlock | 288 distinct states (1,146 generated), depth 11 |
| Reachability | ✅ 2 of 2 witnesses reached | `AllCommitted` in 10 steps, `AllAborted` in 3 steps |
| Known bugs | ✅ 1 of 1 caught | early-commit: `TCConsistent` violated after 5 steps |
| Agreement | ✅ code reaches the model's states | 288 states, depth 11 |
| Code · Gobra | ✅ 10 of 10 functions verified | 10 with contracts, overflow checked, not verified: Successors |
| Build | ✅ go vet, go test | sandboxed, no network |

These results cover `RM = {r1, r2, r3}`. Within those bounds, TLC's search is exhaustive. The receipt claims nothing beyond them.

</details>

## Status

Pre-alpha. The gate works end to end, and the factory can build a verified implementation from ratified statements alone. TypeScript and Python come next.

- [x] **Slice 1 · The gate.** TLC, Gobra, and receipts on a hand-built [two-phase commit](examples/02-twophase-commit).
- [x] **Slice 2 · Synthesis.** Headless Claude Code rebuilt the model and the code from the ratified statements and the request alone. It [passed on its first gate run](examples/02-twophase-commit-synthesized), in 12 turns and under two minutes.
- [ ] **Slice 3 · TypeScript and Python.** A conformance check for existing code, then a spike on Nagini proofs for Python.
- [ ] **Slice 4 · GitHub.** An issue becomes a decision request, then a ratification, then a pull request, then an auto-merge.

The slice plan and slice 1's acceptance criteria are in [D-0013](decisions/D-0013-slice-plan.md).

## How decisions get made here

This repository runs on its own rule: **a decision that isn't written down didn't happen.**

- [`decisions/log.md`](decisions/log.md) is the ordered record of what was decided, when, and by whom. Decisions that are hard to reverse also record the options considered, the reasoning, and what would reopen them.
- [`SPEC.md`](SPEC.md) is the current state, rolled up from the record. If the spec says something the record doesn't support, the spec is wrong.

<br>

<p align="center">
  <sub>Built by <a href="https://github.com/gitdek">@gitdek</a> · <a href="https://puglisij.com">puglisij.com</a><br>
  Every graphic above is generated from real <code>invariant verify</code> output by <a href="docs/assets/generate.py"><code>docs/assets/generate.py</code></a>.</sub>
</p>
