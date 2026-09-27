# Invariant · Product requirements

This document says what Invariant is for, who it serves, what it has to do, and how we'll know it works. It isn't the spec. [`SPEC.md`](../SPEC.md) is the only document to build against, and it holds only what's ratified or decided. Every requirement here cites its decisions in [`decisions/log.md`](../decisions/log.md). A requirement marked **proposed** becomes buildable only once @gitdek ratifies it and it's rolled up into the spec.

**Status.** Proposed as D-0048 on 2026-09-26. A coding agent drafted it from the kickoff brief, the decision log and the first three live issues. @gitdek ratifies it by answering the [questions](#questions-for-gitdek) at the end.

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

- **Whole applications.** The factory builds small cores that are state machines, and the rest of an application calls them. It doesn't prove arbitrary existing code.
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

## The trusted base

A receipt is only as good as the code that produced it. That code is the trusted base:

- **The gate:** pins, TLC's config, witnesses and known bugs, agreement and conformance, the Gobra and Nagini runs, scope, the ratification check and the receipt. That's `internal/project`, `tla`, `tlc`, `gobra`, `conformance`, `toolchain`, `verify`, `scope` and `receipt`, `internal/factory/ratification.go`, and the `verify`, `scope` and `ratification` commands CI runs.
- **CI's workflow**, `.github/workflows/gate.yml`.
- **The watcher's merge decision, while the repository is private**, and the GitHub calls behind it, because the watcher merges instead of GitHub. `D-0033`
- **Outside Invariant:** TLC, Gobra, Nagini and Z3, the Go toolchain, Docker and GitHub.

Everything else can fail to produce a merge, but it can't produce a bad one, because the gate checks everything it makes. That covers formalizing, synthesis, the prompts and the rest of the watcher. So the trusted base stays small, and the parts of it that are state machines get proved. The merge decision is first: in slice 6 it moves into `factory/protocol`. From then on the factory can change it, but only through an amendment @gitdek ratifies, proved against statements such as `NoMergeWithoutGreenGate`. Changes to the rest of the trusted base get more scrutiny than other changes (question 2).

## Requirements

Each requirement serves one goal.

- **Built:** it works today, and it's tested.
- **Next:** it's ratified, and being built.
- **Planned:** it's ratified, for later.
- **Proposed:** this document asks for it.
- **Later:** it's wanted, but not scheduled.

### 1 · Nothing merges that people didn't agree to

- **1.1 Only writers direct the factory,** and no bot's comment directs it or ratifies anything. Built. `D-0014` `D-0036` `D-0041`
- **1.2 Ratified statements are pinned** by hash, along with everything they depend on. Nothing the factory writes can change them, or change what TLC checks. Built. `D-0002` `D-0017` `D-0026`
- **1.3 Ratification is exact.** A writer ratifies by commenting the proposal's hash on the issue, a stale hash ratifies nothing, and CI confirms the comment through GitHub's API. Built. `D-0034`
- **1.4 The gate passes only when every check does:** pins, TLC, witnesses, known bugs, agreement or conformance, proofs, the build, scope and ratification. Built. `D-0004` `D-0013` `D-0014`
- **1.5 The factory merges only when three things hold:** the gate passed on the exact head, the pull request is in scope, and its lock is the ratified proposal. Built. `D-0033` `D-0047`
- **1.6 An amendment lands only on the lock it amends.** If the base branch has moved on, the factory ratifies nothing and asks for a revision. Built. `D-0045` `D-0046`
- **1.7 The factory can't change its own gate.** A factory pull request changes one project and nothing else, and the App can't edit CI. Built. `D-0014` `D-0041`
- **1.8 Changes to the trusted base land by pull request,** and @gitdek merges them once CI passes. Proposed, question 2.
- **1.9 GitHub enforces the gate once the repository is public.** `invariant/gate` becomes a required check, and GitHub's native auto-merge replaces the watcher's merge. Planned. `D-0033`

### 2 · People decide; they don't review code

- **2.1 Issues are plain language,** and so are the questions and proposals the factory posts. Built. `D-0034` `D-0036`
- **2.2 Forks, not guesses.** When an issue allows materially different behaviors, the factory asks and waits. Built. `D-0002`
- **2.3 Checked before shown.** TLC checks a draft against a model before anyone is asked to ratify it. Built. `D-0036`
- **2.4 Amendments read as diffs.** Removed statements and changed invariants are called out in bold. Built. `D-0045` `D-0046`
- **2.5 After ratification, nothing more is asked of people** unless something fails. Synthesis, the pull request and the merge happen on their own. Built. `D-0036`
- **2.6 A failure explains itself** on the issue, and `/invariant retry` picks it up again once someone fixes the cause. Built. `D-0000` `D-0047`
- **2.7 Three languages,** chosen per issue by label. Go and Python are proved, and TypeScript is tested in every state it can reach. Built. `D-0038` `D-0039` `D-0040`

### 3 · Every claim is backed by tool output, and states its limits

- **3.1 CI writes the receipt from tool output.** The factory never writes its own. Built. `D-0000` `D-0013`
- **3.2 The receipt names its evidence:** proved, tested in every state, or tested. It names the bounds, and any function no verifier saw. Built. `D-0024` `D-0029` `D-0031`
- **3.3 Receipts reproduce.** Every verifier is pinned and runs offline, and a commit's fingerprint matches between a local run and CI. Built. `D-0013` `D-0016` `D-0032`
- **3.4 A checker's failure is never a pass.** When a checker runs out of memory or time, the gate fails, and the receipt doesn't blame the code. Built. `D-0047`
- **3.5 Public copy never claims more than the receipt.** Built. `D-0015`
- **3.6 A live view.** A dashboard shows what the factory is working on and every project's evidence, from the same sources as the receipts. It's shared through a Cloudflare tunnel for now, and moves to puglisij.com/invariant later. Next. `D-0049`
- **3.7 Code you can ship.** The code carries no model bounds. Today it does: the rate limiter's code stops at `MAX_CALLS = 5`, and its clock at `MAX_TIME = 3`, because the model needs those limits to stay finite. The limits move into what TLC is told to explore, and sizes such as capacity become parameters. Gobra's and Nagini's proofs then hold at every size, and TLC still checks the design at the ratified bounds. Proposed, slice 7.
- **3.8 Liveness.** People can ratify "eventually" properties under stated fairness, such as every waiting call eventually going out, and TLC checks them. Today the gate checks invariants and deadlock only. Proposed, slice 8, where the factory's own recovery needs it first.
- **3.9 Type checking** for TypeScript and Python, inside the sandbox. Later. `D-0030`
- **3.10 A proof path for TypeScript.** Later. `D-0024` `D-0038`

### 4 · Invariant maintains its own rules

- **4.1 The issue protocol is a proved project.** It becomes `factory/protocol`, proved with Gobra and tied to its TLA+ model state for state, and the watcher runs on it. Planned, after this PRD. `D-0045` `D-0053`
- **4.2 The factory's rules change through the factory.** A change to the protocol is an amendment that @gitdek ratifies. Planned, after this PRD. `D-0045` `D-0053`
- **4.3 The factory survives a crash at any point.** Stop the watcher anywhere and restart it, and no step is lost or done twice, whether it's a post, a push, a pull request or a merge. Proposed, slice 8.
- **4.4 Concurrent work can't lose or double a step.** Two issues changing the same project can't both land, and two watchers running at once can't both act. Today the first holds because of a check, and nothing handles the second. Proposed, slice 8.

### 5 · Fast and cheap enough to use every day

- **5.1 It runs on the owner's machine,** through the official coding-agent CLIs in their documented headless modes, on the owner's accounts. Built. `D-0028` `D-0036`
- **5.2 Effort is bounded.** Each agent run has a cost budget, a turn limit and a timeout, and synthesis gets at most four gate runs. Built. `D-0000` `D-0026`
- **5.3 The checker's memory stays flat** as models grow. Built. `D-0047`
- **5.4 The factory records its numbers:** each issue's factory time, the comments people made, agent spend and gate runs. Proposed, question 3.
- **5.5 Any coding agent.** The factory's agents run on Codex as well as Claude Code, and the working rules live in `AGENTS.md`, which any agent reads. Planned. `D-0028` `D-0052`
- **5.6 Other repositories and existing code.** `invariant init` sets up a repository, and existing-code projects check code that's already there, without changing it. It starts with copythis-ad. Next, slice 6. `D-0053` `D-0054`
- **5.7 A hosted factory** that runs on an API key. Later. `D-0028`

## How we'll know it works

Two measures must stay at zero, always. Both are zero so far.

- Pull requests merged without a green gate on their exact head.
- Locks merged that aren't exactly what a writer ratified.

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
| 6 | Amendments, then existing projects in other repositories, starting with copythis-ad (5.6) | Part A done, Part B next `D-0053` `D-0054` |
| 7 | Code you can ship (3.7) | Proposed |
| 8 | The factory survives crashes and concurrent work, proved, which brings liveness to the gate (3.8, 4.3, 4.4) | Proposed |
| 9 | Ready to go public: GitHub enforces the gate (1.9), and a review of what outside contributors could do. @gitdek chooses when. | Proposed. The switch itself is ratified. `D-0007` `D-0033` |
| Later | Codex, type checking, a TypeScript proof path, a hosted factory | Later |

- **Slice 6 is done when** an issue on copythis-ad goes from opened to merged, with the real lease code tested against rules @gitdek ratified. `D-0054`
- **Then this PRD,** and after it, Invariant builds itself: the issue protocol is ratified on an issue, built by the factory and proved with Gobra, the watcher runs on it, and one issue changes it through the factory. `D-0045` `D-0053` The protocol core runs inside the real watcher, so its model has to be finite without counters that cap its behavior. Otherwise the cap would end up in the code the watcher runs. Slice 7 makes that true for every project.
- **Slice 7 is done when** a factory project's code has no model bounds in it, its proofs don't depend on the bounds, and TLC checks it at the ratified bounds, on a live issue.
- **Slice 8 is done when** a model of the watcher shows that no step is lost or doubled, including a crash at any point and a second watcher. The factory proves the core that decides what to do after a restart, the watcher runs on it, and a test kills the watcher after each kind of step and shows the issue still merges.
- **Slice 9 is done when** `invariant/gate` is a required check, GitHub's auto-merge does the merging, and a review of the issue surface, seen as someone without write access, is written up with its findings fixed.

## Risks

- **The model is wrong.** The factory writes both the model and the code, so a proof shows only that they agree. People ratify the statements, not the model. Witnesses show the model does something, known bugs show the statements can catch mistakes, and agreement or conformance ties the code to the model's states.
- **Rubber-stamp ratification.** A proposal nobody reads is a guess with extra steps. Proposals lead with plain language, amendments are diffs with any loosening called out in bold, and nothing is shown until TLC has checked it.
- **Small bounds.** TLC is exhaustive only within the bounds, and a bug can hide at larger sizes. Receipts always state the bounds, and slice 7 makes the proofs hold at every size.
- **Checker limits.** Big models can run out of memory or CI time, as #6 did. That fails the gate, never passes it, and the checker's memory now stays flat.
- **Issue text as an attack.** An issue's text goes into an agent's prompt. Only writers can start the factory. Agents have no network, no secrets and no GitHub access. What they write is checked by TLC and shown to people before it counts, and then it's gated. Going public widens who can write issues, so slice 9 reviews it.
- **Agent terms and cost.** The agents run on the owner's accounts, only through official CLIs in their documented headless modes, with a budget on every run. `D-0028`
- **A drifting trusted base.** Changes to the gate land as fast as any other change. Question 2 adds review.

## Questions for @gitdek

Answering these ratifies this document as D-0048.

1. **In what order should slices 7 to 9 come?** Recommended: code you can ship, then crash and concurrency safety, then going public. The factory's code today stops after five calls, which nobody would ship, and it's the first thing a skeptical engineer will spot. Crash safety matters most once people rely on the factory. The alternatives are crash safety first, or going public first.
2. **How should changes to the trusted base land?** Recommended: by pull request, and @gitdek merges once CI passes. Everything else still goes straight to main. The alternative is straight to main for everything, as now.
3. **Should the factory record its numbers?** Recommended: yes. Its merge comment reports the issue's factory time, the comments people made, agent spend and gate runs, and keeps them in its hidden marker. The measures above and the portfolio's graphics then come from real runs. The alternative is working them out from logs by hand.
4. **Are the goals, the non-goals and the split of who builds what right, as written?**
