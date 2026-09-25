---
id: D-0023
title: More target languages
date: 2026-09-25
door: one-way
status: open
source: "@gitdek's questions while slice 1 was being built (Claude Code session 2f96fee2)"
---

# D-0023 · More target languages (open)

**The fork.** @gitdek asked how Invariant would generate and verify TypeScript and Python.

The design checks don't depend on the language. TLA+ and TLC, pinned statements, witnesses and known bugs work the same for any implementation.

What changes per language is the code-level verifier, plus the build and scope rules. The gate already keeps those behind one interface, `verify.Language`. Go with Gobra is its only implementation so far.

**Options per language**

- **Python: Nagini.** A verifier for type-annotated Python, from the ETH Zürich group behind Gobra and built on the same Viper engine. Contracts would restate TLA+ actions exactly as the Gobra contracts do. It's a research tool, and it covers only a typed subset of Python.
- **TypeScript: Dafny compiled to JavaScript.** Write the verified core in Dafny, compile it, and wrap it in TypeScript declarations. The proofs cover the Dafny source, and the Dafny compiler is trusted.
- **Any language: a verified core behind a boundary.** For example, Rust verified with Verus or Kani, and called from TypeScript through WebAssembly or from Python through PyO3.
- **Any language: conformance instead of proof.** Trace validation against the TLA+ spec, plus property tests driven by the model, using fast-check or Hypothesis. The receipt must then say "tested against the model", not "proved".

Build and scope rules change too:

- **Checks:** `tsc` and `vitest`, or `mypy --strict` and `pytest`.
- **No new dependencies:** no changes to `package.json`, `pyproject.toml` or lock files.
- **Nothing that runs at install:** no install scripts, no native extensions.

*Claude suggests Python with Nagini first, because it's the closest match to what the gate already does.*

**Nothing is decided yet.** Revisit after slice 3, or sooner if a real use needs another language.
