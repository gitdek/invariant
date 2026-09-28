---
id: D-0105
title: The factory takes every change, and a whole PRD
date: 2026-09-28
door: one-way
status: ratified
ratified_by: "@gitdek"
proposed_by: agent
source: "@gitdek's direction on 2026-09-28: the goal he and friends set for Invariant is that you hand it a full PRD, and it generates the code, then creates and handles the issues. He also wants coding agents to use Invariant to create their pull requests, instead of working outside it."
---

# D-0105 · The factory takes every change, and a whole PRD

**Goal.** Every change to Invariant goes through the factory, and a whole PRD can go in as one request. A person still decides every step that's theirs: the plan, each issue's proposal, and every merge into the trusted base.

## Why now

The work that builds Invariant happens outside Invariant. The PRD's "Who builds what" gives the factory only what it can check against a model, and a coding agent builds the rest in a session with @gitdek ([D-0048](../docs/PRD.md), [D-0045](D-0045-slice-6-plan.md)). Slice 12's pull requests so far, #84 to #87, were built that way.

@gitdek wants that work inside the system. And the goal he and friends set is bigger: hand Invariant a full PRD, and it plans the issues, writes the code, and handles the issues itself.

The factory already has what the flow needs: a proved issue protocol, crash recovery, a lease, questions and ratification by hash, CI's gate and a second agent's review. For plumbing, only the proposal's shape and the checks differ.

## What it would be

1. **Two kinds of issue.** A modeled issue works as today: statements, TLC, then a proof or a check against the model. A plumbing issue's proposal is a plan: the change, the files it touches, and the acceptance tests that show it works. People ratify the plan by its hash, as they ratify statements. The factory's agent writes the code and the tests. The checks are CI's tests, the decision graph, and a second agent's review of the diff against the plan.
2. **Scope.** A plumbing pull request changes only the files its ratified plan names. The factory may build a change to the trusted base, meaning the gate, the verifiers, the scope and ratification checks, CI and the factory's own code, but only a person merges it.
3. **A PRD becomes a plan.** `/invariant plan` on an issue that holds or links a PRD. The factory drafts the issues it would take, in order, each marked modeled or plumbing, with what each depends on and the forks it can't settle. @gitdek answers the forks and ratifies the plan by its hash. The factory then opens the issues and works them one at a time, each through its own ratification.
4. **Agents in sessions.** A coding agent working with @gitdek starts every change as an issue and hands it to the factory. It builds a change by hand only when the factory can't yet, and says so on the issue.
5. **Honest receipts.** Plumbing is tested, not proved, and its receipt says so ([D-0015](log.md)).

What stays is D-0048's principle: proofs where the rules are, tests where the plumbing is, and a person makes every decision. What changes is who runs the process for plumbing: the factory, instead of an agent outside it.

## Options considered

| Option | What it gives | What it costs |
| :-- | :-- | :-- |
| **A. Plumbing through the factory, and PRDs as plans** (recommended) | Any change and any PRD go through one flow, with a person deciding every step | The most to build: plan proposals, a plumbing gate, and a planner |
| B. Plumbing through the factory only | Agents' work goes through Invariant now | A person still turns a PRD into issues |
| C. Keep D-0048's split, with an issue per change | A record of every change on GitHub | The factory still builds none of the plumbing |
| D. Keep things as they are | Nothing new to build | Agents keep working outside the system |

## Questions for @gitdek, with recommendations

1. **A, B, C or D?** Recommend A.
2. **Who merges plumbing?** Recommend the factory merges plumbing outside the trusted base once CI and the review pass, as it merges projects today. A person merges anything in the trusted base.
3. **How big is a plan?** Recommend at most 10 issues, worked one at a time in order, so a person can follow along. A bigger PRD becomes a second plan once the first is merged.
4. **When?** Recommend slice 13, starting right after the MCP decision tools, before the rest of slice 12. A coding agent finishes those tools by hand first, because the factory's agents need them to record the calls they make while building. The dashboard's graph and copythis-ad's decisions then become the first plumbing issues, and the Codex backend moves after.

## Ratified by @gitdek

@gitdek ratified D-0105 with all four recommendations on the evening of 2026-09-28:

1. **Option A.** Plumbing goes through the factory, and a PRD becomes a plan of issues.
2. **Merging.** The factory merges plumbing outside the trusted base once CI and the review pass. A person merges the trusted base.
3. **Plans** hold at most 10 issues, worked one at a time.
4. **Slice 13** starts right after the MCP decision tools. The dashboard's graph and copythis-ad's decisions are its first plumbing issues, and the Codex backend moves after.

## Acceptance

1. A plumbing issue goes from opened to merged through the factory. Its plan is ratified, the factory's agent writes the code and tests, and it merges once CI and the review pass.
2. A trusted-base change built by the factory waits for a person to merge it.
3. A PRD with at least three issues becomes a ratified plan, and the factory opens and works each one, asking what it can't settle.
4. The dashboard's graph and copythis-ad's decisions land this way.

## What would reopen this

- Plumbing reviews that keep missing what the tests don't catch.
- Plans that go wrong often enough that a person should write the issues.
