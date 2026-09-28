# Bounded log shipping buffer

Written by Invariant for [#1](https://github.com/gitdek/invariant/issues/1): Add a bounded buffer for log shipping.

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/1#issuecomment-5837225662). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/LogBuffer.tla`](.invariant/specs/LogBuffer.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | The system starts in Init, and every step is a Next step. |
| `TypeOK` | invariant | The buffer, the shipped lines and the write history are lists of log lines, each producer has written a bounded number of lines, and the shipper is either retrying or not. |
| `BoundedCapacity` | invariant | The buffer never holds more lines than its capacity. |
| `NoLineLost` | invariant | Every line a producer has written is either still in the buffer or has been shipped. |
| `ShippedOnce` | invariant | No line is shipped more than once. |
| `ShippedInOrder` | invariant | The shipped lines are exactly the oldest written lines, in the order they were written. |
| `BufferInOrder` | invariant | The buffer holds exactly the written lines not yet shipped, oldest first. |
| `BufferFull` | witness | The buffer can fill up to its capacity. |
| `RetryPending` | witness | A send can fail and leave its line in the buffer to be retried. |
| `AllShipped` | witness | Every producer can finish writing and every line can be shipped. |
| `WriteOverCapacity` | bug | A producer adds a line to a buffer that is already full. |
| `DropOnFailure` | bug | When a send fails, the line is thrown away instead of retried. |
| `ResendDelivered` | bug | A send that actually went through is treated as failed and retried, so the line is shipped twice. |

Decided on the issue:

- When a producer writes a line and the buffer is already at capacity, what should happen? **A.** The producer waits until the shipper frees a slot, so no line is ever lost. (@gitdek)
- When the shipper fails to send a line, what should happen to that line? **A.** The line stays at the front of the buffer and the shipper retries it before sending any later line. (@gitdek)

## Code with no model bounds

This is slice 8's worked example ([D-0068](../../decisions/D-0068-slice-8-plan.md)). The model describes the buffer and its environment together. The code holds only the buffer, so it could ship.

- [`logbuffer.go`](logbuffer/logbuffer.go) is the system: a ring buffer of any capacity, that takes any number of lines. Gobra proves each operation against its contract at every capacity, with overflow checks. Nothing in it comes from the bounds.
- [`explore.go`](logbuffer/explore.go) is the environment: the producers, the lines each has written, and what the shipper has sent. It keeps the bounds, makes the buffer at the ratified capacity, and calls it for the buffer's part of each step. Agreement explores it the way TLC explores the model, and reaches exactly the model's 87 states.

Checked within `Capacity = 2`, `MaxLines = 2`, `Producers = {p1, p2}`. To run the gate yourself:

```bash
go run ./cmd/invariant verify examples/03-log-buffer
```
