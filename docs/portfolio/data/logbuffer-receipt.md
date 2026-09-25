## ◉ Invariant receipt · bounded log shipping buffer

**✅ Pass.** Every check passed. The code is **proved**. Fingerprint `sha256:dfaf31c67a3f`

| Check | Result | Evidence |
| :-- | :-- | :-- |
| Pinned statements | ✅ 13 of 13 match | ratified by @gitdek on [#1](https://github.com/gitdek/invariant/issues/1#issuecomment-5837225662) |
| Design · TLC | ✅ no violations, no deadlock | 87 distinct states (159 generated), depth 9 |
| Reachability | ✅ 3 of 3 witnesses reached | `BufferFull` in 2 steps, `RetryPending` in 2 steps, `AllShipped` in 8 steps |
| Known bugs | ✅ 3 of 3 caught | write-over-capacity: `BoundedCapacity` violated after 3 steps, drop-on-failure: `NoLineLost` violated after 2 steps, resend-delivered: `ShippedOnce` violated after 2 steps |
| Agreement | ✅ code reaches the model's states | 87 states, depth 9 |
| Code · Gobra | ✅ proved: 5 of 5 functions verified | 5 with contracts, overflow checked, not verified: Successors |
| Build | ✅ go vet, go test | sandboxed, no network |

Checked within `Capacity = 2`, `MaxLines = 2`, `Producers = {p1, p2}`. Within these bounds TLC's search is exhaustive. Nothing is claimed outside them.

<details>
<summary>Ratified statements</summary>

| Statement | Kind | Says | Pin |
| :-- | :-- | :-- | :-- |
| `Spec` | spec | The system starts in Init, and every step is a Next step. | ✅ `sha256:71e5ada548bf` |
| `TypeOK` | invariant | The buffer, the shipped lines and the write history are lists of log lines, each producer has written a bounded number of lines, and the shipper is either retrying or not. | ✅ `sha256:fa8713a219f2` |
| `BoundedCapacity` | invariant | The buffer never holds more lines than its capacity. | ✅ `sha256:a303924cda7f` |
| `NoLineLost` | invariant | Every line a producer has written is either still in the buffer or has been shipped. | ✅ `sha256:4195c3ffbb4e` |
| `ShippedOnce` | invariant | No line is shipped more than once. | ✅ `sha256:4d3b8a454385` |
| `ShippedInOrder` | invariant | The shipped lines are exactly the oldest written lines, in the order they were written. | ✅ `sha256:769363f454b1` |
| `BufferInOrder` | invariant | The buffer holds exactly the written lines not yet shipped, oldest first. | ✅ `sha256:862c93131d2a` |
| `BufferFull` | witness | The buffer can fill up to its capacity. | ✅ `sha256:80f0d0944730` |
| `RetryPending` | witness | A send can fail and leave its line in the buffer to be retried. | ✅ `sha256:96682c6fe092` |
| `AllShipped` | witness | Every producer can finish writing and every line can be shipped. | ✅ `sha256:3ea60c920056` |
| `WriteOverCapacity` | bug | A producer adds a line to a buffer that is already full. | ✅ `sha256:1079fc99b89e` |
| `DropOnFailure` | bug | When a send fails, the line is thrown away instead of retried. | ✅ `sha256:f5da4e6db520` |
| `ResendDelivered` | bug | A send that actually went through is treated as failed and retried, so the line is shipped twice. | ✅ `sha256:54ab9512cf75` |

</details>

<details>
<summary>Toolchain</summary>

| Tool | Pinned at |
| :-- | :-- |
| TLC | TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802) · tla2tools.jar v1.7.4 `sha256:936a262061c9` |
| Java | `eclipse-temurin@sha256:49e21e16e3c8` |
| Gobra | `ghcr.io/viperproject/gobra@sha256:775879e84835` |
| Go sandbox | `golang@sha256:8a5910f31396` |
| Go | go1.27.1 |

</details>

<sub>Generated 2026-09-25T18:46:09Z from tool output only. Fingerprint `sha256:dfaf31c67a3f78c8909ce205f89823204aa20237a2b2b69553cba0817064d31f`</sub>
