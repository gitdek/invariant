# Invariant: portfolio handoff for puglisij.com

Add Invariant to puglisij.com as a project page, with the interactive pieces ratified in [D-0008](../../decisions/log.md): a project card, a Trace Explorer, a Dual Proof Terminal and a PR receipt badge. A fifth piece now exists: the factory's first real issue, from opened to merged.

This file and the data beside it are the source of truth for copy and numbers. The Invariant repository (`~/projects/invariant`) is read-only for the portfolio work. Every number, state, trace and line of code on the page must come from the files listed here (D-0015). If something isn't here, leave it out or ask Joseph.

## Before you build

- **The repository is public** ([D-0075](../../decisions/D-0075-slice-10-plan.md)). Link to `github.com/gitdek/invariant`, its issues and its pull requests.
- **Publishing is Joseph's call**, as with every page on the site. Build on a branch, such as `work/invariant`, and propose a plan first.
- **Audience.** The homepage speaks to business owners, and Invariant is an engineering project. Where it sits on the homepage is an open decision (see the end of this file).

## What Invariant is

One sentence: **Invariant is a code factory that turns GitHub issues into merged pull requests, whose code is proved against formal statements a person ratified on the issue.**

The problem it answers: AI coding agents don't ask clarifying questions. They pick an interpretation and ship it confidently. A proof alone doesn't fix that, because if the agent also wrote the property, the proof only certifies the agent's guess. Invariant splits the work: people decide what must be true, and the factory proves the code satisfies it.

How it works, as built (sources: `README.md`, `SPEC.md`):

