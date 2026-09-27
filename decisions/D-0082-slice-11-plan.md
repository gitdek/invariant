---
id: D-0082
title: Slice 11 · a driver can't skip a step
date: 2026-09-27
door: one-way
status: proposed
proposed_by: agent
source: slice 11 planning, the afternoon slice 10's plan was ratified, from D-0059 and the drivers in this repository
---

# D-0082 · Slice 11 · a driver can't skip a step

**Goal** ([PRD 3.11](../docs/PRD.md), [D-0059](log.md)). When a receipt says code was tested against its model, every step a person or worker could take was tried in every state the driver reached, and the only steps left untried are the ones the bounds rule out. The gate checks this itself, from the driver's output, instead of trusting the agent that wrote the driver.

## Why now

A driver runs the real code and records the states it goes through, and TLC checks that every step is one the model allows. That catches code that takes a step the model forbids, but only if the driver tries the step.

On copythis-ad#33, the factory's driver skipped retry whenever a stalled job's lease had already run out, because the model rules that step out there. That hid the very step the ratified rule was about, and the gate passed. A review of the driver caught it before merge. With the skip removed, the gate found a real bug in the lease code, fixed in copythis-ad#35 (D-0059).

Since then, the synthesis prompt forbids skipping a step because of the state the code is in. That's an instruction, and nothing checks it. Six projects here have a TypeScript or Python driver. Two of them are Python that Nagini also proves, and the other four rest on the driver alone: their receipts say "tested against the model". Now that the repository is public, people read those receipts as claims.

## What the gate would check

1. **Every attempt is recorded.** For each state it reaches, the driver records every step it tries: the operation, its arguments and what happened. The code either took the step, and the driver records the state it reached, or refused it, and the state stayed the same.
2. **Every step is tried, except where only a bound rules it out.** For every state the driver reached, the gate asks TLC which steps the model rules out at the ratified bounds but allows once the environment's bounds grow by one, such as another call when `made[a] = MaxCalls`. Those are the only steps the driver may leave untried, and the gate works them out itself, so a driver never names its own excuse. A step the model rules out at every size is a rule, such as retrying a lease that has run out, so the driver must try it and see the code refuse.
3. **A limit the code enforces is a rule.** Sizes the code takes as parameters, such as a capacity, aren't the environment's bounds. Refusing work past a capacity is something the code must do, so the driver tries it there too.
4. **What's checked today still holds.** Every step the code took is a Next step, a refusal is a step that changes nothing, and the code reaches exactly the model's states.

A driver that explores completely, as every synthesized driver does, can meet all four. A driver for existing code, such as copythis-ad's, samples runs at random, so it can't try every step in every state. Its runs choose from every operation, never only from those the state allows, and a second agent reviews it.

## Options considered

- **A. The gate checks attempts, and only a bound excuses a skip (recommended for drivers that explore completely).** It's evidence from tool output, like everything else in a receipt (3.1). Invariant ships a small exploration harness for TypeScript and Python that records attempts in the right shape, so a driver supplies only its operations and how to make and read the state. The harness makes following the rule easy, and the check makes breaking it visible.
- **B. A second agent reviews every driver (PRD 3.11 as proposed).** An agent with fresh context reads the driver for steps it skips and states it never records, and the factory posts the review with the pull request. It's cheap to build and catches more kinds of mistake, but it's an opinion, not evidence, and a receipt can't count it.
- **C. Both (recommended).** A for drivers that explore completely, and B for every driver: all of it for a driver that samples, and for one that explores, the sizes its manifest calls the code's parameters. Each driver gets the strongest check it can have.

## Plan

1. **The record.** Drivers write every attempt beside their runs. The manifest names the sizes the code takes as parameters, and every other bound is the environment's.
2. **The checks.** The gate asks TLC, in one run, which steps are ruled out in each reached state at the ratified bounds and allowed with the environment's bounds one larger. Then it checks, in Go, that every other step was tried.
3. **The harness.** A TypeScript and a Python module that explore breadth first, try every operation in every state, and write the record. The synthesis prompt asks for drivers built on it.
4. **The review.** A second agent run after synthesis, for every driver, posted with the pull request and counted in the issue's spend. For a driver that samples, it's the check of its steps. For one that explores, it checks the sizes the manifest calls parameters.
5. **The receipt.** A new row: every step tried in every state, with the number of attempts, and the steps left untried because only a bound ruled them out. A reviewed driver's receipt links the review and claims no more.
6. **Existing projects.** The factory rebuilds each driver through an amendment that changes no statement, as #28 did for the rate limiter's code. Until a project's driver is rebuilt, its receipt says its steps weren't checked.

## Acceptance

1. A driver that skips a step because of state fails the gate, and its receipt names the state and the step. The integration tests include one that skips the way copythis-ad#33's did.
2. A driver that leaves a step untried only where a bound rules it out passes. One that leaves it untried where the model rules it out at every size fails, and so does one that stops at a capacity the code should enforce.
3. Every TypeScript and Python project in this repository passes with a rebuilt driver, and its receipt shows every step tried.
4. copythis-ad's next pull request gets a second agent's review of its driver.

## Questions for @gitdek, with recommendations

1. **Is this slice 11?** The other candidate is the Codex backend (5.5, ratified in D-0052). Recommend this first. It closes a known gap in what receipts claim, and the repository is public now.
2. **A, B or C?** Recommend C.
3. **Existing drivers: rebuild them now, or when an issue next touches each project?** Recommend now, through the factory, one amendment per project, so the receipts stop claiming more than was checked.
4. **Go's explorers?** Gobra proves Go code against contracts the same agent writes, so the same gap exists in principle. Recommend checking them the same way once the harness has worked for TypeScript and Python, in this slice if it fits.
5. **Who says which sizes are the code's parameters?** The agent that writes the code, in the manifest. Calling a capacity an environment bound would let its driver stop at the limit, the same kind of skip as copythis-ad#33's. Recommend the second agent's review checks them against the code, as C has it, since a size the code takes is plain to see there.

## What would reopen this

- A proof path for TypeScript (3.10), which would make drivers matter less.
- Drivers generated from the model itself, with no agent writing them.
