---
id: D-0075
title: Slice 10 · ready to go public
date: 2026-09-27
door: one-way
status: proposed
proposed_by: agent
source: slice 10 planning, the morning slice 9's recovery core merged, with an audit of the repository's history
---

# D-0075 · Slice 10 · ready to go public

**Goal** ([PRD 1.9](../docs/PRD.md), [D-0007](log.md), [D-0033](log.md), [D-0048](log.md)). The repository goes public when @gitdek chooses, with GitHub enforcing the gate and nothing in it that shouldn't be public. The slice is done when:

- the repository is public;
- `main` requires CI's `invariant/gate` and a pull request for every change;
- a review of what outside contributors can do found nothing they can direct, ratify or leak.

## What an audit of the history found

The audit was run on 2026-09-27, over 120 commits on every ref.

1. **No secrets.** There are no private keys, GitHub or cloud tokens, tunnel credentials, or the agent door's token. The App's key and the tunnel's credentials never entered the repository.
2. **13 merge commits carry `joe@puglisij.com`.** GitHub stamps a merge made through it with the account's commit email. Every other commit uses the noreply address. Rewriting history to remove it would change the hash of every later commit, and receipts and locks record those hashes.
3. **Cloudflare Access.** The team domain, `puglisij.cloudflareaccess.com`, is in the docs. Anyone who opens `/act` sees it anyway. The application's audience tag and the allowed email exist only on the dashboard's command line.
4. **The essay behind Invariant.** Its employer-internal names are the one thing the agent can't check for, since it doesn't know them. The kickoff brief in `decisions/sources/` is the document that's meant to be published.

## What outside contributors could do, once it's public

- **Comment, and open issues.** Nothing happens. Only writers direct the factory, and no bot ever does (D-0014, D-0036, D-0041).
- **Open pull requests from forks.** CI runs the gate on them with a read-only token and no secrets. It also runs their code, `go test` and Docker, as any public repository's CI would.
- **Get an agent to read their words.** This happens only if a writer labels their issue `invariant`. Then its body reaches the formalizer. The formalizer has file tools and TLC, no network and no GitHub. Nothing it drafts is built until a person ratifies it.
- **See the factory's refs.** `refs/invariant/lease` and `refs/invariant/runs/` are readable. They hold random watcher names, and drafts and code that the issues and pull requests show anyway.
- **See receipts, traces and the dashboard.** These are already public by design (D-0049, D-0051).

## Plan

1. **Before:** @gitdek searches the history for the essay's internal names, and turns on GitHub's email privacy so later merges use the noreply address.
2. **The switch:** @gitdek makes the repository public, in GitHub's settings.
3. **Right after,** by pull request or `gh api`, when he asks:
   - branch protection on `main`, requiring `invariant/gate` and a pull request for every change;
   - approval before workflows run for outside collaborators;
   - a line in `AGENTS.md` and the README telling writers that labeling someone else's issue hands its text to an agent.
4. **Other repositories:** `invariant init` stops needing a deploy key for CI to read Invariant (D-0055). Existing keys can be removed.
5. **SPEC's Merging section** says GitHub enforces the gate.

## Questions for @gitdek, with recommendations

1. **When?** Recommend: once slice 9's live check has run its day, so the repository goes public with a finished slice.
2. **Who merges?** D-0033 planned for GitHub's auto-merge to take over. Recommend instead keeping the factory's own merge and having GitHub require the gate, so GitHub refuses any merge the gate didn't pass. The factory's merge is proved twice now, in `factory/protocol` and `factory/recovery`. Handing it to auto-merge would put an unproved path in its place.
3. **The 13 merge commits with `joe@puglisij.com`?** Recommend keeping them, since the address is on his own domain. The alternative is rewriting history.
4. **Outside contributors' workflow runs?** Recommend requiring approval for every run from an outside collaborator.

## What would reopen this

- Outside pull requests becoming a real way to contribute.
- A hosted factory (5.7), which would face the public with a key of its own.
