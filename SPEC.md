# Invariant · Spec

This file describes the current state only. It is rolled up from the ratified entries in [`decisions/log.md`](decisions/log.md), and every line cites the decisions behind it. A line that no ratified decision supports is a bug in this file. The history and the reasons belong in the log.

**Rolled up through** D-0008 · 2026-09-25

## What Invariant is

A code factory that turns GitHub issues into merged pull requests. The code in each pull request is proved against formal statements that people have ratified. `D-0000` `D-0002`

## Principles

- **People decide what must be true, and the factory proves that it is.** The factory drafts formal statements, and a person ratifies them before the factory builds against them. Ratified statements are pinned by hash and the factory cannot change them. Changing one takes a new decision. `D-0002`
- **The factory doesn't guess.** When it can't formalize an issue without choosing between interpretations, it posts the fork on the issue as a decision request and does not proceed on that fork. `D-0002`

## System

- Invariant is written in Go. `D-0001`
- Generated code is Go, verified with Gobra. Designs are model-checked with TLA+ and TLC. `D-0000` `D-0003`
- Verifier toolchains run in Docker. `D-0000`
- Synthesis backends are pluggable: direct model APIs, or headless coding agents such as Claude Code and Codex. `D-0000`
- The factory is triggered by `issues.opened`, by `/invariant solve` issue comments, and by `repository_dispatch`. `D-0000`
- Each piece of work happens on a branch named `invariant/issue-<id>-<slug>`. Formal artifacts live under `.invariant/specs/`. `D-0000`
- When a check fails, TLC counterexamples and verifier errors feed back into synthesis for up to three repair attempts. If the check still fails, the PR is labeled `invariant:human-review-needed` and the counterexample is posted on the issue. `D-0000`
- Every pull request carries a verification receipt with an issue summary, the states TLC explored, the properties verified, and a diff summary. `D-0000`
- The CLI commands are `invariant init`, `invariant verify`, `invariant synthesize` and `invariant trace`. `D-0000`

## Merging

- Pull requests auto-merge starting with the first factory PR. `invariant/gate` is a required status check, and GitHub's native auto-merge merges once it passes. `D-0004`
- The gate passes only when all of the following hold:
  - TLC reports no invariant violations and no deadlocks.
  - Gobra verifies the code.
  - CI is green.
  - The pinned statements are unchanged.
  - The diff stays in scope.

  Pinned-statement checks and scope rules must exist before the factory opens its first PR. `D-0000` `D-0003` `D-0004`
- After a merge, the branch is deleted and the originating issue is notified with the proof artifacts. `D-0000`

## Current focus

- First slice: `examples/02-twophase-commit`. `D-0005`

## Project

- A logo and a polished README from day one. `D-0006`
- Hosted at `github.com/gitdek/invariant`: private now, public later. `D-0007`
- A portfolio piece on puglisij.com, with a project card, the Trace Explorer, the Dual Proof Terminal and the PR receipt badge. `D-0008`

## Undecided: don't build against these

| ID | Question | Status |
| :-- | :-- | :-- |
| D-0009 | Logo and brand system | proposed |
| [D-0010](decisions/D-0010-role-of-lean.md) | Role of Lean 4 | open |
| [D-0011](decisions/D-0011-ratification-scope.md) | What gets ratified, and what ties the Go to the TLA+ model | open |
| [D-0012](decisions/D-0012-license.md) | License | open; must be decided before going public |
| [D-0013](decisions/D-0013-slice-plan.md) | Slice plan, and slice 1 acceptance criteria | proposed |
| D-0014 | Security baseline for triggers and synthesis | proposed |
| D-0015 | Showcase built from real tool output | proposed |
