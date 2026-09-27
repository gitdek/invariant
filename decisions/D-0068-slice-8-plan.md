---
id: D-0068
title: Slice 8 · code you can ship
date: 2026-09-27
door: one-way
status: proposed
proposed_by: agent
source: slice 8 planning, overnight after slice 7, with a Gobra spike
---

# D-0068 · Slice 8 · code you can ship

**Goal** ([PRD 3.7](../docs/PRD.md), [D-0048](log.md), [D-0058](log.md)). A factory project's code carries no model bounds, so its proofs hold at every size, and TLC still checks the design at the ratified bounds. The slice is done when a factory project's code has no model bounds in it, its proofs don't depend on the bounds, and TLC checks it at the ratified bounds, on a live issue.

## What's wrong today

Two kinds of model artifact end up in the code.

1. **Sizes are constants.** The log buffer's Go core declares `Capacity = 2`, and its `Ship` moves lines with `t.Buf[0] = s.Buf[1]`, which is right only at capacity 2. The rate limiter's TypeScript stops at `MAX_CALLS = 5`, and its clock at `MAX_TIME = 3`.
2. **The model's environment and history live in the code.** The log buffer's state holds `Log`, `Sent` and `Written`: what the producers wrote, and what was shipped. `MaxLines` caps how many lines a producer can ever write. Those variables are how the model stays finite and states its rules. They aren't the buffer's state.

The Go prompt asks for this on purpose: "a comparable `State` struct that represents the spec's variables for the bounds above, one to one", in "fixed-size arrays". That made agreement a direct comparison and Gobra's job easy. The cost was code nobody would ship.

## The design: the system in the code, the environment in the explorer

A model describes a system and the environment it runs in. The code should hold only the system.

1. **The code is the system,** with sizes as parameters: `New(capacity int)`, a slice sized when it's made, and operations that refuse when the buffer is full. No constant from the bounds appears in it, and it keeps only the state the system itself needs.
2. **The explorer (Go) or the driver (TypeScript and Python) is the environment.** It makes the code at the ratified bounds and keeps the model's environment and history variables, such as who wrote what and what was shipped. It takes only the environment's steps that the bounds allow, and maps the code's state and its own onto the spec's variables. Agreement, or the exhaustive conformance check, then compares state for state with TLC at the bounds, as it does today.
3. **Contracts are parametric.** Gobra and Nagini prove each operation at every size, over slices with quantified permissions in Go and over lists in Python. A contract restates its action for the system's part. The rules about history are the ones TLC checks.
4. **The gate checks that the bounds aren't in the code.** Agreement runs twice: once at the ratified bounds, and once at one size larger, with TLC given the larger constants, when that model stays under the state cap. Code that hardcodes a size fails the second run.
5. **Receipts say exactly what's claimed.** Each operation is proved against its contract at every size, and the design's rules are checked by TLC at the ratified bounds and one size larger. Nothing claims more (D-0015).

## What a spike found

A spike, not committed, wrote the log buffer's core this way: a buffer of any capacity, lines written without limit, and contracts over quantified slice permissions. It's excerpted below.

- **`New` and `Write` verified** at every capacity, with overflow checks, in about 20 seconds.
- **Two Gobra pitfalls,** which the prompt will warn about:
  - With `--overflow`, a pure function can't read an int field through a predicate's unfolding, so permissions are spelled out field by field.
  - A remainder by a symbolic length, `(head+i) % len(slots)`, kept the solver busy for over ten minutes, so index arithmetic stays linear.
- **`Ship` didn't verify yet.** It shifts the lines down in a loop, and its loop invariants need to be ones the solver can instantiate. This is the slice's main risk: parametric proofs take more engineering than fixed-size ones. The agent will need a worked example and its four gate runs.

## Options considered

- **A. The system in the code, the environment in the explorer (recommended).**
  - What it gets: shippable code, proofs at every size, and the same TLC check at the bounds.
  - What it costs: harder contracts, an explorer that does more, and a new style for every future Go project.
- **B. Parameters only.** Replace the size constants with parameters, and keep the model's history in the code. It's easier, and the proofs would hold at every size, but the code still carries `Log`, `Sent` and `Written`, so it isn't shippable.
- **C. Keep the bounded core, and generate a shippable wrapper around it.** The wrapper would be unproved, which defeats the point.

## Plan

1. **Build the worked example by hand first.** Rebuild `examples/03-log-buffer` this way, until Gobra proves all of it and agreement passes. It becomes the prompt's worked example.
2. **Rewrite the synthesis prompts** for Go, TypeScript and Python around the split: code that is the system, with sizes as parameters, and an explorer or driver that is the environment. Include the Gobra pitfalls.
3. **Agreement and conformance** accept an explorer or driver that keeps environment and history variables, mapped onto the spec's.
4. **The gate** runs agreement at the bounds and at one size larger, and a receipt says "every size" only when that passes and the verifier proved the code.
5. **Live:** an issue amends a factory project so its code has no bounds, through the factory.

Existing projects keep their receipts, and nobody rewrites them unless an issue asks.

## Acceptance

1. `examples/03-log-buffer`'s core has no model bounds, Gobra proves it at every capacity, and agreement passes at the ratified bounds and at one size larger.
2. The gate fails a project whose code hardcodes a size. A test plants one.
3. Live, on an issue: a factory project's code loses its bounds through the factory, with proofs at every size, and the bot merges it once CI's gate passes.

## Questions for @gitdek, with recommendations

1. **The shape: A, B or C?** Recommend A.
2. **One size larger, or two?** Recommend one. It catches a hardcoded size and keeps TLC fast.
3. **Which project goes live first?** Recommend the rate limiter, [#3](https://github.com/gitdek/invariant/issues/3) and [#5](https://github.com/gitdek/invariant/issues/5). Its bounds are the most visible, `MAX_CALLS = 5` and a clock that stops at 3, and it's TypeScript, so the live run also tests the driver side. The log buffer stays the Go worked example.

## What would reopen this

If parametric proofs take the agent more than its four gate runs on most issues, then the prompt or the gate-run budget changes, or option B becomes the fallback for some languages.

## Appendix: the spike's `Write`

```go
// Buffer holds its lines oldest first in Lines[0:N], in Cap = len(Lines) slots.
type Buffer struct {
	Lines []Line
	N     int
}

// Write adds a line, newest, when there's room.
// @ requires acc(&b.Lines, 1/2) && acc(&b.N) && b.Ok()
// @ requires forall j int :: { &b.Lines[j] } 0 <= j && j < len(b.Lines) ==> acc(&b.Lines[j])
// @ requires b.N < len(b.Lines)
// @ ensures acc(&b.Lines, 1/2) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Lines[j] } 0 <= j && j < len(b.Lines) ==> acc(&b.Lines[j])
// @ ensures b.N == old(b.N) + 1 && b.Lines[old(b.N)] == l
// @ ensures forall j int :: { b.Lines[j] } 0 <= j && j < old(b.N) ==> b.Lines[j] == old(b.Lines[j])
func (b *Buffer) Write(l Line) {
	b.Lines[b.N] = l
	b.N = b.N + 1
}
```
