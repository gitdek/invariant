# Example: two-phase commit

In two-phase commit, a transaction manager commits only after every resource manager has prepared. Until it prepares, any resource manager may abort. This is slice 1's proving ground: a hand-built project that the gate checks end to end.

## What was ratified

The statements live in [`.invariant/ratified.lock`](.invariant/ratified.lock). Each one is pinned by hash to its text in [`TwoPhase.tla`](.invariant/specs/TwoPhase.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `TypeOK` | invariant | Every variable always holds a value of the expected type. |
| `TCConsistent` | invariant | No resource manager commits while another aborts. |
| `AllCommitted` | witness | Every resource manager can end up committed. |
| `AllAborted` | witness | Every resource manager can end up aborted. |

TLC checks them with three resource managers, `RM = {r1, r2, r3}`. The lock file also records one known bug the invariants must catch. In **early commit**, the coordinator commits once any resource manager has prepared, instead of waiting for all of them.

## What the gate checks

1. All four statements still match their pins.
2. TLC explores every reachable state of the model, 288 in all. `TypeOK` and `TCConsistent` hold in every one, and none of them deadlocks.
3. Both witnesses are reachable, which shows the invariants aren't holding only because the model does nothing.
4. The early-commit bug makes TLC report a `TCConsistent` violation in five steps:
   - r1 prepares.
   - The coordinator commits on r1's vote alone.
   - r1 commits.
   - r2 aborts.
5. Gobra verifies all ten functions in [`twophase`](twophase/twophase.go). Each step function's contract restates one TLA+ action. Gobra also checks that every array index is in range and that no integer operation overflows.
6. `go vet` and `go test` pass. The tests walk the Go state machine and reach exactly the 288 states TLC reports.

## Run it

Run these from the repository root, with Docker running:

```bash
go run ./cmd/invariant verify -out out examples/02-twophase-commit
```

```bash
go run ./cmd/invariant trace out/traces/early-commit.json
```