1. **Formalize.** An agent reads the issue and drafts formal statements in TLA+: invariants (what must never go wrong), witnesses (outcomes that must be reachable, so the invariants can't pass because nothing happens) and known bugs (plausible mistakes the invariants must catch).
2. **Ask.** When the issue allows materially different behaviors, the factory doesn't pick. It posts the question on the issue and waits.
3. **Ratify.** The factory checks its draft with the TLC model checker before anyone sees it. A person with write access ratifies by commenting `/invariant ratify <hash>`. The statements are then pinned by hash, and the factory can't change them.
4. **Synthesize.** An agent writes the code against the ratified statements, confined to its workspace with file tools only. The gate checks it, and the agent gets up to three repairs.
5. **Gate and merge.** CI re-runs every check. The factory merges its own pull request only when CI's gate passes on that exact commit.

The gate's checks:

- **Pins:** the ratified text hasn't changed.
- **TLC:** no invariant violated and no deadlock, exhaustively within the checked bounds.
- **Reachability:** every witness can be reached.
- **Known bugs:** each bug is caught.
- **Agreement:** the Go code reaches exactly the model's states.
- **Proof:** Gobra (Go) or Nagini (Python) verifies each function against a contract that restates one TLA+ action.
- **Build:** vet and tests pass, in a sandbox with no network.
- **Scope:** a factory PR touches only its own project.
- **Ratification:** CI confirms on GitHub that a writer ratified exactly the proposal in the lock.

Languages: Go is proved with Gobra. New Python cores are proved with Nagini. Existing TypeScript and Python code is tested against the model ("conformance"), and receipts always say which kind of evidence they carry: "proved" or "tested against the model".

It runs locally: `invariant watch` polls GitHub through the `gh` CLI and runs its agents with Claude Code on Joseph's own account. Invariant itself is written in Go.

## The numbers you can use

All of these come from the data files below. Quote bounds with any state count. Never say "100%", "bug-free" or "all states" without the bounds.

**Two-phase commit, the hand-built example** (`data/twophase-receipt.json`):

- TLC explored 288 distinct states (1,146 generated), depth 11, exhaustive within `RM = {r1, r2, r3}`: three resource managers.
- Witnesses: every resource manager committed is reachable in 10 steps; all aborted in 3.
- The planted early-commit bug is caught in 5 steps: `TCConsistent` is violated.
- Gobra verified 10 of 10 functions, with integer overflow checked. The code reaches exactly the model's 288 states.
- The same model is also implemented as:
  - Python proved with Nagini: 8 of 8 functions, all 288 states.
  - Plain TypeScript tested against the model: 283 of 288 states visited.
  - Plain Python tested against the model: 275 of 288.
- The factory rebuilt the model and Go code from the ratified statements alone. It passed on its first gate run, in 12 turns and under two minutes.

**The factory's first issue** (`../assets/factory-run.json`; `data/logbuffer-receipt.json`):

- Issue #1, "Add a bounded buffer for log shipping", opened 17:58:44 UTC on September 25, 2026.
- 42 seconds later the factory asked 2 questions instead of guessing:
  - What happens when a producer writes to a full buffer?
  - What happens when a send fails?
- Joseph chose F1 A (the producer waits until there's room) and F2 A (the failed line is retried before any later line).
- The factory proposed 13 statements: 6 invariants, 3 witnesses and 3 known bugs, already checked by TLC in 87 states, within `Capacity = 2`, `MaxLines = 2`, `Producers = {p1, p2}`.
- Joseph ratified. The factory wrote the Go code (Gobra: 5 of 5 functions proved; the code reaches all 87 states) and opened PR #2.
- CI's gate passed. It checked the scope, and checked Joseph's ratifying comment on GitHub. The factory merged #2 as `3ce3ba3`, 26 minutes after the issue was opened. About 10 of those minutes were Joseph deciding. CI took 11 minutes 40 seconds, and the factory merged 12 seconds after it passed.
- The receipt's fingerprint is identical locally and in CI: `sha256:dfaf31c67a3f`.

## The pieces

Match the site's existing interactive pieces: plain HTML, CSS and JavaScript, same-origin, under the current Content Security Policy. Keyboard operable. `prefers-reduced-motion` shows the finished state without animation. They must work on a phone.

### 1. Project card and Work row

- Name: **Invariant**. Mark: `../brand/mark.svg` or `mark-dark.svg`, one state orbiting a fixed point.
- Tag, suggested: `AI agents · Formal verification · Developer tools`.
- Lede, suggested: "You decide what must be true. It proves the code."
- Deck, suggested: "A code factory that turns GitHub issues into merged pull requests, with code proved against statements a person ratified on the issue."

### 2. The factory timeline (the page's centerpiece)

Replay issue #1 from opened to merged. Draw it from `../assets/factory-run.json`: the issue, every comment (the factory's hidden state markers are already decoded into `marker`), and the pull request. `../assets/factory-run.svg` is the README's animated version: match its facts, not necessarily its look.

- Seven events on a rail, each with its UTC time and the time since the issue opened. A person's events and the factory's are told apart by color and shape.
- Each event opens to show what was said:
  - the two questions and their options
  - Joseph's answers
  - the 13 statements in plain language
  - the ratify comment
  - the PR's receipt
  - the merge
- Close on the total: issue to merge in 26 minutes, with three decisions made by a person.

### 3. Trace Explorer

Step through the real TLC counterexample for the early-commit bug in two-phase commit: `data/twophase-early-commit.json`, which has 6 states. Each state has:

- `action`: the TLA+ action taken to reach it
- `changed`: which variables changed
- `vars`: the values as JSON
- `tla`: the same values in TLA+ syntax

Suggested design:

- The coordinator (`tmState`) and three resource managers (`rmState`) as chips: working, prepared, committed, aborted. Plus the message set (`msgs`) and `tmPrepared`.
- Step forward and back with buttons and the arrow keys. Autoplay is optional.
- Highlight what changed at each step. On the last step, flag that `TCConsistent` is violated: r1 has committed while r2 has aborted.
- Say in words what happens: r1 prepares, the coordinator records it, r2 aborts on its own, the buggy coordinator commits on r1's vote alone, and r1 commits.
- Optional: the factory-built log buffer's three bug traces (`data/logbuffer-*.json`) as a second tab, in the same format.

The brief first described this piece as "transitioning from a deadlock into a mathematically sound state machine". That's superseded. Show the real counterexample, and explain that the gate plants known bugs and requires TLC to catch each one, which is how you know the invariants have teeth.

### 4. Dual Proof Terminal

A split view: the TLA+ action on one side, and the Go function with its Gobra contract on the other. Each contract restates exactly one action.

- TLA+: `examples/02-twophase-commit/.invariant/specs/TwoPhase.tla`, lines 72 to 76 (`RMPrepare(r)`).
- Go: `examples/02-twophase-commit/twophase/twophase.go`, lines 96 to 109 (`RMPrepare`).
- Link the clauses: the enabling condition `rmState[r] = "working"` matches `// @ requires s.RM[r] == Working`. The effect matches the `ensures` lines, and `UNCHANGED` matches the frame `ensures` lines.
- A terminal strip beneath can replay the real receipt lines from `data/twophase-receipt.md`. For example, "Design · TLC ✅ no violations, no deadlock · 288 distinct states" and "Code · Gobra ✅ proved: 10 of 10 functions verified".
- The brief first said "Lean 4 tactics beside Rust". That's superseded: Lean was dropped and the code is Go (D-0003, D-0010).

### 5. PR receipt badge

A GitHub-style check badge for PR #2, expanding into the receipt table. Draw it from `data/logbuffer-receipt.json` (the two-phase commit receipt works too).

- Collapsed, suggested: `✓ invariant/gate · proved · 87 states, exhaustive within bounds · merged by the factory`.
- Expanded: the receipt rows (pins, TLC, reachability, known bugs, agreement, code, build), the bounds, and the fingerprint.
- The brief's "100% States Checked | 0 Axiom Violations | Auto-Merged" is superseded. Public copy states the bounds and never claims 100% (D-0015).

## Copy rules

The site's rules apply: no em dashes, `↗` only on links that leave the page, `→` only where it points at something, and concrete copy with no slogans.

For Invariant specifically:

- **"Proved" means Gobra or Nagini verified it.** Conformance-tested code is "tested against the model".
- **Model checking is exhaustive within bounds.** Say which bounds.
- **The factory doesn't work "without humans".** People decide the forks and ratify the statements. The factory then merges on its own, only after CI's gate passes.
- **No Lean 4, no Rust.** Don't reuse the brief's first micro-copy or its comparison table. Its rows, "Deterministic / Mathematical Proof" and "Eliminated at Design Time", overclaim. If you want a comparison, keep every row true for what's built.
- **What's missing is part of the story.** Invariant checks safety, not liveness. The log buffer's model assumes the shipper knows whether a send succeeded. The factory builds new projects only, and writes Go only.

## Brand

- Ink `#0E1116` on light, `#E6EDF3` on dark. Accent `#0CA678` on light, `#38D9A9` on dark. The invariant is always drawn in the accent.
- Logos: `../brand/logo.svg`, `logo-dark.svg`, `logo-animated.svg` and `logo-animated-dark.svg`, where the state orbits and the invariant holds still. The mark is `mark.svg` and `mark-dark.svg`.
- The README's graphics (`../assets/*.svg`) use GitHub's dark palette, like screens. On the site's tomato ground, framing them as screens, the way screenshots are framed, will likely read best. That's your call.
- The README's animated SVGs are SMIL, generated from real output by `../assets/generate.py`. You can show them as images, but for the site's interactive pieces, build from the JSON so they're keyboard operable and respect reduced motion.

## Data files

Paths are relative to this file.

| File | What it is | Made by |
| :-- | :-- | :-- |
| `data/twophase-receipt.json`, `.md` | Two-phase commit receipt, fingerprint `sha256:2a0189e69e1f` | `go run ./cmd/invariant verify -out out/02-twophase-commit examples/02-twophase-commit` |
| `data/twophase-early-commit.json` | TLC counterexample for the early-commit bug, 6 states | the same run |
| `data/logbuffer-receipt.json`, `.md` | The factory-built log buffer's receipt, fingerprint `sha256:dfaf31c67a3f` | `go run ./cmd/invariant verify -out out/03-log-buffer examples/03-log-buffer` |
| `data/logbuffer-*.json` | The log buffer's three known-bug counterexamples | the same run |
| `../assets/factory-run.json` | Issue #1 and PR #2 as recorded from GitHub, with the factory's markers decoded | `python3 docs/assets/snapshot_factory_run.py 1 2` |
| `../assets/*.svg` | The README's animated graphics | `python3 docs/assets/generate.py` |

Regenerating needs Go and Docker. If you regenerate, fingerprints must match the table, or the page's numbers are stale.

## Open decisions for Joseph

1. **When to publish.** The page can be built now, with links to the repository, issue #1 and PR #2.
2. **Homepage placement.** Invariant is an engineering showcase on a site written for business owners. It could be a full Work row, a smaller "lab" row, or reachable only from its own page.
3. **Naming the tools.** Invariant is agent-agnostic ([D-0052](../../decisions/log.md)): name roles, such as "a coding agent". Where the page says which agents the factory runs, it runs Claude Code today, and a Codex backend is planned.
4. **Costs.** The first issue's three agent runs cost about $0.80 by the agent's own estimate, on a Max plan, so not a charge. Show it or leave it out.
