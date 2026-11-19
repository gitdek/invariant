---
id: D-0488
title: A lock pins each statement's layout as well as its text
date: 2026-11-19
door: one-way
status: ratified
ratified_by: "@gitdek"
proposed_by: agent
source: "#814, which @gitdek ratified as it states the proposal on 2026-11-19, in conversation, asking for his ratification to be recorded"
---

# D-0488 · A lock pins each statement's layout as well as its text

**Goal.** A pin should mean that TLC checks what a person ratified. It hashes a statement's canonical text: its tokens, with comments dropped and whitespace collapsed. TLA+ reads a bulleted list of `/\` or `\/` by the columns its items start in, so the same tokens can mean different things. On the pinned TLC,

```tla
Inv ==
    /\ FALSE => FALSE
    /\ FALSE
```

is `(FALSE => FALSE) /\ FALSE`, which TLC reports violated, and `Inv == /\ FALSE => FALSE /\ FALSE` is `FALSE => (FALSE /\ FALSE)`, which holds. Both have one canonical text and one pin. Any ratified statement with an implication, a disjunction or another weaker operator inside a bulleted item could be weakened by joining its lines or moving a line across a list's column, and keep its pin ([#814](https://github.com/gitdek/invariant/issues/814)). [D-0423](journal/D-0423.jsonl) made the gate read the text TLC reads, and left layout outside a pin. This closes that.

## What it is

- **A layout pin beside each pin.** A statement in a lock records `layout` beside `sha256`: the SHA-256 of the layout of every definition its pin covers, in the pin's order. A definition's layout is its text as written, with each comment blanked in place, the space at each line's end trimmed and the lines left blank dropped, so every token keeps its line and its column. Editing a comment, or adding one on a line of its own or after the code on a line, changes no layout pin. Moving a token does, and so does a comment written before a token on its line, since that moves the token.
- **New ratifications hash both pins.** The proposal hash covers each statement's `layout` with the rest of it, so the layout a person saw in the proposal is the layout that's pinned. The factory's drafts and `invariant pin` record it.
- **Existing locks keep verifying.** A lock ratified before this keeps its pins and its proposal hash, so its ratifying comment or decision still checks. Its layout pins are added once, by a migration, as `migrated_layout`, which the proposal hash leaves out. Each is computed from the model at the commit that recorded the lock's ratification: the commit that wrote the proposal it holds into the lock, where the lock holds it and its parent's lock doesn't. A lock changed and changed back has more than one, and they must lay its statements out alike. Every lock in this repository was migrated this way, and `invariant pin -layout` does it for another repository's.
- **A lock holds one kind of layout pin, or none.** Every statement has a ratified layout pin, or every statement a migrated one, or none has one.
- **CI refuses a layout pin's change without a new ratification.** A ratified layout pin is part of the proposal, so changing it fails the ratification check as changing a pin does. For a migrated one, the ratification check reads the repository's whole history: each migrated layout pin must be the one the model gives at every commit that recorded the lock's proposal, on every branch the history holds, and a lock whose proposal had migrated layout pins anywhere in that history must still have them. Nothing a pull request adds to the history changes which commits recorded a proposal before it.
- **The gate checks both pins, and reports a layout mismatch as its own failure.** A statement whose tokens match and whose layout doesn't fails with its layout, apart from a statement whose text changed. When the gate can read the model as it was ratified, from the commits that recorded the proposal or from the ratified project a synthesis builds from, it names the definition and the first line that moved, with that line now and as ratified. The gate's verdict comes from the lock alone: the history only makes the message better.
- **A lock without layout pins keeps verifying.** In a repository that hasn't migrated, the gate checks each pin as before, and its receipt says the statement's layout isn't pinned yet. Nothing fails.

## Why it's a one-way door

It changes what a lock records and what a proposal hashes. Every lock ratified from here on rests on how a layout is read and hashed, and changing that later changes their proposals, each of which would need a person to ratify it again. The migration records pins that only the repository's history can check.

## Compatibility

- **A gate from before this change** reads a migrated lock without its migrated layout pins and computes the same proposal, so it passes as it did. It reads a lock ratified with layout pins without them, so the proposal it computes isn't the one a person ratified, and its ratification check fails. A repository moves the gate it pins before its factory drafts with this build, as it did for [D-0470](D-0470-bug-classification.md).
- **A watcher from before this change** drafts proposals without layout pins. Its ratifications pin no layout, and the gate says so. The watchers move to this build so that every new ratification pins the layout. In this repository a test holds every lock to pinning its layout, so a project such a watcher builds gets its layout pins with `invariant pin -layout` before it merges.
- **An older `invariant pin`** rewrites a lock without its migrated layout pins. CI's ratification check refuses the result.

## Options considered

| Option | What it gives | What it costs |
| :-- | :-- | :-- |
| **A. A layout pin beside each pin, in new proposals, migrated into old locks outside theirs** (#814) | What TLC reads is pinned, comments stay free to edit, and every existing ratification still checks | Two kinds of layout pin for good, and a migrated pin only the repository's history can check |
| B. One pin that hashes the layout | One kind of pin | Every pin and proposal hash changes at once, every ratifying comment stops checking, and every lock needs a person to ratify it again |
| C. Hash what SANY parses instead of the text | Reformatting that keeps the meaning keeps the pin | A Java parser in the pin's path, or a Go copy of SANY's column rules that has to match it exactly, in the trusted base |
| D. Refuse a weaker operator inside a bulleted item | No new pin | It reads TLA+'s column rules itself, which is what #812 refused to do, and refuses models people wrote and ratified |
| E. Migrate from the model as it is today | No history to read | It pins whatever layout is there now, including one changed since the ratification, which is what a migration is for catching |
| F. Re-ratify every lock with layout pins | One kind of layout pin | Eighteen locks here, and every other repository's, each wait for a person, for statements nobody changed |

## Ratified by @gitdek

@gitdek ratified the proposal as #814 states it on 2026-11-19, in conversation, and asked for his ratification to be recorded. It's recorded by hand with `invariant decisions ratify -by @gitdek`, as #352 recorded his ratifications of #271's amendments.

## What would reopen it

- A TLC upgrade that reads layout differently, such as a tab's width. A layout pin is conservative, since any change of indentation changes it, but it assumes TLC reads a line's tokens by their place in the text as written.
- A formatter people want to run on ratified models. Each reformatted statement needs a new ratification, and if that's too often, option C becomes worth its cost.
- A repository whose history is rewritten or lost. Its migrated layout pins can't be checked any more, and its locks need ratifying with layout pins.
