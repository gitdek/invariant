# Invariant · Spec

This file describes the current state only. It's rolled up from the ratified and decided decisions in the [decision journal](decisions/journal/), whose table is [`decisions/log.md`](decisions/log.md), and every line cites the decisions behind it. A line that no ratified or decided decision supports is a bug in this file. History and reasoning live in the journal and the decisions' records.

**Rolled up through** D-0058 · 2026-09-26 (every entry is ratified or decided)

## What Invariant is

A code factory that turns GitHub issues into merged pull requests. The code in each pull request is proved against formal statements that people have ratified. `D-0000` `D-0002`

## Principles

- **People decide what must be true, and the factory proves that it is.** The factory drafts formal statements, and a person ratifies them before the factory builds against them. Ratified statements are pinned by hash, and the factory cannot change them. Changing one takes a new decision. `D-0002`
- **The factory doesn't guess.** When it can't formalize an issue without choosing between interpretations, it posts the fork on the issue as a decision request and does not proceed on that fork. `D-0002`
- **Proofs where the rules are, tests where the plumbing is, and a person makes every decision.** People decide every requirement, fork and statement. Invariant proves the rules that make it trustworthy, as projects under `factory/`. Coding agents build everything else, tested by CI. `D-0048`
- **A small trusted base.** A bug outside the gate, CI and the merge decision can stop a merge, but it can't produce a bad one, because the gate checks everything the factory makes. Changes to the trusted base land by pull request, and @gitdek merges them once CI passes. `D-0048`
- **Checks can't be quietly weakened.** The gate writes TLC's config itself from what was ratified. Reachability witnesses show the invariants don't hold only because the model does nothing. Known bugs show the invariants are strong enough to catch real mistakes. `D-0011` `D-0013` `D-0017`

## What gets ratified

- Each project's `.invariant/ratified.lock` records five things, and two more when something must eventually happen. `D-0011` `D-0013` `D-0026` `D-0071`
  - The spec, `Init /\ [][Next]_vars`.
  - The invariants.
  - The bounds TLC checks them within.
  - The reachability witnesses.
  - The known bugs, as TLA+ actions the invariants, or a property, must catch.
  - The properties: temporal formulas every behavior must satisfy, such as every command eventually being answered.
  - The fairness they assume: `WF_vars(A)` or `SF_vars(A)`, one condition each, about the system's own steps. The spec never states fairness itself.
- Each pin covers a statement and every definition it depends on, stopping at the factory's `Init` and `Next`. So a fairness statement pins the action it names, and the model must keep that action a part of `Next`. `D-0026` `D-0071`
- The factory owns the model (`Init`, the actions and `Next`), the implementation and its contracts. Each contract restates one TLA+ action. `D-0011` `D-0026` `D-0031`
- A person records a ratification with `invariant pin`. `D-0017`

## System

- Invariant is written in Go. `D-0001`
- Invariant targets Go, TypeScript and Python, the languages of @gitdek's projects. `D-0023`
- Every language gets a proof path and a conformance path, and each receipt says which one ran. Proofs fit new code written for a verifier; existing code is tested against the model. `D-0024`
  - Go: proved with Gobra, and tied to the model by agreement. An explorer with `Try` is also tested against the model by conformance, attempt by attempt. `D-0003` `D-0026` `D-0090`
  - Python: new cores are proved with Nagini, in files marked `# +nagini`, and still tested against the model by conformance. Existing code is tested by conformance alone. `D-0024` `D-0029` `D-0031`
  - TypeScript: tested against the model by conformance. No proof path yet. `D-0024` `D-0029`
