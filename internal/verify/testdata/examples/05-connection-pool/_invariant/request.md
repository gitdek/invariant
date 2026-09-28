# A connection pool that never hands out more than it has

Issue #18 in gitdek/invariant, opened by @gitdek.

Services share a pool of connections, such as to a database. A client asks the pool for a connection, uses it, and gives it back. The pool's size is chosen when it's made.

- The pool never has more connections out than its size.
- A client holds at most one connection at a time.
- Only a client that holds a connection can give one back, and giving it back frees exactly that one.
- When every connection is out, a client that asks is refused, and nothing changes.
- The pool can fill up, and every connection that's out can come back, until none is.

Check it with a pool of two and three clients. The code is the pool alone, and it must work at any size and with any number of clients. Nothing in it may depend on those bounds (D-0068).

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
