---
id: D-0003
title: Generated code is Go, verified with Gobra
date: 2026-09-25
door: one-way
status: ratified
ratified_by: "@gitdek"
source: kickoff design review (Claude Code session 2f96fee2)
supersedes: kickoff brief §1–4, which targeted Rust/C++ and used Lean 4 proofs of functional correctness
---

# D-0003 · Generated code is Go, verified with Gobra

**Decision.** The code the factory generates is Go. Code-level verification uses [Gobra](https://github.com/viperproject/gobra), a Go verifier from ETH Zürich built on the Viper infrastructure. Designs are still model-checked with TLA+ and TLC.

**Options considered**

- Rust, with Aeneas translating it into Lean so that the Lean proofs cover the shipped code. Claude recommended this option.
- Go, verified directly with Gobra. ← chosen
- Go, with Lean proving a separate model and property tests checking the Go against that model.

**Why.** One language end to end: Invariant and the code it generates are both Go.

**Implications**

- Lean 4 no longer has a role in code-level proofs. Whether it keeps any role is an open question: [D-0010](D-0010-role-of-lean.md).
- Gobra runs on the JVM with Z3. Its published Docker image is about 255 MB compressed and amd64-only, so on Apple Silicon it runs under emulation.
- Gobra can only verify the subset of Go it supports. Slice 1 is the first real test.

**What would reopen it.** Gobra failing to verify the two-phase commit step functions in slice 1 without unreasonable effort, or the factory needing Go features that Gobra doesn't support.
