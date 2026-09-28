# Add a bounded buffer for log shipping

Issue #1 in gitdek/invariant, opened by @gitdek.

Producers write log lines into a buffer with a fixed capacity. A shipper takes lines out, oldest first, and sends them on.

- Several producers can write to the buffer.
- The shipper sends lines in the order they went in, and sends each line once.
- The buffer never holds more lines than its capacity.

## Decided

- **F1. When a producer writes a line and the buffer is already at capacity, what should happen?** A. The producer waits until the shipper frees a slot, so no line is ever lost. (decided by @gitdek: https://github.com/gitdek/invariant/issues/1#issuecomment-5837111263)
- **F2. When the shipper fails to send a line, what should happen to that line?** A. The line stays at the front of the buffer and the shipper retries it before sending any later line. (decided by @gitdek: https://github.com/gitdek/invariant/issues/1#issuecomment-5837111263)
