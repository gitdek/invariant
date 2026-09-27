# Bounded connection pool

Written by Invariant for [#18](https://github.com/gitdek/invariant/issues/18): A connection pool that never hands out more than it has.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/18#issuecomment-5855556473). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/ConnectionPool.tla`](.invariant/specs/ConnectionPool.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The pool starts with no connections out, and every step is a client taking a free connection, being refused when none is free, or giving back one it holds. |
| `NeverOverSize` | invariant | The pool never has more connections out than its size. |
| `AtMostOnePerClient` | invariant | A client never holds more than one connection at a time. |
| `NoSharedConnection` | invariant | No connection is ever held by two clients at once. |
| `TypeOK` | invariant | Each client always holds some set of the pool's connections. |
| `PoolFull` | witness | The pool can reach a state where every connection is out. |
| `SomeoneHolds` | witness | A client can get a connection. |
| `AcquireWhenFull` | bug | When every connection is out, the pool hands a client one that is already out. |
| `AcquireTwice` | bug | A client that already holds a connection is given a second one. |

Checked within `Clients = {c1, c2, c3}`, `Size = 2`. To run the gate yourself:

```bash
go run ./cmd/invariant verify examples/05-connection-pool
```