- The factory writes all three languages. Go is proved with Gobra, and Python is a core proved with Nagini. TypeScript is a state machine tested against the model in every state it can reach. An issue picks its language with an `invariant:typescript`, `invariant:python` or `invariant:go` label, and without one, the repository's default applies. `D-0038` `D-0039` `D-0040` `D-0043`
- An existing-code project checks code that was already in its repository, without holding it or changing it. An issue names the code with `Code:` lines, and the project holds only the model and a conformance driver the factory writes. The gate runs the driver on the real code, in a throwaway copy of its package, and requires it to import every piece of code it checks. The receipt hashes that code. It's TypeScript only, for now. `D-0054` `D-0055`
- When a project's conformance driver explores every state the code can reach, its manifest says so, the driver is built on Invariant's harness, which always does, or it's a Go explorer with `Try`, which the gate's own harness explores. The gate then requires the driver to visit every state the model reaches, so the code and the model reach exactly the same states. `D-0043` `D-0088` `D-0090`
  - The factory builds those drivers on Invariant's harness, `invariant-explore.ts` or `invariant_explore.py`, which tries every step the model's `Next` names, with every argument, in every state, and records each attempt, refusals included. A step returns nothing only where the environment's bounds rule it out. The gate writes its own copy of the harness in before a driver runs, so the one that records is always Invariant's. The agent that writes the code names the sizes it takes as parameters in the manifest, the one field of it the agent may write. `D-0082` `D-0086`
  - A Go explorer gives `Init`, `Try` and `Abstract`. `Try` tries every step the model's `Next` names, with every argument, in the state it's given, and reports each with the state the code reached, which is the same state when the code refuses. It leaves a step out only where the environment's bounds rule it out. `Abstract` reads a state in the spec's vocabulary. The gate writes its own test into the package, which explores from `Init` through `Try`, breadth first, and records every attempt, and TLC checks them as it does a driver's. The code's operations refuse by themselves, with contracts that say both outcomes, rather than relying on `requires` lines the explorer guards. An explorer with only `Successors` keeps the agreement check, and its receipt says its steps weren't checked. `D-0082` `D-0090`
  - Once synthesis passes the gate, a second agent with fresh context reads the driver, or a Go explorer, in a copy of the project, with tools that only read. It looks for steps the driver skips, states it doesn't read back from the code, steps of `Next` it never tries, and sizes the code takes that the manifest doesn't name. For a driver that samples runs, its review is the check of the driver's steps. The factory posts the review with the pull request, as an opinion the receipt doesn't count, and its cost counts in the build's spend. A review that fails to run takes nothing from the build. `D-0082` `D-0087`
- Conformance: a project's driver calls the code's operations at random and records each state in the spec's vocabulary. TLC checks that every run starts in a state `Init` allows and that every step that changes the state is a `Next` step. Runs use a fixed seed, so receipts reproduce. TLC checks the steps in batches, so memory stays flat as models grow. `D-0029` `D-0047`
- Code is the system alone, with sizes as parameters and no constant from the bounds. The explorer (Go) or driver (TypeScript and Python) is the environment: it makes the code at the ratified bounds, and keeps the model's environment and history. Gobra and Nagini prove each operation at every size. `D-0068`
- Designs are model-checked with TLA+ and TLC. Go code is verified with Gobra, and Python cores with Nagini. Lean 4 has no role in v1. `D-0003` `D-0010` `D-0031`
- Verifiers run in Docker and are pinned. `D-0000` `D-0013` `D-0016` `D-0020`
  - TLC is v1.7.4, pinned by SHA-256, running in `eclipse-temurin` pinned by digest.
  - Gobra is pinned by digest, and it also checks integer overflow.
  - Builds, tests and exploration run in `golang:1.27-alpine`, pinned by digest, on a throwaway copy.
  - TypeScript runs in `node:24-alpine` and Python in `python:3.13-alpine`, both pinned by digest and limited to the standard library. Type checking isn't run yet. `D-0030`
  - Nagini 1.3.1 runs in an image built from a recipe inside Invariant, for linux/amd64 only. Every input is pinned: the base by digest, the Java runtime by copying it from TLC's Temurin image, and every Python package by wheel hash. Receipts name the recipe by its hash. `D-0031` `D-0032`
  - Existing TypeScript code runs in an image built from its package's own lockfile, starting from `node:24-bookworm-slim`, pinned by digest. Only that build has network. Receipts name its recipe. `D-0054` `D-0055`
  - Nothing runs with network access or with the host's environment.
