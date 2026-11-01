# Example: two-phase commit in TypeScript

The ratified two-phase commit from [the Go example](../02-twophase-commit), implemented as ordinary TypeScript. [`src/transaction.ts`](src/transaction.ts) is a small `Transaction` library, written the way you'd write it in any codebase. Nothing in it was written for a verifier.

It isn't proved. It's **tested against the model**:

1. The driver, [`conformance.ts`](conformance.ts), explores the library with Invariant's harness. In every state it reaches, it tries every step the model's `Next` names, for every resource manager: `prepare`, give up, record a vote, commit, abort and learn, and lets the library refuse what the model rules out. It records each attempt in the spec's vocabulary. That mapping is the driver's `abstract` function.
2. TLC checks the recorded runs against the ratified model. Every run must start in a state `Init` allows. Every step that changes the state must be a `Next` step. A refused operation changes nothing, which is always allowed.
3. The driver reaches all 288 of the model's states and tries all 4,896 steps there, so the code and the model reach exactly the same states.

The statements, their pins and the design checks are the same as the Go example's. The sandbox is `node:24-alpine`, which runs `.ts` files directly, with no network. The build step is `node --test`.

Plant the early-commit bug, a coordinator that commits after a single vote, and the gate fails at that exact step. The integration tests do exactly this.

```bash
go run ./cmd/invariant verify examples/02-twophase-commit-ts
```
