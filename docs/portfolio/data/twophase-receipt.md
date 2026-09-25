## ◉ Invariant receipt · two-phase commit

**✅ Pass.** Every check passed. The code is **proved**. Fingerprint `sha256:2a0189e69e1f`

| Check | Result | Evidence |
| :-- | :-- | :-- |
| Pinned statements | ✅ 6 of 6 match | recorded in D-0027 |
| Design · TLC | ✅ no violations, no deadlock | 288 distinct states (1,146 generated), depth 11 |
| Reachability | ✅ 2 of 2 witnesses reached | `AllCommitted` in 10 steps, `AllAborted` in 3 steps |
| Known bugs | ✅ 1 of 1 caught | early-commit: `TCConsistent` violated after 5 steps |
| Agreement | ✅ code reaches the model's states | 288 states, depth 11 |
| Code · Gobra | ✅ proved: 10 of 10 functions verified | 10 with contracts, overflow checked, not verified: Successors |
| Build | ✅ go vet, go test | sandboxed, no network |

Checked within `RM = {r1, r2, r3}`. Within these bounds TLC's search is exhaustive. Nothing is claimed outside them.

<details>
<summary>Ratified statements</summary>

| Statement | Kind | Says | Pin |
| :-- | :-- | :-- | :-- |
| `Spec` | spec | The system starts in Init, and every step is a Next step. | ✅ `sha256:0d1aeead3996` |
| `TypeOK` | invariant | Every variable always holds a value of the expected type. | ✅ `sha256:ec91f02b2eba` |
| `TCConsistent` | invariant | No resource manager commits while another aborts. | ✅ `sha256:e3999c24b8da` |
| `AllCommitted` | witness | Every resource manager can end up committed. | ✅ `sha256:aa3963a9e306` |
| `AllAborted` | witness | Every resource manager can end up aborted. | ✅ `sha256:dc2a1880f366` |
| `EarlyCommit` | bug | The coordinator commits once any resource manager has prepared, instead of waiting for all of them. | ✅ `sha256:6cdecc4cd683` |

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

<sub>Generated 2026-09-25T17:05:01Z from tool output only. Fingerprint `sha256:2a0189e69e1f776a7205c2f2c59892932b2c35a3eec7f874957910a79acc2a5c`</sub>