- Invariant converts TLC counterexamples into JSON traces. `D-0016`
- Each project is its own Go module. `D-0020`
- Synthesis backends are pluggable: direct model APIs, or headless coding agents such as Claude Code and Codex. `D-0000`
- Invariant is agent-agnostic. It works with Codex as well as Claude Code, its product and docs name roles rather than an agent, and its working rules live in `AGENTS.md`, which `CLAUDE.md` imports. The factory's agents run on Claude Code today, and a Codex backend is planned. `D-0028` `D-0052`
- Synthesis runs locally through the official coding-agent CLIs in their documented headless modes, on @gitdek's own accounts. Anything shared, hosted or run in CI uses an API key. `D-0028`
- A synthesis agent starts from a skeleton that holds only the pinned definitions. It works outside the repository, with file tools and the gate as its only tools. Every gate run checks a project assembled from the original lock, manifest, request and `go.mod`, plus the agent's model and code. `D-0026`
- The agent gets at most four gate runs: one attempt and three repairs. Each run is capped by an estimated-cost budget, a turn limit and a timeout. `D-0000` `D-0026`
- The factory runs as `invariant watch` on @gitdek's machine. It polls GitHub through the `gh` CLI with @gitdek's login, and runs its agents with Claude Code on @gitdek's account. `D-0028` `D-0036`
  - It takes an issue when a writer opens it with a `/invariant solve` line, gives it the `invariant` label, or comments `/invariant solve`. `repository_dispatch` waits for a hosted factory. `D-0000` `D-0036`
  - Only people with write access can direct it: `/invariant solve`, `choose`, `revise`, `ratify` and `retry`. Everyone else is ignored. `retry` has the factory look again at a pull request that failed, once someone has fixed the cause. `D-0014` `D-0036` `D-0047`
  - It keeps no state of its own. Each of its comments records the issue's state in a hidden marker, and a status label shows it. `D-0036`
  - The agents never touch GitHub. Only the factory's own code posts, pushes and merges. Synthesis runs with no secrets and no network. `D-0014` `D-0036`
  - One watcher acts on a repository at a time. It holds a lease in the ref `refs/invariant/lease`: a commit saying who holds it and until when. Taking or renewing the lease pushes a new commit in place of the one the watcher read, and git refuses the push if another watcher moved the ref first. The holder checks it every poll, agent runs included, and renews it once half of it has gone. It stops acting a minute before it runs out by its own clock, and checks it before every post, label, push, pull request, merge and agent run. Another watcher takes it only a minute after it runs out, by its own clock. A lease lasts five minutes unless `-lease` says otherwise. `D-0069` `D-0072`
  - Every effect happens once, across crashes and watchers (D-0069). Each agent run is recorded before it starts, in a ref under `refs/invariant/runs/`, created atomically, and its result goes in the same ref when it finishes, where any watcher can read it. A run recorded with no result stopped partway: the factory says so on the issue instead of paying for another, and only a writer's command starts a new one. A build's result is the commit of its code, which the factory pushes unless the branch already has it. It opens the pull request unless one is already open from the branch. A merge is recorded before it happens, with the head it merges, so a merge the factory made before it stopped is still recorded as the factory's. A test stops the watcher before each of its effects, on the same machine or another, and the issue still merges with every effect done once. `D-0069` `D-0073`
  - It takes every effect only as its recovery core allows: `factory/recovery`, which #23 ratified and the factory built as #25, proved at every size. Before each effect, it describes the step in the core's terms, from what every watcher can see and whether it holds the lease. The core knows each step's effects, and the protocol still decides the steps. A CI failure, a closed pull request, a merge by someone else and the limit on stopped builds are the protocol's alone, since the core's model leaves them out. `D-0069` `D-0074`
  - It runs on its own proved protocol, `factory/protocol`. Before each step it takes on an issue, it maps the issue onto the model, as it is and as the step would leave it, and takes the step only if the model has it. A refused step labels the issue for a person. `D-0045` `D-0064`
  - Agents get only the file tools and Invariant's own tool, and they can't read the home directory. `D-0037`
  - The factory acts as its GitHub App's bot, `invariant-code-factory[bot]` (App ID 5079269), installed on gitdek/invariant only. It comments, pushes and merges with the App's token. Only the bot's comments count as its posts, and no bot's comment directs it or ratifies anything. Run without `-app-id`, it posts as @gitdek. `D-0041` `D-0044`
- Formalization: an agent drafts the statements, their bounds and a draft model for an issue. When the issue allows materially different behaviors, it lists them as forks in a decision request instead of choosing. The factory posts a proposal only after TLC has checked it against the draft model. `D-0002` `D-0036`
  - A writer's later comment can change a fork they decided, and a revise follows it. The draft names each change, and the proposal records it as decided, citing the comment, only if the fork was decided, the option is one of its own, and the person is a writer who commented. `D-0094`
