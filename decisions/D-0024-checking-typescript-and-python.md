---
id: D-0024
title: How TypeScript and Python code gets checked
date: 2026-09-25
door: one-way
status: proposed
proposed_by: Claude
source: follow-up to D-0023 (Claude Code session 2f96fee2)
---

# D-0024 · How TypeScript and Python code gets checked (proposed)

**Context.** [D-0023](D-0023-more-languages.md) commits Invariant to Go, TypeScript and Python, because those are the languages of @gitdek's projects. Most code in those projects already exists and was never written for a verifier. Proofs suit new, self-contained cores, like the two-phase commit state machine. Conformance testing, which checks running code against the model, suits everything else.

**Proposal**

- **Every language gets both paths**, and the receipt names which one ran. A receipt's code row says "proved" or "tested against the model", and never blurs the two.
- **Python.**
  - Proof: Nagini, for new cores written in its typed subset.
  - Conformance: Hypothesis stateful tests generated from the TLA+ actions. Each action becomes a rule whose precondition is the action's enabling condition. Trace validation also checks recorded runs against the spec.
- **TypeScript.**
  - Proof: Dafny compiled to JavaScript, for new cores.
  - Conformance: fast-check model-based tests generated from the TLA+ actions, plus trace validation.
- **Go** keeps Gobra for proofs ([D-0003](D-0003-go-with-gobra.md)), and gets the same conformance path for existing code.
- **Order.** This amends [D-0013](D-0013-slice-plan.md):
  1. Slice 2: synthesis for Go.
  2. Slice 3: conformance for TypeScript and Python, then a Nagini spike.
  3. Slice 4: GitHub.

  Conformance comes first because it covers the existing code where most work happens.

**Why.** A proof covers only code written for its verifier, and most project code isn't. Conformance testing still ties that code to ratified statements, and the gate's other checks still apply to the model: pins, TLC, witnesses and known bugs.

**What would reopen it.** Nagini or Dafny can't handle the cores the projects need, or conformance tests prove too weak in practice to catch real bugs.
