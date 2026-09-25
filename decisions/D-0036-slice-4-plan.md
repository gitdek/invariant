---
id: D-0036
title: Slice 4 plan and acceptance criteria
date: 2026-09-25
door: two-way
status: decided
decided_by: Claude
source: slice 4 build (Claude Code session 2f96fee2), carrying out D-0013, D-0024 and D-0033 to D-0035
---

# D-0036 · Slice 4 plan and acceptance criteria

**Goal** (from [D-0013](D-0013-slice-plan.md) and the README). An issue becomes a decision request, then a ratification, then a pull request, then a merge. People decide what must be true on the issue itself, and the factory does the rest.

## How the factory runs

- **Locally, per [D-0028](log.md).** `invariant watch -repo OWNER/NAME` runs on @gitdek's machine. It polls GitHub through the `gh` CLI with @gitdek's login, and runs formalization and synthesis with Claude Code on @gitdek's account. CI runs only the gate, with no agent and no API key.
- **Triggers.** A writer opens an issue with a `/invariant solve` line, gives an issue the `invariant` label, or comments `/invariant solve`. Issues nobody hands to the factory are left alone, so the factory can share a repository with ordinary issues. D-0000's `repository_dispatch` trigger waits for a hosted factory, because a local poller can't receive it.
- **No state of its own.** Everything the factory knows is on GitHub. Each factory comment ends with a hidden marker recording the issue's state and the commands it answered. A status label shows the state in the issue list. The watcher can stop and start at any time, and it picks up a build that stopped partway.
- **Only writers are heard.** Commands and comments count only from people with write access (D-0014). The factory posts from @gitdek's account, so every comment it writes starts with "◉ **Invariant**", and it never treats its own comments as commands. The agents never touch GitHub: only the factory's Go code posts, pushes and merges.

## The flow on an issue

1. **Formalize.** An agent drafts the statements, their bounds and a draft model, in a scratch directory, with file tools and a `check` tool as its only tools. The check tool runs the gate's model checks: TLC, the witnesses and the known bugs. Or, when the issue allows materially different behaviors, the agent lists them as forks instead of choosing ([D-0002](D-0002-people-ratify-statements.md)).
2. **Ask.** The factory posts the forks as a decision request. A writer answers each with `/invariant choose F1 A`, or explains in words and comments `/invariant revise`. Once every fork is answered, the factory drafts again with the answers.
3. **Propose.** The factory checks the draft itself before posting it. The proposal shows each statement in plain language, the pinned TLA+, the bounds, what TLC found and the decisions it carries. It also gives the hash of the whole proposal ([D-0034](log.md)).
4. **Ratify.** A writer comments `/invariant ratify` with at least 12 characters of the hash. The factory writes the project under `examples/NN-slug`: manifest, lock, module, request, `go.mod` and a README. The lock records who ratified, where and what. This is the first commit on `invariant/issue-<n>-<slug>`.
5. **Build.** Synthesis starts from the drafted model, writes the code and gates it, as in slice 2. The second commit holds the code. The factory opens a pull request that closes the issue and carries the receipt. If the gate never passed, the pull request is a draft labeled `invariant:human-review-needed`, and the failure is posted on the issue.
6. **Merge** ([D-0033](log.md)). The factory merges only when all of these hold:
   - `invariant/gate` has passed on the pull request's exact head commit.
   - The pull request stays in scope.
   - Its lock is the proposal that was ratified.

   It merges with a merge commit, so the ratification stays a commit of its own. Then it deletes the branch and says so on the issue.

## The gate gains

- **Scope** (`invariant scope`, D-0014). A factory pull request changes exactly one project and nothing else. It may not edit `.github/`, add a module dependency, use cgo, or change an existing project's ratified lock. A new project must carry a ratification record. CI runs this on factory branches, and the factory runs it again before merging.
- **Ratification** (`invariant ratification`). For each project with a ratification record, the lock must still hash to the ratified proposal. The comment it names must be in this repository, on the recorded issue and by the recorded person. That person must have write access, the comment can't be the factory's own, and it must ratify exactly that hash. CI checks this through the API on every run.
- **Every project, found.** CI verifies every project in the repository, discovered from the manifests, so a factory pull request is gated without editing the workflow.

## Slice 4 is done when

1. Unit tests run the whole flow against a fake GitHub, a scripted formalizer and builder, and real git: issue, fork, answer, proposal, ratification, pull request, green gate, merge. They also cover the failure paths:
   - Commands from a reader are ignored.
   - A premature or mismatched ratification changes nothing.
   - A red gate, an out-of-scope push and a failed build never merge.
   - A stuck formalization recovers on `/invariant revise`.
2. Unit tests show the scope check and the ratification check reject each way around them listed above.
3. An integration test shows the formalizer's check runs real TLC on a draft, and fails a model that breaks an invariant.
4. Live: the bounded buffer issue ([D-0035](log.md)) on gitdek/invariant goes from issue to merged pull request. Its fork is asked and answered by @gitdek, @gitdek ratifies it, and CI's gate passes before the factory merges.

## Known gaps

- The factory posts as @gitdek. A bot account or GitHub App would give it its own identity.
- It builds new projects only. Changing an existing project's ratified statements needs a flow of its own.
- Synthesis writes Go only, so factory projects are Go for now.
- The ratification check reads a person's permission as it is today, not as it was when they ratified.