- Ratification happens on the issue. The proposal shows each statement in plain language and TLA+, with the hash of the whole proposal. A writer ratifies by commenting `/invariant ratify <hash>`. The lock records who ratified it, where, and what. `D-0034`
- Each ratified issue becomes a new project under `examples/`. Its ratification is the first commit on the issue's branch, and synthesis starts from the drafted model. `D-0033` `D-0036`
- An issue can name its project with a `Project: <dir>` line. `D-0045` `D-0046`
  - If a project exists there, the issue is an amendment. The factory drafts the whole new set of statements from the project as it stands. The proposal shows what's added, changed, removed and unchanged, and calls out removals and changed invariants in bold.
  - The ratification records the lock it amends and where that lock was ratified. Before committing it, the factory checks that the base branch still holds that lock.
  - Synthesis changes the existing code instead of rewriting it.
  - If no project exists there, a new project goes there.
- Each piece of work happens on a branch named `invariant/issue-<id>-<slug>`. Formal artifacts live under `.invariant/specs/`. `D-0000`
- When a check fails, the counterexamples and verifier errors feed back into synthesis for up to three repair attempts. If it still fails, the PR is labeled `invariant:human-review-needed` and the counterexample is posted on the issue. `D-0000`
- Every pull request carries a receipt that CI generates from tool output only. `D-0000` `D-0013`
  - It lists the states explored, the bounds, the functions verified, the statement hashes and the tool versions.
  - It says Go code is proved at every size only when Gobra proved it and it agrees with TLC one size past the bounds. Otherwise it says what was checked, and at which bounds. `D-0015` `D-0068` `D-0070`
  - Its fingerprint matches between a local run and a CI run of the same commit.
- A live dashboard shows what the factory is working on, each project's evidence, the roadmap and the decisions. `invariant dashboard` serves it from @gitdek's machine, and a Cloudflare tunnel publishes it at `invariant.puglisij.com`, open to anyone with the link. It moves to puglisij.com/invariant later. It has a light and a dark theme. `D-0049` `D-0050`
  - The public page only reads: GitHub through `gh`, CI's receipts from main, TLC's own state graphs, and each repository's lease, which names the one watcher that may act. `D-0077` It shows no code, comment bodies, emails or keys. `D-0051`
  - At `/act`, behind Cloudflare Access, @gitdek can post what an issue is waiting for: answers, ratify, retry and revise. Rebuilds that change no statement can be ratified together, with one confirmation, each on its own issue. He can also open a new issue for the factory to solve. The server checks Access's signed token on every request, and each command posts as his own comment. `D-0065` `D-0067` `D-0093`
  - The watcher writes what it's doing to a status file for it. `D-0051`
- Every project's decisions are one graph. `D-0096`
  - The store is one SQLite file on the machine that runs the factory, opened through a pure-Go driver by every process that needs it. Each repository's record is its decision journal: one file per decision in `decisions/journal/`, one line per write to it, each chained to the line before by hash. `decisions/log.md`'s table is the journal's view. `D-0096` `D-0097`
  - `invariant decisions` makes every write, to the journal and the store in one transaction: `decide`, `ratify`, `supersede` and `link`. Only a person ratifies, and a one-way door names its record. A new decision's ID comes after every one the store has journaled for its project, so two branches on one machine never take the same one. `D-0100` `D-0101`
  - A decision's edges are the ones its journal records, typed `refines`, `supersedes`, `reopens` or `cites`, and every mention of it in another decision, the docs or the code. `show`, `dependents`, `implementers`, `grounds` and `search` answer what rests on a decision and what it rests on, and `sql` runs read-only queries with a timeout. `D-0096` `D-0100`
  - CI rebuilds the graph from the journal alone, on every change, docs included. It fails a change that cites a decision that doesn't exist, rests the SPEC on a superseded one, leaves a SPEC bullet untraced, misses a record, leaves `decisions/log.md` behind the journal, or changes or removes a journal line the base branch has. `D-0102`
- The CLI's `verify`, `synthesize`, `formalize`, `watch`, `scope`, `ratification`, `pin`, `trace`, `dashboard`, `init` and `decisions` commands are built. `init` sets up another repository: it writes the gate workflow, pinned to a full commit of Invariant, which CI reads without a key now that Invariant is public, and prints the steps only a person can take. `D-0000` `D-0013` `D-0017` `D-0026` `D-0036` `D-0051` `D-0054` `D-0055` `D-0075` `D-0100`

## Merging

