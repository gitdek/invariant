---
id: D-0054
title: Slice 6 Part B · existing projects, starting with copythis-ad
date: 2026-09-26
door: two-way
status: ratified
ratified_by: "@gitdek"
proposed_by: agent
source: slice 6 planning, after D-0053 moved Part B to @gitdek's existing projects
---

# D-0054 · Slice 6 Part B · existing projects, starting with copythis-ad

**Goal** ([D-0053](log.md)). The factory works in @gitdek's other repositories, on code that already exists. It starts with [gitdek/copythis-ad](https://github.com/gitdek/copythis-ad), his least important project. There, the video-analysis jobs run on a lease protocol in `src/lib/admin-control-store.ts`: claim, renew, complete, expire, stall, cancel and retry. Its tests already state rules, for example:

- only one lease per job
- a stale lease token changes nothing
- nothing completes after a lease expires
- a stalled attempt is released before a retry

Those are exactly the rules Invariant exists to check.

## Checking code that already exists

Today every factory project holds its own code. An **existing-code project** holds only the model and a conformance driver. The code it checks stays where it is, and the factory never changes it. This is D-0024's path for existing code: tested against the model.

1. **The issue names the code** with `Code:` lines, such as `Code: src/lib/admin-control-store.ts`, and names where the project goes with a `Project:` line (D-0046).
2. **Formalizing.** The formalizer reads the named code and its tests. It drafts the rules the code is meant to keep, a model of the protocol at small bounds, and known bugs. It asks forks wherever the code's intent is unclear, instead of reading intent into it. TLC checks the draft before anyone sees it, as now.
3. **Ratifying** is unchanged: `/invariant ratify <hash>`.
4. **Synthesis writes only the driver.** The driver runs the real code, in memory and with the store's own injected clock, through random claims, renewals, completions, expiries, stalls, cancels and retries. It records each state in the spec's vocabulary, and TLC checks every step. The agent can read the existing code, but its workspace can't change it.
5. **The manifest** names the files it checks, `"existing": [...]`. The receipt records each one's SHA-256, so it says exactly which code was checked, at which commit.
6. **Running the real code.** Existing code has real dependencies. The store uses `better-sqlite3`, a native module, so the standard-library-only sandbox can't run it. The gate builds a dependencies image from the repository's own `package-lock.json`, with network allowed only while it builds. It then runs the driver on a throwaway copy of the repository, in that image, with no network and none of the host's environment. Receipts name the image by its hash.
7. **When the check fails,** the code took a step the ratified rules forbid. Either the code has a bug or the model is wrong. The factory can't change the app, so the pull request opens as a draft with the exact counterexample. A person or an agent fixes the app in an ordinary pull request, and then `/invariant retry`.
8. **Scope** is unchanged. A factory pull request changes one project and nothing else, so it can never touch the code it checks.

A caution: the claim rests on the driver really calling the code. The driver is short and part of the pull request, the gate requires it to import every file it names, and the receipt says the code is **tested against the model by the factory's driver**. A stronger check, requiring the driver to catch planted mutations of the real code, can come later.

## Working in another repository

1. **`invariant init`**, finally built, sets up a repository. It writes the gate workflow, `.github/workflows/gate.yml`, which builds Invariant at a pinned commit and runs the same gate CI runs here. It also prints the setup steps. A person commits the workflow, because the App can't edit CI (D-0041).
2. **CI reads Invariant through a read-only deploy key** (@gitdek's call). Invariant is private. A deploy key can only read `gitdek/invariant`, isn't tied to anyone's account, and doesn't expire. Its private half is a secret in copythis-ad, used only to check Invariant out.
3. **The factory's bot acts there too** (@gitdek's call). copythis-ad joins the App's installation, and a second watcher runs with `-repo gitdek/copythis-ad -projects invariant -language typescript`.
4. **The dashboard shows both repositories** (@gitdek's call): issues, stages, statements and receipts. It never shows code or comment bodies.

## Part B is done when

1. Unit tests cover the new pieces:
   - an existing-code manifest
   - the dependencies image
   - the runner on a throwaway copy of a repository
   - the formalizer and synthesis seeing, but not changing, the named code
   - a driver that doesn't import its files
   - `init`'s workflow
   - the dashboard with two repositories
2. Live: an issue on copythis-ad goes from opened to merged. @gitdek ratifies the lease rules, the factory writes the model and the driver, CI's gate in copythis-ad runs the real store against the model, and the bot merges. Or the gate finds a real bug, the receipt shows the counterexample, the app is fixed, and the issue then merges.

## Decided by @gitdek

1. **First job in copythis-ad:** check the existing lease code as it is. The alternative was a new proved lease core that the app would switch to.
2. **CI's access to Invariant:** a read-only deploy key. The alternatives were a fine-grained token, or making Invariant public first.
3. **Who acts there:** the factory's bot, with copythis-ad added to the App's installation. The alternative was @gitdek's account.
4. **The dashboard:** it shows both repositories. The alternative was Invariant's own only.
