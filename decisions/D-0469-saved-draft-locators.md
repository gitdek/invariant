---
id: D-0469
title: A saved draft's address on the dashboard is permanent
date: 2026-11-13
door: one-way
status: proposed
proposed_by: agent
source: "#897's ratified plan, step 2 of #783; its review asks whoever merges it to record the public locator contract"
---

# D-0469 · A saved draft's address on the dashboard is permanent

**Goal.** A draft the factory saves, under [#790's plan](../.invariant/plans/issue-790.json), is too large for the comment that proposes it, so the comment carries a reference to it. A person deciding on that draft needs to read it: its rules and bounds, what TLC found, each alternative and its chart. [#897's plan](../.invariant/plans/issue-897.json) has the dashboard serve it at an address the cards link to, and makes that address permanent.

## What it is

- **One address names one draft.** `/draft/<owner>/<repository>/<issue>/<full-result-commit>/<payload-digest>` names the draft by the immutable commit that holds its result and the SHA-256 of the result's bytes. Nothing in it is a branch, a latest revision or a card.
- **It answers with that draft or with a failure.** The address redirects to a script-free page whose own identity binds the reference, the exact bytes and what's rendered from them, and the same bytes can be downloaded. A revision of the proposal, an answer to one of its questions, its ratification and the eviction of pages generated from it can't make the address answer with anything else. Evidence that's missing, altered or not the factory's gets an explicit failure, never another draft.
- **The dashboard keeps what the address needs.** It retains each authenticated record, and the factory comment and completed run the record was read under, independently of the cards it shows. Generated pages can be evicted and are rebuilt from the retained record, a restart included, after its bytes and its authority are checked again. Bytes that were damaged can't be sealed again under their old identity.
- **Only the factory's drafts have one.** The dashboard reads a saved draft only for a repository it's configured for, from a marker that repository's known factory account posted, through GitHub reads of the completed run at its full commit. The address is an index of authenticated records it retains. It isn't a file server for the repository.

## Why it's a one-way door

The address is printed on the public page and on `/act`, beside each proposal a person is asked to decide. People copy links, and a link to evidence is worth something only if it still shows that evidence later. Changing the shape of the address, or letting an old one answer with a newer draft, breaks every link already given out, and nothing can call them back.

## Options considered

| Option | What it gives | What it costs |
| :-- | :-- | :-- |
| **A. An address made of the immutable commit and the bytes' digest, with the records retained** (the plan) | A link that shows the same evidence for good, and can be checked against the repository by anyone | The dashboard keeps every saved record it has served, so its store grows with the drafts a repository saves |
| B. An address made of the issue and its latest draft | Short links, and nothing to retain | A link changes what it shows when the draft is revised, so a person can't cite what they decided on |
| C. Link to the saved file on GitHub | Nothing for the dashboard to keep | A person reads raw JSON, with no rules table, chart or alternatives, and the link dies if the run's ref is removed |
| D. No address until a proposal is ratified | Nothing published that could change | The person deciding has nothing to read, which is when they need it |

## What would reopen it

- The retained records growing past what the dashboard's host should hold. That would need a retention rule, and an honest answer for an address whose record was let go.
- The dashboard moving to another host or path, which needs the old addresses to keep answering.
- Serving a private repository, where a permanent public address is the wrong default.

## Questions for @gitdek, with a recommendation

1. **Keep A, with the address permanent?** Recommend yes. It's what the plan ratified on #897 states, and what's built and merged. This record is here because a one-way door gets one, and it stays `proposed` until you ratify it.