- Factory pull requests merge on green, starting with the first. `invariant/gate` is the check. The factory merges its own pull request, pinned to the head commit that passed. `main` requires `invariant/gate`, from GitHub Actions, and a pull request for every change, with no exception for admins, so GitHub refuses any merge the gate didn't pass. When the gate doesn't pass, the factory says how on the issue and waits for a writer's `retry`. A gate GitHub never started, as when the account's Actions minutes ran out, is said not to have run, never to have failed, and the dashboard says the same. `D-0004` `D-0021` `D-0033` `D-0075` `D-0081`
- The gate passes only when all of these hold. `D-0004` `D-0013` `D-0014`
  - The pinned statements match.
  - TLC reports no invariant violation and no deadlock.
  - Every witness is reachable.
  - Every property holds of every behavior the spec allows under the fairness statements. TLC checks each property alone, so a broken one is named, with the behavior that breaks it. `D-0069` `D-0071`
  - Every fairness statement is about a step the model takes: with its action added to `Next`, every step of it is already a `Next` step. Fairness on a step the model can't take would leave no behavior to check, and properties would hold vacuously. `D-0071`
  - Every known bug is caught: with its action added to `Next`, TLC finds its expected invariant violated, or, for a property, a behavior under the same fairness that breaks it.
  - Go: the code, explored from `Init()` through `Try()`, reaches exactly the states TLC found, at the same depth. An explorer with only `Successors()` is explored through that. `D-0090`
  - One size past the bounds, the code reaches exactly the states TLC finds there too, at the same depth. For Go, the explorer runs again with its bounds one size larger. For TypeScript and Python, an exhaustive driver runs again and counts, when it names its bounds: in the driver, or in `explore.py` for a Python core. The bounds must be constants named after the model's. One size larger means each number plus one, and a set of numbered model values, such as `{p1, p2}`, with the next one added. If TLC can't finish there within three minutes, or finds a problem in the model there, nothing is claimed or failed. `D-0068` `D-0070`
  - TypeScript and Python: every step the code took is a step the model allows.
  - A driver that explores every state and records its attempts tried every step the model's `Next` names, with every argument, in every state it reached. A step may go untried only where the environment's bounds alone rule it out: TLC finds the model disables it at the ratified bounds and allows it with the environment's numeric bounds one larger. Sizes the manifest calls the code's parameters, such as a capacity, don't count, so a driver tries a step past them and sees the code refuse it. A driver that records only its runs gets a receipt saying its steps weren't checked. `D-0082` `D-0085`
  - Existing code: the driver imports every piece of code the project checks. `D-0054`
  - Gobra verifies Go files marked `// +gobra`, Nagini verifies Python files marked `# +nagini`, and the receipt names any function neither saw. `D-0031`
  - CI is green.
  - The diff stays in scope. It changes one project and nothing else, and an existing project's manifest only in its code's parameters. It adds no dependencies: no Go modules, npm packages or Python requirements. It uses no cgo and doesn't edit `.github/`. It changes a ratified lock only by adding a new, ratified project, or as an amendment. An amendment's lock carries this pull request's issue's ratification, and amends exactly the lock on the base branch. `D-0014` `D-0036` `D-0043` `D-0045`
  - Every factory project's ratification checks out on GitHub: a writer's comment ratified exactly the proposal its lock holds. `D-0034` `D-0036`
- CI runs on pull requests and on main. When a change touches only docs and decisions, its gate is skipped, which GitHub counts as passing, and a factory pull request always runs it. A newer push to a pull request cancels its run that's still going. Every push to main runs to the end, so each commit on main gets its gate and its receipts, and the dashboard reads receipts only from a gate that ran. A factory pull request's gate verifies the one project it changes, since scope already confines it to that project. The gate's own tests run on pull requests that can change the gate. The decision graph is checked on every change, and when that check fails, the gate runs and fails on it too. `D-0079` `D-0080` `D-0083` `D-0102`
- The pinned-statement checks and the scope rules must exist before the factory opens its first PR. `D-0004` `D-0014`
- After a merge, the branch is deleted and the originating issue is notified with the proof artifacts. `D-0000`

## Current focus

