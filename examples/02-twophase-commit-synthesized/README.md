# Example: two-phase commit, synthesized

The factory wrote this project. It started from the [hand-built example](../02-twophase-commit)'s ratified statements and request alone: a skeleton of pinned definitions, with the model and the Go package removed. Then it had to pass the same gate.

| | |
| :-- | :-- |
| Agent | claude-code (headless), model `claude-opus-5-5` |
| Turns | 12 |
| Estimated cost | $0.36, by the agent's own estimate. On a Claude plan this draws on the plan's usage rather than being billed. |
| Time | 115 seconds, including Invariant's final gate |
| Protected files edited | none |

| Gate run | Result |
| :-- | :-- |
| 1 | ✅ passed |

Invariant's final gate on the assembled result passed:
- TLC explored 288 states.
- The Go code reaches the same 288.
- Gobra verified 8 functions.
- The pins are identical to the hand-built example's.

What the factory wrote:
- the model section of [`TwoPhase.tla`](.invariant/specs/TwoPhase.tla)
- [`twophase.go`](twophase/twophase.go)
- [`explore.go`](twophase/explore.go)
- [`twophase_test.go`](twophase/twophase_test.go)

Everything else came from the ratified project unchanged.

Two-phase commit is a textbook protocol, so passing on the first try is easier here than it would be for a less familiar system.

To reproduce it, run this from the repository root with Docker running. It uses your Claude account.

```bash
go run ./cmd/invariant synthesize examples/02-twophase-commit
```
