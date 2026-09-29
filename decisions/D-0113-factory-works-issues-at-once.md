---
id: D-0113
title: The factory works on several issues at once
date: 2026-09-28
door: two-way
status: ratified
ratified_by: "@gitdek"
proposed_by: agent
source: "@gitdek's direction on 2026-09-28, after #97 to #103 waited behind #91's build without a word"
---

# D-0113 · The factory works on several issues at once

**Goal.** A long step on one issue no longer holds up every other issue. While a build runs for two hours, the factory still answers commands, drafts proposals and plans, and builds other issues.

## Why now

The watcher takes one step at a time, across every issue in the repository (`Poll` in `internal/factory/factory.go`). Some steps are short, such as posting an answer. Drafting takes minutes, and a build can take up to its time limit, which was 150 minutes on the evening of 2026-09-28.

That evening, #91's build started at 22:39. Seven plumbing issues filed after it, #97 to #103, waited behind it with no plan and no answer to `/invariant revise`. D-0105 makes this worse over time: every change goes through the factory now, and a PRD's plan opens up to 10 issues.

## What stays the same

Both proved models describe one issue. [`factory/protocol`](../factory/protocol) is one issue's commands and posts, and [`factory/recovery`](../factory/recovery) is one issue's effects, with two watchers, crashes and the repository's lease. Neither says how a watcher orders its work across issues.

Two issues share no post, branch, pull request or run record, since each is keyed by its issue's number (D-0073). So as long as each issue still takes one step at a time, every proved rule holds for each issue as it does now.

The lease stays one per repository. Every effect already checks it before it's taken (D-0074), and the check is safe to make from several steps at once.

## What it would be

1. **One step per issue, several issues at once.** Each poll starts a step for every issue that needs one and has none running, up to a cap, `-parallel N`. A step never starts on an issue whose last step is still running.
2. **Shared things are taken one at a time.** The watcher's clone is changed by one git command at a time, including fetches, worktrees and pushes, so two steps can't race on a ref. Each step already works in its own worktree and its own directory under the work directory.
3. **Losing the lease stops everything.** When the watcher loses the lease, every running step stops before its next effect, and every agent run is dropped, as one is now (`FairDropRun`).
4. **The watcher says everything it's doing.** Its status file lists every running step, and the dashboard shows each one, with the live view #97 builds.
5. **The cap is the lever.** It bounds the machine's load, since each build's gate runs the verifiers in Docker, and how much the agents use the owner's account at once. `-parallel 1` is today's behavior.

## Options considered

| Option | What it gives | What it costs |
| :-- | :-- | :-- |
| **A. Several issues at once in one watcher, one step per issue** (recommended) | Answers, drafts and plans go on during a build, and builds run side by side. The proved rules are untouched | The watcher's plumbing: a scheduler, one git command at a time on the clone, and a status that lists every step. It's tested, not proved |
| B. Two lanes, one for drafting and one for building | A long build no longer holds up a plan or an answer. At most two steps run at once | Builds still queue behind each other, so a plan of 10 issues builds one at a time |
| C. A lease per issue, and watchers on several machines | Scales past one machine | Changes the proved recovery core, whose lease is per repository, so it needs a new model and proof |
| D. Keep one step at a time | Nothing to build | Every issue waits for the longest step ahead of it |

## Questions for @gitdek, with recommendations

1. **A, B, C or D?** Recommend A.
2. **How many at once?** Recommend 3 by default. That's enough for a build, a draft and an answer at the same time, and it keeps the machine's load and the account's usage in check. `-parallel` changes it.
3. **Proved or tested?** Recommend tested. The scheduler is plumbing, and each issue's rules stay proved by the models that prove them now. Its tests run two issues' steps at the same time with Go's race detector on. The crash test stops the watcher before each effect of two overlapping issues, and checks that both still finish with every effect done once. If an interaction between issues ever turns up, model it then.
4. **How?** Recommend a plumbing issue the factory builds once this is ratified. It changes the factory's own code, so a person merges it.

## Ratified by @gitdek

@gitdek ratified D-0113 with all four recommendations on the night of 2026-09-28:

1. **Option A.** The factory works on several issues at once in one watcher, with one step per issue.
2. **Three at a time** by default, which `-parallel` changes.
3. **Tested, not proved.** The scheduler is plumbing, and each issue's rules stay proved as they are.
4. **Built by the factory** as a plumbing issue, and merged by a person, since it's the factory's own code.

## Acceptance

1. While a build runs, the factory drafts plans and answers commands on other issues within one poll.
2. Two builds run at once, and each merges with every effect done once.
3. The crash test passes for two overlapping issues.
4. The dashboard shows every step the watcher is running.

## What would reopen this

- One issue's step changing another issue's state.
- Parallel agent runs hitting the account's limits often enough that the cap should be 1.
- A need to run watchers on more than one machine at once, which is option C.
