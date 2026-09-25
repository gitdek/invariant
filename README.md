<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/logo-dark.svg">
    <img src="docs/brand/logo.svg" alt="Invariant" width="460">
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

## The gate

| Check | Tool | Fails when |
| :-- | :-- | :-- |
| Design | TLA+ · TLC | An invariant is violated, or the system can deadlock, within the stated bounds |
| Code | Gobra | A function doesn't verify against its contract |
| Integrity | Invariant | A pinned statement changed, or the bounds were weakened |
| Scope | Invariant | The diff adds dependencies, or edits CI config or pinned specs |
| Build | `go test` · `go vet` | Anything is red |

Every pull request carries a receipt that CI generates from tool output. It lists the states explored and the bounds they cover, the functions verified, the statement hashes, and the tool versions. The factory never writes its own receipt.

## Status

Pre-alpha. The design and the first decisions are in place. Code starts with the gate.

- [ ] **Slice 1 · The gate.** TLC, Gobra, and receipts on a hand-built [two-phase commit](https://en.wikipedia.org/wiki/Two-phase_commit_protocol).
- [ ] **Slice 2 · Synthesis.** The factory rebuilds that implementation from the ratified statements and passes the same gate.
- [ ] **Slice 3 · GitHub.** An issue becomes a decision request, then a ratification, then a pull request, then an auto-merge.

The slice plan is still a proposal: [D-0013](decisions/D-0013-slice-plan.md).

## How decisions get made here

This repository runs on its own rule: **a decision that isn't written down didn't happen.**

- [`decisions/log.md`](decisions/log.md) is the ordered record of what was decided, when, and by whom. Decisions that are hard to reverse also record the options considered, the reasoning, and what would reopen them.
- [`SPEC.md`](SPEC.md) is the current state, rolled up from ratified decisions only. If the spec says something the record doesn't support, the spec is wrong.

<br>

<p align="center">
  <sub>Built by <a href="https://github.com/gitdek">@gitdek</a> · <a href="https://puglisij.com">puglisij.com</a></sub>
</p>
