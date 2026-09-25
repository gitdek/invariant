# Invariant · Spec

This file describes the current state only. It's rolled up from the ratified and decided entries in [`decisions/log.md`](decisions/log.md), and every line cites the decisions behind it. A line that no ratified or decided entry supports is a bug in this file. History and reasoning live in the log.

**Rolled up through** D-0021 · 2026-09-25

## What Invariant is

A code factory that turns GitHub issues into merged pull requests. The code in each pull request is proved against formal statements that people have ratified. `D-0000` `D-0002`

## Principles

- **People decide what must be true, and the factory proves that it is.** The factory drafts formal statements, and a person ratifies them before the factory builds against them. Ratified statements are pinned by hash, and the factory cannot change them. Changing one takes a new decision. `D-0002`
- **The factory doesn't guess.** When it can't formalize an issue without choosing between interpretations, it posts the fork on the issue as a decision request and does not proceed on that fork. `D-0002`
- **Checks can't be quietly weakened.** The gate writes TLC's config itself from what was ratified. Reachability witnesses show the invariants don't hold only because the model does nothing. Known bugs show the invariants are strong enough to catch real mistakes. `D-0011` `D-0013` `D-0017`

## What gets ratified

- Each project's `.invariant/ratified.lock` records four things. `D-0011` `D-0013`
  - The invariants.
  - The bounds TLC checks them within.
  - The reachability witnesses.
  - The known bugs the invariants must catch.
- The factory owns the TLA+ model, the Go implementation and its Gobra contracts. Each contract restates one TLA+ action. `D-0011`
- A person records a ratification with `invariant pin`. `D-0017`

## System

- Invariant is written in Go. `D-0001`
- Designs are model-checked with TLA+ and TLC. Generated code is Go, verified with Gobra. Lean 4 has no role in v1. `D-0003` `D-0010`
- Verifiers run in Docker and are pinned. `D-0000` `D-0013` `D-0016` `D-0020`
  - TLC is v1.7.4, pinned by SHA-256, running in `eclipse-temurin` pinned by digest.
  - Gobra is pinned by digest, and it also checks integer overflow.
- Invariant converts TLC counterexamples into JSON traces. `D-0016`
- Each project is its own Go module. `D-0020`
- Synthesis backends are pluggable: direct model APIs, or headless coding agents such as Claude Code and Codex. `D-0000`
- The factory is triggered by `issues.opened`, by `/invariant solve` issue comments, and by `repository_dispatch`. `D-0000` `D-0014`
  - Only users with write access can trigger it.
  - Synthesis runs with no secrets and no network.
- Each piece of work happens on a branch named `invariant/issue-<id>-<slug>`. Formal artifacts live under `.invariant/specs/`. `D-0000`
- When a check fails, the counterexamples and verifier errors feed back into synthesis for up to three repair attempts. If it still fails, the PR is labeled `invariant:human-review-needed` and the counterexample is posted on the issue. `D-0000`
- Every pull request carries a receipt that CI generates from tool output only. `D-0000` `D-0013`
  - It lists the states explored, the bounds, the functions verified, the statement hashes and the tool versions.
  - Its fingerprint matches between a local run and a CI run of the same commit.
- The CLI's `verify`, `pin` and `trace` commands are built. `init` and `synthesize` are specified but not yet built. `D-0000` `D-0013` `D-0017`

## Merging

- Pull requests auto-merge starting with the first factory PR. `invariant/gate` is a required status check, and GitHub's native auto-merge merges once it passes. `D-0004` `D-0021`
- The gate passes only when all of these hold. `D-0004` `D-0013` `D-0014`
  - The pinned statements match.
  - TLC reports no invariant violation and no deadlock.
  - Every witness is reachable.
  - Every known bug is caught.
  - Gobra verifies the code.
  - CI is green.
  - The diff stays in scope: no new module dependencies, no cgo, no edits to `.github/` or pinned specs.
- The pinned-statement checks and the scope rules must exist before the factory opens its first PR. `D-0004` `D-0014`
- After a merge, the branch is deleted and the originating issue is notified with the proof artifacts. `D-0000`

## Current focus

- Slices, in order: the gate, proven on `examples/02-twophase-commit`; then synthesis; then GitHub. `D-0005` `D-0013`

## Project

- Brand: a mark of one state orbiting a fixed point, with the invariant in the accent color. `D-0006` `D-0009`
- Hosted at `github.com/gitdek/invariant`. Private now, public later. `D-0007`
- Licensed under Apache-2.0. `D-0012`
- A portfolio piece on puglisij.com, with a project card, the Trace Explorer, the Dual Proof Terminal and the PR receipt badge. `D-0008`
  - They're built from real tool output only.
  - Public copy states the checked bounds instead of claiming "100%". `D-0015`

## Undecided: don't build against these

| ID | Question | Status |
| :-- | :-- | :-- |
| D-0019 | The exact text of the two-phase commit statements and its known bug | proposed |
| D-0022 | A `decided` status for execution-level calls | proposed |
