---
id: D-0068
title: Slice 8 · code you can ship
date: 2026-09-27
door: one-way
status: ratified
ratified_by: "@gitdek"
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

A spike, not committed, wrote the log buffer's core this way: a ring buffer of any capacity, with lines written without limit and no constant from the bounds. **Gobra proved all of it, `New`, `Write` and `Ship`, with overflow checks, in about 24 seconds.** It's plain Go, with its contracts in comments, and it's in the appendix. What made it work is what the prompt will teach:

- **Spell out permissions field by field.** With `--overflow`, a pure function can't read an int field through a predicate's unfolding.
- **Keep index arithmetic linear.** A remainder by a symbolic length, `(head+i) % len(slots)`, kept the solver busy for over ten minutes. The code wraps an index with an `if`, and the spec wraps it with a ghost conditional, which Go itself doesn't have.
- **Say what the buffer holds through a ghost view,** `At(i)`, the i-th oldest line. Each operation's contract is then short and exact: `Ship` returns the old `At(0)`, and every `At(i)` becomes the old `At(i+1)`.
- **Avoid loops over slices where a ring will do.** Two earlier tries shifted the lines down on each ship, and neither verified. A loop that shifts in place needs loop invariants the solver can instantiate. Go's `copy` is specified only for slices that don't overlap.

**Then the whole design went through today's gate, unchanged.** A copy of `examples/03-log-buffer` kept its 13 ratified statements and its bounds, and swapped in two files: the proved ring buffer as its code, and an explorer that is the environment. The explorer keeps the bounds, the producers' writes and what was sent, and it calls the ring buffer for the buffer's part of each step.

- **At the ratified bounds** (`Capacity = 2`), `invariant verify` passed in about 25 seconds:
  - agreement: 87 states, depth 9, exactly TLC's
  - Gobra: 4 of 4 functions proved, with overflow checks, and the explorer's functions listed as unverified
- **At one size larger** (`Capacity = 3`, with the lock edited only for the experiment), the same core, byte for byte, agreed with TLC again: 111 states, depth 9.
- **The original bounded code at capacity 3 fails agreement:** it reaches 87 states where the model reaches 111. That's the check that catches a hardcoded size.

So for Go, agreement needs no change: the explorer can already carry the environment.

The main risk moves. Parametric proofs are feasible and fast, and the gate already checks the split. The work is teaching the agent these patterns, with the log buffer as the worked example, within its four gate runs.

## Options considered

- **A. The system in the code, the environment in the explorer (recommended).**
  - What it gets: shippable code, proofs at every size, and the same TLC check at the bounds.
  - What it costs: harder contracts, an explorer that does more, and a new style for every future Go project.
- **B. Parameters only.** Replace the size constants with parameters, and keep the model's history in the code. It's easier, and the proofs would hold at every size, but the code still carries `Log`, `Sent` and `Written`, so it isn't shippable.
- **C. Keep the bounded core, and generate a shippable wrapper around it.** The wrapper would be unproved, which defeats the point.

## Plan

1. **Build the worked example by hand first.** Rebuild `examples/03-log-buffer` this way, until Gobra proves all of it and agreement passes. It becomes the prompt's worked example.
2. **Rewrite the synthesis prompts** for Go, TypeScript and Python around the split: code that is the system, with sizes as parameters, and an explorer or driver that is the environment. Include the Gobra pitfalls.
3. **Agreement and conformance.** For Go, nothing changes: the demo above passed today's gate. TypeScript and Python drivers already keep the environment, so the slice checks they need nothing more.
4. **The gate** runs TLC and agreement at the ratified bounds and again at one size larger, without touching the lock. A receipt says "every size" only when both pass and the verifier proved the code.
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

## Ratified

@gitdek had the agent ratify this plan on the morning of 2026-09-27, with its recommendations:

1. **Shape A:** the system in the code, the environment in the explorer.
2. **One size larger,** not two.
3. **The first live issue is a new Go project, not the rate limiter.** This departs from the recommendation above. Acceptance 3 needs proofs at every size, and TypeScript has conformance but no proofs, so the rate limiter can't meet it. The rate limiter follows, for the TypeScript driver side.

## What would reopen this

If parametric proofs take the agent more than its four gate runs on most issues, then the prompt or the gate-run budget changes, or option B becomes the fallback for some languages.

## Appendix: the spike, which Gobra proves

```go
// +gobra

// A spike for slice 8: a ring buffer of any capacity, with O(1) writes and
// ships and no model bounds. The index arithmetic is linear: code wraps with
// an if, and the spec wraps with a ghost conditional.
package logbuffer

// MaxCapacity keeps index arithmetic far from overflow. It's the machine's
// limit, not the model's.
const MaxCapacity = 1 << 30

type Line struct {
	P int
	N int
}

// Buffer is a ring of slots holding N lines, the oldest at Head.
type Buffer struct {
	Slots []Line
	Head  int
	N     int
}

// @ requires acc(&b.Slots, _) && acc(&b.Head, _) && acc(&b.N, _)
// @ decreases
// @ pure
func (b *Buffer) Ok() bool {
	return 0 < len(b.Slots) && len(b.Slots) <= MaxCapacity &&
		0 <= b.Head && b.Head < len(b.Slots) && 0 <= b.N && b.N <= len(b.Slots)
}

// @ ghost
// @ requires 0 <= x && x < 2*n && 0 < n
// @ ensures 0 <= r && r < n
// @ decreases
// @ pure func wrap(x, n int) (r int) { return x < n ? x : x - n }

// @ ghost
// @ requires acc(&b.Slots, _) && acc(&b.Head, _) && acc(&b.N, _) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j], _)
// @ requires 0 <= i && i < b.N
// @ decreases
// @ pure func (b *Buffer) At(i int) Line { return b.Slots[wrap(b.Head+i, len(b.Slots))] }

// New makes an empty buffer of the given capacity.
// @ requires 0 < capacity && capacity <= MaxCapacity
// @ ensures acc(&b.Slots) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures len(b.Slots) == capacity && b.N == 0
func New(capacity int) (b *Buffer) {
	return &Buffer{Slots: make([]Line, capacity)}
}

// Write adds a line, newest, when there's room.
// @ requires acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ requires b.N < len(b.Slots)
// @ ensures acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures b.N == old(b.N) + 1 && b.At(old(b.N)) == l
// @ ensures forall i int :: { b.At(i) } 0 <= i && i < old(b.N) ==> b.At(i) == old(b.At(i))
func (b *Buffer) Write(l Line) {
	i := b.Head + b.N
	if i >= len(b.Slots) {
		i = i - len(b.Slots)
	}
	b.Slots[i] = l
	b.N = b.N + 1
}

// Ship takes the oldest line out, when there is one.
// @ requires acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ requires 0 < b.N
// @ ensures acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures l == old(b.At(0)) && b.N == old(b.N) - 1
// @ ensures forall i int :: { b.At(i) } 0 <= i && i < b.N ==> b.At(i) == old(b.At(i + 1))
func (b *Buffer) Ship() (l Line) {
	l = b.Slots[b.Head]
	b.Head = b.Head + 1
	if b.Head == len(b.Slots) {
		b.Head = 0
	}
	b.N = b.N - 1
	return l
}
```
