# Invariant · Spec

This file describes the current state only. It's rolled up from the ratified and decided entries in [`decisions/log.md`](decisions/log.md), and every line cites the decisions behind it. A line that no ratified or decided entry supports is a bug in this file. History and reasoning live in the log.

**Rolled up through** D-0032 · 2026-09-25 (every entry is ratified or decided)

## What Invariant is

A code factory that turns GitHub issues into merged pull requests. The code in each pull request is proved against formal statements that people have ratified. `D-0000` `D-0002`

## Principles

- **People decide what must be true, and the factory proves that it is.** The factory drafts formal statements, and a person ratifies them before the factory builds against them. Ratified statements are pinned by hash, and the factory cannot change them. Changing one takes a new decision. `D-0002`
- **The factory doesn't guess.** When it can't formalize an issue without choosing between interpretations, it posts the fork on the issue as a decision request and does not proceed on that fork. `D-0002`
- **Checks can't be quietly weakened.** The gate writes TLC's config itself from what was ratified. Reachability witnesses show the invariants don't hold only because the model does nothing. Known bugs show the invariants are strong enough to catch real mistakes. `D-0011` `D-0013` `D-0017`

## What gets ratified

- Each project's `.invariant/ratified.lock` records five things. `D-0011` `D-0013` `D-0026`
  - The spec, `Init /\ [][Next]_vars`.
  - The invariants.
  - The bounds TLC checks them within.
  - The reachability witnesses.
  - The known bugs, as TLA+ actions the invariants must catch.
- Each pin covers a statement and every definition it depends on, stopping at the factory's `Init` and `Next`. `D-0026`
- The factory owns the model (`Init`, the actions and `Next`), the implementation and its contracts. Each contract restates one TLA+ action. `D-0011` `D-0026` `D-0031`
- A person records a ratification with `invariant pin`. `D-0017`

## System

- Invariant is written in Go. `D-0001`
- Invariant targets Go, TypeScript and Python, the languages of @gitdek's projects. `D-0023`
- Every language gets a proof path and a conformance path, and each receipt says which one ran. Proofs fit new code written for a verifier; existing code is tested against the model. `D-0024`
  - Go: proved with Gobra, and tied to the model by agreement. `D-0003` `D-0026`
  - Python: new cores are proved with Nagini, in files marked `# +nagini`, and still tested against the model by conformance. Existing code is tested by conformance alone. `D-0024` `D-0029` `D-0031`
  - TypeScript: tested against the model by conformance. No proof path yet. `D-0024` `D-0029`
- Conformance: a project's driver calls the code's operations at random and records each state in the spec's vocabulary. TLC checks that every run starts in a state `Init` allows and that every step that changes the state is a `Next` step. Runs use a fixed seed, so receipts reproduce. `D-0029`
- Designs are model-checked with TLA+ and TLC. Go code is verified with Gobra, and Python cores with Nagini. Lean 4 has no role in v1. `D-0003` `D-0010` `D-0031`
- Verifiers run in Docker and are pinned. `D-0000` `D-0013` `D-0016` `D-0020`
  - TLC is v1.7.4, pinned by SHA-256, running in `eclipse-temurin` pinned by digest.
  - Gobra is pinned by digest, and it also checks integer overflow.
  - Builds, tests and exploration run in `golang:1.27-alpine`, pinned by digest, on a throwaway copy.
  - TypeScript runs in `node:24-alpine` and Python in `python:3.13-alpine`, both pinned by digest and limited to the standard library. Type checking isn't run yet. `D-0030`
  - Nagini 1.3.1 runs in an image built from a recipe inside Invariant, for linux/amd64 only. Every input is pinned: the base by digest, the Java runtime by copying it from TLC's Temurin image, and every Python package by wheel hash. Receipts name the recipe by its hash. `D-0031` `D-0032`
  - Nothing runs with network access or with the host's environment.
