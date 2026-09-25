# Bounded log shipping buffer, in TypeScript

The statements @gitdek ratified on [#1](https://github.com/gitdek/invariant/issues/1), with the code written in TypeScript. The lock is the same one the [Go version](../03-log-buffer) carries, and its proposal hash is the same. Only the language changed, and the language isn't part of what was ratified.

Invariant's synthesis wrote everything here from the ratified statements and the request alone, and passed on its first gate run: 8 turns, 44 seconds.

- [`src/machine.ts`](src/machine.ts) is the model as a state machine: `init()`, one function per TLA+ action, returning the next state or `null` when the action isn't enabled, and `successors()`.
- [`conformance.ts`](conformance.ts) explores the state machine completely, breadth first, and records a run for every step it can take. TLC checks every run.
- [`src/machine.test.ts`](src/machine.test.ts) runs with `node --test`.

TypeScript has no proof path yet, so the receipt says **tested against the model**. The manifest marks the driver as exhaustive, so the gate also requires it to visit every state the model reaches. It visits all 87, and every step it takes is one the model allows. So the code and the model reach exactly the same states (D-0038).

The sandbox is `node:24-alpine`, with Node's standard library only and no network. The `package.json` has no dependencies, and the scope check would reject a pull request that added one.

```bash
go run ./cmd/invariant verify examples/03-log-buffer-ts
```
