---
id: D-0010
title: Role of Lean 4
date: 2026-09-25
door: one-way
status: ratified
ratified_by: "@gitdek"
source: kickoff design review (Claude Code session 2f96fee2)
---

# D-0010 · Role of Lean 4

**Decision.** Drop Lean 4 for v1. TLA+ and TLC check designs, and Gobra checks code. The first option below.

**The fork.** The brief names Lean 4 in its title and its pitch. After [D-0003](D-0003-go-with-gobra.md), Lean has no job in code-level proofs: Gobra verifies the Go directly, and TLC checks the design.

**Options**

- **Drop Lean for v1.** The stack becomes TLA+ and TLC for designs, and Gobra for code. The pitch and the portfolio copy change to match. *Claude recommends this option because it's the only one that doesn't add a second model of the same system.*
- **Use Lean to prove the protocol at every size.** TLC only checks small configurations, such as three resource managers. Lean would prove the invariant for any number of them. That needs a Lean model of the protocol next to the TLA+ one, and nothing mechanically ties the two models together.
- **Use TLAPS instead of Lean for proofs at every size.** The TLA+ Proof System proves the same spec TLC checks, so there's no second model. It's less well known than Lean.

**Blocks.** The pitch and the portfolio copy ([D-0008](log.md)). Nothing in slice 1 depends on it.