- Invariant converts TLC counterexamples into JSON traces. `D-0016`
- Each project is its own Go module. `D-0020`
- Synthesis backends are pluggable: direct model APIs, or headless coding agents such as Claude Code and Codex. `D-0000`
- Synthesis runs locally through the official coding-agent CLIs in their documented headless modes, on @gitdek's own accounts. Anything shared, hosted or run in CI uses an API key. `D-0028`
- A synthesis agent starts from a skeleton that holds only the pinned definitions. It works outside the repository, with file tools and the gate as its only tools. Every gate run checks a project assembled from the original lock, manifest, request and `go.mod`, plus the agent's model and code. `D-0026`
- The agent gets at most four gate runs: one attempt and three repairs. Each run is capped by an estimated-cost budget, a turn limit and a timeout. `D-0000` `D-0026`
- The factory is triggered by `issues.opened`, by `/invariant solve` issue comments, and by `repository_dispatch`. `D-0000` `D-0014`
  - Only users with write access can trigger it.
  - Synthesis runs with no secrets and no network.
- Each piece of work happens on a branch named `invariant/issue-<id>-<slug>`. Formal artifacts live under `.invariant/specs/`. `D-0000`
- When a check fails, the counterexamples and verifier errors feed back into synthesis for up to three repair attempts. If it still fails, the PR is labeled `invariant:human-review-needed` and the counterexample is posted on the issue. `D-0000`
- Every pull request carries a receipt that CI generates from tool output only. `D-0000` `D-0013`
  - It lists the states explored, the bounds, the functions verified, the statement hashes and the tool versions.
  - Its fingerprint matches between a local run and a CI run of the same commit.
- The CLI's `verify`, `synthesize`, `pin` and `trace` commands are built. `init` is specified but not yet built. `D-0000` `D-0013` `D-0017` `D-0026`

## Merging

- Pull requests auto-merge starting with the first factory PR. `invariant/gate` is a required status check, and GitHub's native auto-merge merges once it passes. `D-0004` `D-0021`
- The gate passes only when all of these hold. `D-0004` `D-0013` `D-0014`
  - The pinned statements match.
  - TLC reports no invariant violation and no deadlock.
  - Every witness is reachable.
  - Every known bug is caught.
  - Go: the code, explored from `Init()` through `Successors()`, reaches exactly the states TLC found, at the same depth.
  - TypeScript and Python: every step the code took is a step the model allows.
  - Gobra verifies Go files marked `// +gobra`, Nagini verifies Python files marked `# +nagini`, and the receipt names any function neither saw. `D-0031`
  - CI is green.
  - The diff stays in scope: no new module dependencies, no cgo, no edits to `.github/` or pinned specs.
- The pinned-statement checks and the scope rules must exist before the factory opens its first PR. `D-0004` `D-0014`
- After a merge, the branch is deleted and the originating issue is notified with the proof artifacts. `D-0000`

## Current focus

- Slices, in order: `D-0005` `D-0013` `D-0024`
  1. The gate, proven on `examples/02-twophase-commit`. Done.
  2. Synthesis for Go. Done.
  3. TypeScript and Python: conformance for existing code, then a Nagini spike. Done.
  4. GitHub. Next.

## Project

- Brand: a mark of one state orbiting a fixed point, with the invariant in the accent color. `D-0006` `D-0009`
- The README's graphics are animated, and they're generated from real `invariant verify` output. `D-0015` `D-0025`
- Hosted at `github.com/gitdek/invariant`. Private now, public later. `D-0007`
- Licensed under Apache-2.0. `D-0012`
- A portfolio piece on puglisij.com, with a project card, the Trace Explorer, the Dual Proof Terminal and the PR receipt badge. `D-0008`
  - They're built from real tool output only.
  - Public copy states the checked bounds instead of claiming "100%". `D-0015`

## Undecided: don't build against these

Nothing is undecided right now.
