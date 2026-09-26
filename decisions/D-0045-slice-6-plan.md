---
id: D-0045
title: Slice 6 plan and acceptance criteria
date: 2026-09-25
door: two-way
status: ratified
ratified_by: "@gitdek"
proposed_by: Claude
source: slice 6 planning (Claude Code session 2f96fee2), carrying out D-0042
---

# D-0045 · Slice 6 plan and acceptance criteria

**Goal** ([D-0042](log.md)). The factory can change a project that already exists, and Invariant starts building itself. Today every ratified issue becomes a new project, and a pull request that changes an existing project's lock is out of scope. Slice 6 lifts that, safely, and then makes the factory's own issue protocol the first part of Invariant that Invariant maintains.

It comes in two parts. Part B needs Part A.

## Part A · Amendments

An **amendment** is an issue that changes an existing project. Its statements are re-ratified as a whole, its code is changed rather than rewritten, and the gate checks the result exactly as it checks a new project.

1. **Naming the project.** The issue names the project it changes, with a line such as `Project: examples/04-api-rate-limiter`. An issue that names no project gets a new one, as today. If an issue plainly reads as a change to an existing project but names none, the formalizer asks which one, as a fork, instead of guessing ([D-0002](D-0002-people-ratify-statements.md)).
2. **Drafting.** The formalizer gets the project's current module and ratified statements, and drafts the full new set. It changes only what the issue asks for and keeps the rest word for word. The factory compares the new set with the lock and sorts every statement into added, changed, removed or unchanged.
3. **The proposal** shows that diff. It starts with what's ratified today and where ("ratified by @gitdek on #3"). Then it lists each added, changed or removed statement, with the old and new TLA+ side by side for changes, and collapses the unchanged ones. Removing or weakening a statement is called out at the top, in bold. TLC checks the new draft before anyone sees it, as now.
4. **Ratifying** works the same way: `/invariant ratify <hash>`, where the hash covers the whole new set. The lock's ratification record gains `amends`, the proposal hash it replaces, so each lock names the one before it, and the git history of the lock is the full chain. A hand-built project, such as the two-phase commit ratified in D-0027, can be amended too. Its first amendment's `amends` points at the lock recorded in `decisions/log.md`.
5. **Synthesis** starts from the project as it is: the new module, with the draft model, and the current code. The prompt says the code already implements the old statements, and that the agent's job is to change it to implement the new ones. The gate checks everything the lock pins, so a change that breaks an old, unchanged statement fails, just as a new one would.
6. **Scope.** A factory pull request may change an existing project's lock only when all of these hold:
   - The new lock's ratification is for this pull request's issue. The issue is read from the branch name, `invariant/issue-<n>-…`.
   - The ratification's `amends` equals the hash of the lock on the base branch. This stops an amendment drafted against an older lock from landing after someone else's.
   - The lock hashes to the proposal that was ratified.

   CI's ratification check then confirms the comment itself, as today. Everything else about scope stays: one project, no dependencies, no cgo, no CI files.
7. **Receipts** show the chain: "ratified by @gitdek on #5, amending #3."

## Part B · Invariant builds itself: the factory's protocol

The factory's rules for an issue are a state machine:

- Asking comes before proposing, and proposing before ratifying.
- Only writers' commands count, and no bot's.
- A stale hash ratifies nothing.
- Nothing merges without a green gate on the exact head, with a lock that is the ratified proposal.

Today those rules live in `internal/factory` as ordinary Go, and the tests are the only thing checking them. They're also precisely the kind of rule Invariant exists to prove.

1. **Formalize the protocol through the factory.** An issue describes the protocol in plain language, and the factory drafts it in TLA+. Claude writes the issue from the code, so it's complete. @gitdek answers the forks and ratifies. The statements would include, for example:
   - `NoMergeWithoutGreenGate`: nothing merges unless the gate passed on the merged head.
   - `NoStaleRatification`: only the current proposal can be ratified.
   - `OnlyWritersDirect`: a command from a non-writer or a bot never changes an issue's state.
   - `NoRatifyWithOpenForks`: nothing can be ratified while a question is unanswered.
   - Known bugs such as `MergeOnRedGate` and `RatifyStaleProposal`, which the invariants must catch.
2. **The factory builds the core** as a new Go project, `factory/protocol/`, and proves it with Gobra. It's a pure function from an issue's state and an event to the next state and the action to take, and agreement ties it to the TLA+ model state for state.
3. **The watcher uses the proved core.** Claude and @gitdek wire it in with an ordinary pull request, since the factory may not touch `internal/`. The root module imports the project through a `replace` line in `go.mod`, and CI's gate covers it like any other project.
4. **From then on, the factory's rules change through the factory.** An issue that changes the protocol is an amendment (Part A) to `factory/protocol`. @gitdek re-ratifies the rules, the factory changes the proved core, and CI proves it again before the bot merges.

What stays ordinary code, changed by Claude and @gitdek with CI's tests: the GitHub client, git and Docker plumbing, the CLI, and the prompts. Those aren't state machines with a model to check against, and forcing them into one would be theater.

## Slice 6 is done when

1. Unit tests run an amendment through the whole flow against the fake GitHub and real git:
   - an issue naming a project
   - a diff in the proposal, with a removal called out
   - ratification with `amends`
   - synthesis starting from the current code
   - a merge

   They also cover the failure paths:
   - An amendment drafted against a lock that has since changed is refused at merge.
   - A lock change carrying another issue's ratification is refused.
   - A proposal can't be ratified while its target is ambiguous.
2. Live, amendment: an issue changes an existing factory project through the factory, from issue to merge, with @gitdek ratifying the diff. For example: "the rate limiter should refuse a call when more than N calls are already waiting".
3. Live, self-hosting: the factory's protocol is ratified by @gitdek on an issue, built by the factory as `factory/protocol`, and proved with Gobra. The watcher runs on it, and one issue changes the protocol through the factory.

## Decided by @gitdek

1. **How an issue names its project:** a `Project: <dir>` line in the issue. The alternatives were an argument to `/invariant solve`, or a label per project.
2. **Removing or weakening a ratified statement:** allowed, but called out in bold at the top of the proposal. Only @gitdek's ratification makes it happen. The alternative was to forbid it in amendments and require a new project.
3. **Amending hand-built projects:** allowed, with `amends` pointing at the lock recorded in the log. The alternative was to amend only projects the factory built.
4. **The first self-hosted project:** the factory's issue protocol, at `factory/protocol/`. The alternatives were the scope rules, or the proposal hash and ratification record.

## Size

- **Part A** is about the size of slice 4:
  - the formalizer's diff and the amendment prompt
  - the ratification chain
  - the scope rule
  - synthesis starting from existing code
  - tests
- **Part B** is smaller in code but needs the most care, because the TLA+ must match the real factory. Most of the work is writing an issue that describes the protocol exactly, then refactoring the watcher onto the proved core.
- Each live run takes about 30 minutes plus your decisions, as #1 and #3 did.
