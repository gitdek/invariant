---
id: D-0069
title: Slice 9 · the factory survives crashes and concurrent work
date: 2026-09-27
door: one-way
status: proposed
proposed_by: agent
source: slice 9 planning, overnight after slice 7, from a trace of the watcher's side effects
---

# D-0069 · Slice 9 · the factory survives crashes and concurrent work

**Goal** ([PRD 3.8, 4.3, 4.4](../docs/PRD.md), [D-0048](log.md), [D-0058](log.md)). Stop the watcher anywhere and restart it, or run two at once, and no step is lost or done twice: not a post, a push, a pull request, a merge or an agent run. The slice is done when a model of the watcher shows that, including a crash at any point and a second watcher. The factory proves the core that decides what to do after a restart, the watcher runs on it, and a test kills the watcher after each kind of step and shows the issue still merges.

## Where the watcher stands

Slice 7 proved what the factory may do. This slice is about what happens when it stops partway through doing it. Each step is a short sequence of side effects, and a crash can land between any two of them. On restart, the watcher sees only GitHub.

| Step | Side effects, in order | After a crash between them, today |
| :-- | :-- | :-- |
| Draft | agent run, post, label | The command is still pending, so the watcher drafts again: **a second agent run**. |
| Ratify | push the ratification, post, label | The watcher finds its own branch and posts. Nothing is lost or doubled. |
| Build | agent run, push, pull request, post, label | #12 posts the build as stopped, with no second agent run. **If the pull request already exists, it's orphaned:** a writer's retry builds again, and GitHub refuses a second pull request for the branch. |
| Merge | merge, delete the branch, post | The next poll sees the merge and records it, as merged by someone else. **Nothing is lost, but the record is wrong.** |

With **two watchers** on one repository, both see the same pending command, and both act on it:
- two agent runs
- two posts, and two proposals with different hashes
- two pushes, of which one fails
- two merges, of which one fails

The gate still keeps anything wrong from merging. But runs, posts and money are doubled.

Tonight found two more of the same kind. A deferred mark made a refused pull request look finished, so the next poll would build again. And a post over GitHub's length limit would be drafted again on every poll. Both are fixed, but the pattern is general: **a step whose effect didn't happen, or whose result can't be seen on GitHub, gets done again.**

## The design

1. **Every effect is idempotent by key.** Each step names what it's for: the command it answers, the proposal it ratifies, the head it merges. Before acting, the watcher looks for that effect on GitHub and adopts it instead of repeating it:
   - a post that already answers the command
   - the branch that already holds the ratification
   - the open pull request already on the branch
   - the merge that already happened, recorded as the factory's own
2. **Agent runs are recorded before they start.** A draft or build writes its intent to its log directory first, as builds now do with their `done` file. A restart finds an unfinished run and says so on the issue, instead of paying for another. A person's retry is the only thing that starts a new run.
3. **One watcher per repository holds a lease.** It's a ref under `refs/invariant/lease`, created atomically, which GitHub refuses to create twice. The lease names the watcher, and it's renewed on every poll and expires after a few missed polls. A second watcher waits. A lease that has run out can be taken, and the step after taking it is idempotent anyway, so a slow watcher can't double anything.
4. **A proved recovery core decides.** A pure function takes what the watcher sees on GitHub (the issue's posts, its branch, its pull request) and what its logs say, and returns the next effect. It becomes `factory/recovery`, built by the factory from a ratified issue, like `factory/protocol`. The watcher runs on it the way it runs on the protocol.
5. **The gate checks liveness.** A project can ratify "eventually" statements under stated fairness, such as "every pending command is eventually answered, if the watcher keeps running". TLC checks them as temporal properties (3.8). The recovery model is the first to need them: safety alone would pass a watcher that never does anything.

## Options considered

- **A. Idempotent effects plus a lease (recommended).**
  - Idempotency handles crashes, which have to be handled anyway.
  - The lease keeps a second watcher from paying for a second agent run.
  - Each covers the other's gap. A lease can lapse while its holder is still acting, and idempotency makes that harmless.
- **B. Idempotent effects only.** A second watcher can't double a post, a push or a merge. But two watchers can both start an agent run before either posts, and that costs money twice.
- **C. A lease only.** The lease stops concurrency, but a crash inside a step still repeats or loses its effects.

## Plan

1. **Model it.** An issue asks the factory to formalize the watcher's effects for one issue: steps split at every side effect, a crash between any two, a restart that reads GitHub, and a second watcher with a lease. The invariants:
   - no effect twice
   - no agent run twice for one command
   - one pull request per ratification
   - merges only as the protocol allows

   The liveness statement: every pending command is eventually answered, under fairness for the watcher.
2. **Liveness in the gate.** A statement kind for temporal properties with their fairness, checked by TLC, with witnesses and known bugs as today.
3. **The recovery core.** The factory builds `factory/recovery` from the ratified issue, proved with Gobra, the same way as slice 7.
4. **The watcher runs on it.** Idempotency keys in every effect, run intents in the logs, and the lease. The pull request for this touches the trusted base, so @gitdek merges it (D-0048).
5. **Crash tests.** The fake GitHub and repository fail, or the watcher stops, after each kind of effect, and the issue still merges with every effect done once. A second watcher in the same test waits.

## Acceptance

1. The model shows that no effect happens twice and no pending command is lost, with a crash at any point and a second watcher. TLC checks it, liveness included, and every known bug is caught.
2. `factory/recovery` is proved with Gobra, and the watcher decides every restart with it.
3. A test stops the watcher after each kind of effect, and each issue still merges, with one post per command, one agent run per command, and one pull request per ratification.
4. Live: two watchers run on one repository for a day, and the second one never acts.

## Questions for @gitdek, with recommendations

1. **The shape: A, B or C?** Recommend A.
2. **What does a lease live in?** Recommend a Git ref, because creating one is atomic on GitHub. The alternatives are a label or a comment, which two watchers can both add.
3. **Is slice 9 still after slice 8?** Recommend yes, as ratified. The orphaned pull request and the second agent run on a crashed draft are rare and cost money, not correctness. Handle them one issue at a time as they come up.

## What would reopen this

A hosted factory (5.7), which would run many watchers and might need a real coordinator instead of a lease. Or GitHub adding compare-and-swap on labels or comments.
