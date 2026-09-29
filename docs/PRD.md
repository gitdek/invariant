# Invariant · Product requirements

This document says what Invariant is for, who it serves, what it has to do, and how we'll know it works. It isn't the spec. [`SPEC.md`](../SPEC.md) is the only document to build against, and it holds only what's ratified or decided. Every requirement here cites its decisions in [`decisions/log.md`](../decisions/log.md). A requirement marked **proposed** becomes buildable only once @gitdek ratifies it and it's rolled up into the spec.

**Status.** Ratified as D-0048 by @gitdek on 2026-09-26, with the four recommendations in [Decided by @gitdek](#decided-by-gitdek). A coding agent drafted it from the kickoff brief, the decision log and the first three live issues.

## The problem

AI coding agents write code faster than people can review it, and they don't ask clarifying questions. When a request is ambiguous, an agent picks an interpretation and ships it with total confidence. Tests catch some mistakes, but only the ones someone thought to test for, and review doesn't scale with the agent.

Formal verification alone doesn't fix this. A proof shows that code matches a property. If the agent wrote the property too, the proof certifies the agent's guess.

## The product

Invariant is a code factory that turns GitHub issues into merged pull requests. It splits the work where it belongs: people decide what must be true, and Invariant proves that the code satisfies it.

1. Someone opens an issue in plain language.
2. The factory drafts formal statements: invariants, the bounds they're checked within, witnesses that show the system does something, and known bugs the invariants must catch. Where the issue allows materially different behaviors, it asks instead of guessing.
3. TLC checks the draft against a model before anyone sees it.
4. A person with write access ratifies the statements by hash, on the issue.
5. The factory writes the code, and repairs it against counterexamples until the gate passes.
6. CI runs the gate again on the exact commit, and the factory merges.

## Who it's for

- **The owner** decides what must be true, and wants to spend minutes on each decision, not hours reviewing diffs. Today, that's @gitdek.
- **Whoever files the issue** describes the behavior they need in plain language, and answers questions about it.
- **Whoever reads the pull request later** needs to know exactly what was checked, how, and within what bounds, without taking anyone's word for it. That includes the engineers and leaders who read about Invariant on puglisij.com.

## Goals

1. **Nothing merges that people didn't agree to.** Every change the factory merges is exactly what a person with write access ratified, and it passed the gate on the commit that merged.
2. **People decide; they don't review code.** On a typical issue, a person answers the forks and ratifies. The factory does the rest.
3. **Every claim is backed by tool output, and states its limits.** A receipt says what kind of evidence it is and the bounds it holds within, and nothing claims more.
4. **Invariant maintains its own rules.** The rules that make Invariant trustworthy are proved by Invariant, and they change only through amendments a person ratifies.
5. **Fast and cheap enough to use every day.** An issue goes from opened to merged in minutes of factory time, for a few dollars at most.

## Non-goals, for now

- **Proving whole applications.** The factory proves small cores that are state machines, and the rest of an application calls them. From slice 13 it builds the rest too, tested rather than proved (D-0105). It doesn't prove arbitrary existing code.
- **A hosted service.** The factory runs on the owner's machine, with the owner's accounts. A hosted factory would use an API key. `D-0028`
- **Claims beyond the checked bounds.** No "100%", and no "bug-free". `D-0015`
- **Deciding what to build.** The factory never makes a call a person should make. `D-0002`
- **Lean, Rust and C++**, from the kickoff brief. `D-0003` `D-0010` `D-0023`

## Who builds what

Invariant can't build all of itself, and it shouldn't. The factory builds only what it can check against a ratified model, and most of Invariant isn't a state machine. It's plumbing around GitHub, git, Docker and the verifiers, and forcing that into a model would be theater. `D-0045` So the work splits three ways.

| Who | What | Checked by |
| :-- | :-- | :-- |
| **@gitdek** | Decides. Ratifies every requirement, answers every fork, and ratifies every statement. | |
| **Invariant** | The rules that make it trustworthy, as proved projects under `factory/`. Its issue protocol comes first, then how it survives crashes and concurrent work. It changes them only through amendments @gitdek ratifies. | Its own gate: TLC, Gobra and agreement, again in CI |
| **Coding agents** | Everything else: the gate's machinery, the verifier integrations, the GitHub and git plumbing, the prompts, the CLI, the docs and the graphics. Any coding agent builds it, in sessions with @gitdek, and each decision is logged as it's made. | CI: vet, unit and integration tests, and the gate on every project |

Once a requirement is ratified, it becomes code in one of two ways. If it's a rule with a model, an agent writes an issue that describes it completely, @gitdek answers the forks and ratifies, and the factory builds and proves it. Anything else, an agent builds in a session with @gitdek.

That split is the ideal, not a compromise. Proofs go where the rules are, tests go where the plumbing is, and a person makes every decision.

From slice 13, the factory runs both. A plumbing issue goes through the same flow as a modeled one, with a plan and tests where statements and a proof don't fit, and a PRD becomes a plan of issues a person ratifies. Coding agents start every change as an issue, and build by hand only what the factory can't take yet. `D-0105`

## The trusted base

A receipt is only as good as the code that produced it. That code is the trusted base:

- **The gate:** pins, TLC's config, witnesses and known bugs, agreement and conformance, the Gobra and Nagini runs, scope, the ratification check and the receipt. That's `internal/project`, `tla`, `tlc`, `gobra`, `conformance`, `toolchain`, `verify`, `scope` and `receipt`, `internal/factory/ratification.go`, and the `verify`, `scope` and `ratification` commands CI runs.
- **CI's workflow**, `.github/workflows/gate.yml`.
- **The watcher's merge decision, while the repository is private**, and the GitHub calls behind it, because the watcher merges instead of GitHub. `D-0033`
- **Outside Invariant:** TLC, Gobra, Nagini and Z3, the Go toolchain, Docker and GitHub.

Everything else can fail to produce a merge, but it can't produce a bad one, because the gate checks everything it makes. That covers formalizing, synthesis, the prompts and the rest of the watcher. So the trusted base stays small, and the parts of it that are state machines get proved. The merge decision is first: in slice 7 it moves into `factory/protocol`. From then on the factory can change it, but only through an amendment @gitdek ratifies, proved against statements such as `NoMergeWithoutGreenGate`. Changes to the rest of the trusted base land by pull request, and @gitdek merges them (D-0048).

## Requirements

Each requirement serves one goal.

- **Built:** it works today, and it's tested.
- **Next:** it's ratified, and being built.
- **Planned:** it's ratified, for later.
- **Later:** it's wanted, but not scheduled.

### 1 · Nothing merges that people didn't agree to

- **1.1 Only writers direct the factory,** and no bot's comment directs it or ratifies anything. Built. `D-0014` `D-0036` `D-0041`
- **1.2 Ratified statements are pinned** by hash, along with everything they depend on. Nothing the factory writes can change them, or change what TLC checks. Built. `D-0002` `D-0017` `D-0026`
- **1.3 Ratification is exact.** A writer ratifies by commenting the proposal's hash on the issue, a stale hash ratifies nothing, and CI confirms the comment through GitHub's API. Built. `D-0034`
- **1.4 The gate passes only when every check does:** pins, TLC, witnesses, known bugs, agreement or conformance, proofs, the build, scope and ratification. Built. `D-0004` `D-0013` `D-0014`
- **1.5 The factory merges only when three things hold:** the gate passed on the exact head, the pull request is in scope, and its lock is the ratified proposal. Built. `D-0033` `D-0047`
- **1.6 An amendment lands only on the lock it amends.** If the base branch has moved on, the factory ratifies nothing and asks for a revision. Built. `D-0045` `D-0046`
- **1.7 The factory can't change its own gate.** A factory pull request changes one project and nothing else, and the App can't edit CI. Built. `D-0014` `D-0041`
- **1.8 Changes to the trusted base land by pull request,** and @gitdek merges them once CI passes. In force. `D-0048`
- **1.9 GitHub enforces the gate.** `main` requires `invariant/gate` and a pull request for every change, so GitHub refuses any merge the gate didn't pass, the owner's included. The factory keeps merging its own proved way, instead of handing merges to GitHub's auto-merge. Built, slice 10. `D-0033` `D-0075`

### 2 · People decide; they don't review code

- **2.1 Issues are plain language,** and so are the questions and proposals the factory posts. Built. `D-0034` `D-0036`
- **2.2 Forks, not guesses.** When an issue allows materially different behaviors, the factory asks and waits. Built. `D-0002`
- **2.3 Checked before shown.** TLC checks a draft against a model before anyone is asked to ratify it. Built. `D-0036`
- **2.4 Amendments read as diffs.** Removed statements and changed invariants are called out in bold. Built. `D-0045` `D-0046`
- **2.5 After ratification, nothing more is asked of people** unless something fails. Synthesis, the pull request and the merge happen on their own. Built. `D-0036`
- **2.6 A failure explains itself** on the issue, and `/invariant retry` picks it up again once someone fixes the cause. Built. `D-0000` `D-0047`
- **2.7 Three languages,** chosen per issue by label. Go and Python are proved, and TypeScript is tested in every state it can reach. Built. `D-0038` `D-0039` `D-0040`
- **2.8 Every change goes through the factory, and so can a whole PRD.** A change that isn't a state machine is a plumbing issue: its proposal is a plan with acceptance tests, which a person ratifies by hash, and its checks are CI's tests, the decision graph and a second agent's review. Its pull request changes only the files the plan names, and only a person merges a change to the trusted base. `/invariant plan` turns a PRD into a plan of at most 10 issues, which a person ratifies, and the factory works them one at a time. Slice 13. Plumbing issues and PRD plans are built: each poll, the factory works the plans `/invariant plan` ratifies. A first real PRD end to end is next. `D-0105` `D-0107` `D-0108` `D-0109`

### 3 · Every claim is backed by tool output, and states its limits

- **3.1 CI writes the receipt from tool output.** The factory never writes its own. Built. `D-0000` `D-0013`
- **3.2 The receipt names its evidence:** proved, tested in every state, or tested. It names the bounds, and any function no verifier saw. Built. `D-0024` `D-0029` `D-0031`
- **3.3 Receipts reproduce.** Every verifier is pinned and runs offline, and a commit's fingerprint matches between a local run and CI. Built. `D-0013` `D-0016` `D-0032`
- **3.4 A checker's failure is never a pass.** When a checker runs out of memory or time, the gate fails, and the receipt doesn't blame the code. Built. `D-0047`
- **3.5 Public copy never claims more than the receipt.** Built. `D-0015`
- **3.6 A live view.** A dashboard shows what the factory is working on and every project's evidence, from the same sources as the receipts, and what's waiting on a person. It's shared through a Cloudflare tunnel for now, and moves to puglisij.com/invariant later. Built. `D-0049` `D-0050` `D-0060`
- **3.7 Code you can ship.** The code carries no model bounds: it's the system alone, with sizes as parameters, and the explorer or driver keeps the environment at the bounds. Gobra and Nagini prove it at every size, and TLC checks the design at the ratified bounds. The gate also checks the code one size past the bounds, and fails code that hardcodes a size: a Go explorer runs again, and a TypeScript or Python driver counts. Built, slice 8: the factory asks for code this way in all three languages, and #18 did it live in Go. #28 took the rate limiter's bounds out of its code through the factory (#30). Other existing projects keep theirs until an issue asks. `D-0048` `D-0058` `D-0068`
- **3.11 A driver can't skip a step.** A conformance driver is evidence only if it tries every step a person or worker could. On copythis-ad#33, the factory's driver skipped the one step the ratified rule was about, and the gate passed anyway (D-0059). The gate records every attempt a driver makes, and works out with TLC which steps only a bound rules out in each state, so it can check that every other step was tried. A second agent, with fresh context, also reads each driver for steps it skips or states it never records, and the factory posts its review with the pull request. Built in slice 11 for TypeScript, Python and Go: the factory rebuilt all twelve projects here, and each receipt shows every step tried, 3,626,031 attempts in all. `D-0059` `D-0082` `D-0085` `D-0086` `D-0087` `D-0088` `D-0090`
- **3.12 One decision graph for every project.** A decision is worth only as much as it can be found. Every project's decisions live in one embedded store, with typed links to the decisions they refine or supersede, the SPEC lines that rest on them and the code that implements them. Agents ask it what depends on a decision and what reopening one would touch, and CI checks that nothing cites a decision that doesn't exist or no longer holds. Slice 12. `D-0096`
- **3.8 Liveness.** People can ratify "eventually" properties under stated fairness, such as every waiting call eventually going out, and TLC checks them. Each property is checked alone, under the ratified fairness, and each fairness statement must be about a step the model takes, so no property holds vacuously. A known bug can expect a property. Built in slice 9, where the factory's recovery model is the first to use it: both its properties hold under its five fairness statements. `D-0048` `D-0058` `D-0069` `D-0071`
- **3.9 Type checking** for TypeScript and Python, inside the sandbox. Later. `D-0030`
- **3.10 A proof path for TypeScript.** Later. `D-0024` `D-0038`

### 4 · Invariant maintains its own rules

- **4.1 The issue protocol is a proved project.** It becomes `factory/protocol`, proved with Gobra and tied to its TLA+ model state for state, and the watcher runs on it. Built, slice 7. `D-0045` `D-0053` `D-0058`
- **4.2 The factory's rules change through the factory.** A change to the protocol is an amendment that @gitdek ratifies. Built, slice 7. `D-0045` `D-0053` `D-0058`
- **4.3 The factory survives a crash at any point.** Stop the watcher anywhere and restart it, and no step is lost or done twice, whether it's a post, a push, a pull request or a merge. Built, slice 9: runs and merges are recorded before they happen, every effect is looked for before it's taken, and the proved recovery core, `factory/recovery`, allows each one. A test stops the watcher before each of its effects, on the same machine or another. `D-0048` `D-0058` `D-0069` `D-0073` `D-0074`
- **4.4 Concurrent work can't lose or double a step.** Two issues changing the same project can't both land, and two watchers running at once can't both act. The first holds because of a check. For the second, one watcher at a time holds a lease in a Git ref, and checks it before every effect. Built, slice 9. Two watchers ran on this repository for a day: GitHub refused the second one's only push for the lease, and it read the first's next 563 renewals without acting. `D-0048` `D-0058` `D-0069` `D-0072`

### 5 · Fast and cheap enough to use every day

- **5.1 It runs on the owner's machine,** through the official coding-agent CLIs in their documented headless modes, on the owner's accounts. Built. `D-0028` `D-0036`
- **5.2 Effort is bounded.** Each agent run has a cost budget, a turn limit and a timeout, and synthesis gets at most four gate runs. Built. `D-0000` `D-0026`
- **5.3 The checker's memory stays flat** as models grow. Built. `D-0047`
- **5.4 The factory records its numbers:** each issue's factory time, the comments people made, agent spend and gate runs, in its merge comment and its hidden marker. Built. `D-0048`
- **5.5 Any coding agent.** The factory's agents run on Codex as well as Claude Code, and the working rules live in `AGENTS.md`, which any agent reads. In progress: an issue picks its coding agent with an `Agent:` line, else its project's manifest does, and the draft settles it (#173). Codex itself comes next. `D-0028` `D-0052` `D-0130`
- **5.6 Other repositories and existing code.** `invariant init` sets up a repository, and existing-code projects check code that's already there, without changing it. Built: copythis-ad's lease protocol is checked on every pull request, and the check caught a real bug. `D-0053` `D-0054` `D-0059`
- **5.7 A hosted factory** that runs on an API key. Later. `D-0028`

## How we'll know it works

Two measures must stay at zero, always. The dashboard counts both for the factory's merges, and both are zero so far.

- Pull requests merged without a green gate on their exact head.
- Locks merged that aren't exactly what a writer ratified.

One pull request that changed CI itself, #34, merged without the gate. The account's Actions minutes had run out, and @gitdek chose to merge it once the whole suite passed locally (D-0079).

The rest have targets. Here are the first three live issues:

| Measure | Target | #1, Go | #3, TypeScript | #5, an amendment |
| :-- | :-- | :-- | :-- | :-- |
| Comments from people | 3 or fewer | 2 | 2 | 2 |
| People only decided | 9 issues in 10 | yes | yes | no: CI's checker ran out of memory, and it had to be fixed |
| Factory time, not counting time waiting on people | under 20 minutes | 16 minutes | 13 minutes | 11 minutes |
| Synthesis passed its first gate run | 8 issues in 10 | yes | yes | yes |
| Estimated agent spend | under $2 | synthesis $0.26, the rest not recorded | synthesis $0.23, the rest not recorded | $0.64 |

Most factory time is CI, about ten minutes a run. Agent spend wasn't recorded per issue until #5, which is why 5.4 has the factory record its own numbers.

## Roadmap

Slices ship in order. Each one's plan and acceptance criteria are ratified before it's built, as D-0036, D-0043 and D-0045 were.

| Slice | What | Status |
| :-- | :-- | :-- |
| 1 to 5 | The gate, synthesis, TypeScript and Python checking, GitHub, and synthesis in all three languages | Done |
| 6 | Amendments, then existing projects in other repositories, starting with copythis-ad (5.6) | Done `D-0053` `D-0054` |
| 7 | Invariant builds itself: the issue protocol as `factory/protocol` (4.1, 4.2) | Done `D-0045` `D-0053` `D-0058` |
| 8 | Code you can ship (3.7) | Done `D-0048` `D-0058` `D-0068` |
| 9 | The factory survives crashes and concurrent work, proved, which brings liveness to the gate (3.8, 4.3, 4.4) | Done `D-0048` `D-0058` `D-0069` |
| 10 | Ready to go public: GitHub enforces the gate (1.9), and a review of what outside contributors could do. @gitdek chooses when. | Done `D-0007` `D-0033` `D-0048` `D-0075` |
| 11 | A driver can't skip a step (3.11) | Done `D-0059` `D-0082` `D-0090` |
| 12 | One decision graph for every project (3.12) | Done `D-0096` |
| 13 | The factory takes every change, and a whole PRD (2.8) | Paused `D-0105` `D-0131` |
| 14 | Codex on chosen projects (5.5): a project picks its coding agent, and the factory runs Codex under the same rules as Claude Code | In progress `D-0129` |
| Later | Type checking, a TypeScript proof path, a hosted factory | Later |

- **Slice 6 is done when** an issue on copythis-ad goes from opened to merged, with the real lease code tested against rules @gitdek ratified. `D-0054`
- **Slice 7 is done when** the issue protocol is ratified on an issue, built by the factory and proved with Gobra, the watcher runs on it, and one issue changes it through the factory. `D-0045` `D-0053` The protocol core runs inside the real watcher, so its model has to be finite without counters that cap its behavior. Otherwise the cap would end up in the code the watcher runs. Slice 8 makes that true for every project.
- **Slice 8 is done when** a factory project's code has no model bounds in it, its proofs don't depend on the bounds, and TLC checks it at the ratified bounds, on a live issue.
- **Slice 9 is done when** a model of the watcher shows that no step is lost or doubled, including a crash at any point and a second watcher. The factory proves the core that decides what to do after a restart, the watcher runs on it, and a test kills the watcher after each kind of step and shows the issue still merges.
- **Slice 10 is done when** `invariant/gate` is a required check, GitHub's auto-merge does the merging, and a review of the issue surface, seen as someone without write access, is written up with its findings fixed.
- **Slice 12 is done when** every decision in this repository and copythis-ad is in the store, each repository's journal rebuilds it exactly, an agent answers what depends on a decision through the tools, and CI fails a pull request that cites a decision that doesn't exist or has been superseded. `D-0096`
- **Slice 13 is done when** a plumbing issue goes from opened to merged through the factory, a trusted-base change it built waits for a person's merge, a PRD with at least three issues becomes a ratified plan the factory works through, and the dashboard's graph and copythis-ad's decisions land that way. `D-0105`

## Risks

- **The model is wrong.** The factory writes both the model and the code, so a proof shows only that they agree. People ratify the statements, not the model. Witnesses show the model does something, known bugs show the statements can catch mistakes, and agreement or conformance ties the code to the model's states.
- **Rubber-stamp ratification.** A proposal nobody reads is a guess with extra steps. Proposals lead with plain language, amendments are diffs with any loosening called out in bold, and nothing is shown until TLC has checked it.
- **Small bounds.** TLC is exhaustive only within the bounds, and a bug can hide at larger sizes. Receipts always state the bounds, and slice 8 makes the proofs hold at every size.
- **Checker limits.** Big models can run out of memory or CI time, as #6 did. That fails the gate, never passes it, and the checker's memory now stays flat.
- **Issue text as an attack.** An issue's text goes into an agent's prompt. Only writers can start the factory. Agents have no network, no secrets and no GitHub access. What they write is checked by TLC and shown to people before it counts, and then it's gated. Going public widens who can write issues, so slice 10 reviews it.
- **Agent terms and cost.** The agents run on the owner's accounts, only through official CLIs in their documented headless modes, with a budget on every run. `D-0028`
- **A drifting trusted base.** Changes to the gate land as fast as any other change. Question 2 adds review.

## Decided by @gitdek

@gitdek ratified this document with the four recommendations it asked about.

1. **The order after self-hosting:** code you can ship, then crash and concurrency safety, then going public. D-0053 had already put self-hosting first, right after this PRD, so the slices are 7 to 10 (D-0058). The alternatives were crash safety first, or going public first.
2. **Changes to the trusted base** land by pull request, and @gitdek merges them once CI passes. Everything else can go straight to main. The alternative was straight to main for everything.
3. **The factory records its numbers.** Its merge comment reports the issue's factory time, the comments people made, agent spend and gate runs, and its hidden marker keeps them. The alternative was working them out from logs by hand.
4. **The goals, the non-goals and the split of who builds what** stand as written.