- Slices, in order: `D-0005` `D-0013` `D-0024` `D-0042`
  1. The gate, proven on `examples/02-twophase-commit`. Done.
  2. Synthesis for Go. Done.
  3. TypeScript and Python: conformance for existing code, then a Nagini spike. Done.
  4. GitHub. Done: issue #1, a bounded buffer, became pull request #2, which the factory merged once CI's gate passed. `D-0035` `D-0036`
  5. TypeScript and Python synthesis. Done: issue #3, a rate limiter in TypeScript, went from opened to merged as the factory's own bot. `D-0042` `D-0043` `D-0044`
  6. Done, in two parts. Part A, amendments: issue #5 amended the rate limiter, its proposal showed the diff and its lock amends #3's, and the bot merged it once CI's gate passed. Part B, existing projects: copythis-ad#33 checked the app's analysis lease protocol as it is. The gate found a real bug: a stalled retry canceled an attempt whose lease had run out, which erased the record that the attempt may have been charged. copythis-ad#35 fixed it, and the bot merged the check as copythis-ad#34. `D-0042` `D-0045` `D-0046` `D-0047` `D-0053` `D-0054` `D-0059`
  7. Invariant builds itself. Done: #9 ratified the factory's issue protocol, and the bot merged it as `factory/protocol` in #11, proved with Gobra. #13 amended it through the factory, and the bot merged #15 once CI proved it again. Since #12, the watcher checks every step it takes against it. `D-0045` `D-0053` `D-0058`
  8. Code you can ship. Done: code is the system alone, with sizes as parameters, and the explorer or driver is the environment. The gate proves Go code at every size and checks agreement one size past the bounds, and fails code that hardcodes a size. #18 became #20, a connection pool proved at every size, which the bot merged. The check one size larger covers TypeScript and Python drivers too (D-0076). #28 took the rate limiter's bounds out of its code, through the factory, and the bot merged #30. Other existing projects keep theirs until an issue asks. `D-0048` `D-0058` `D-0068`
  9. The factory survives crashes and concurrent work, proved, which brings liveness to the gate. Done: the gate checks properties under fairness. #23 ratified the recovery model, and the bot merged `factory/recovery` as #25, proved at every size. The watcher holds a lease, records its runs and merges, and takes every effect only as the core allows. A test stops it before each of its effects, and the issue still merges with every effect done once. Two watchers ran on this repository for a day, and the second never acted. GitHub refused its one push for the lease, when both reached for it in the same second. `D-0048` `D-0058` `D-0069` `D-0071` `D-0072` `D-0073` `D-0074`
  10. Public. Done: the repository is public since 2026-09-28, which brought CI back on GitHub's free runners. `main` requires the gate and a pull request for every change, and every run from an outside collaborator needs approval. The factory keeps merging. `D-0007` `D-0033` `D-0048` `D-0075` `D-0079` `D-0080`
  11. A driver can't skip a step. Done: the gate checks that a driver or a Go explorer tried every step in every state it reached, except where only a bound rules the step out, and a second agent reviews each one. The factory rebuilt all twelve projects here through #47 to #52 and #64 to #69, and every receipt shows every step tried. The review on copythis-ad's next pull request found three problems in its driver. `D-0059` `D-0082` `D-0085` `D-0086` `D-0087` `D-0088` `D-0089` `D-0090`
  12. One decision graph for every project. In progress: every project's decisions live in one embedded store, SQLite used as a graph through a pure-Go driver. Invariant makes every write, each with a line in the decision's own journal file in the project's repository. This repository's decisions are in it, `invariant decisions` records and answers, and CI checks the graph on every change. Next, agents query it through tools over MCP, the dashboard draws it, and copythis-ad's decisions move in. LadybugDB is the planned upgrade. `D-0096` `D-0097` `D-0100` `D-0102`
- The factory records its numbers: each issue's factory time, people's comments, agent spend and gate runs, in its merge comment and its hidden marker. `invariant ledger` lists them for every issue it took. `D-0048` `D-0062`
- The product's requirements, goals and measures are in [`docs/PRD.md`](docs/PRD.md). `D-0048`

## Project

- Brand: a mark of one state orbiting a fixed point, with the invariant in the accent color. `D-0006` `D-0009`
- The README's graphics are animated, and they're generated from real `invariant verify` output. `D-0015` `D-0025`
- Hosted at `github.com/gitdek/invariant`, public since 2026-09-28. `D-0007` `D-0075`
- Licensed under Apache-2.0. `D-0012`
- A portfolio piece on puglisij.com, with a project card, the Trace Explorer, the Dual Proof Terminal and the PR receipt badge. `D-0008`
  - They're built from real tool output only.
  - Public copy states the checked bounds instead of claiming "100%". `D-0015`

## Undecided: don't build against these

Nothing is undecided right now.
