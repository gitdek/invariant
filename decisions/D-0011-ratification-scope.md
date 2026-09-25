---
id: D-0011
title: What gets ratified, and what ties the Go to the TLA+ model
date: 2026-09-25
door: one-way
status: ratified
ratified_by: "@gitdek"
source: kickoff design review (Claude Code session 2f96fee2)
---

# D-0011 · What gets ratified, and what ties the Go to the TLA+ model

**Decision.** People ratify the invariants, the checking bounds and the reachability witnesses. The factory owns the rest. The first option below.

**The fork.** [D-0002](D-0002-people-ratify-statements.md) says people ratify the statements that code is proved against. With TLA+ and Gobra there are three layers that could count as statements:

- the TLA+ invariants
- the TLA+ model itself (`Init` and `Next`)
- the Gobra contracts on the Go functions

Pin too little, and the factory can satisfy the invariants with a model that doesn't match the code. Pin too much, and ratifying stops being a two-minute job.

**Options**

- **Ratify the invariants, the checking bounds, and the reachability witnesses.** The factory owns the TLA+ model, the Go, and the Gobra contracts. Each Go step function's contract must mirror one TLA+ action. In slice 1 that match is checked by review; later, the contracts are generated from the TLA+ actions. Trace validation checks the running code against the model. *Claude recommends this option. People ratify what they can judge ("no resource manager commits while another aborts"), and tools rather than people check that the code matches the model.*
- **Also ratify the Gobra contracts.** Tighter, but it asks people to approve specifications written in terms of memory permissions.
- **Ratify the whole TLA+ spec.** People approve the design itself, not only its properties. The factory writes only the Go and the contracts.

**Blocks.** Criterion 5 of the slice 1 plan ([D-0013](D-0013-slice-plan.md)).
