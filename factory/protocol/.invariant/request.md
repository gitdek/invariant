# Prove the factory's issue protocol

Issue #9 in gitdek/invariant, opened by @gitdek.

The factory decides what to do on an issue from the issue's state and what just happened. Those rules are what make Invariant trustworthy, and today they're ordinary Go, checked only by tests. Formalize them, so that the factory can build a core proved against them, and the watcher can run on it (D-0045).

**The issue's state** is the kind of the factory's latest post. The possible kinds are:

- none yet
- asked questions
- proposed statements
- stuck
- unsupported
- ratified
- pull request open
- failed
- merged
- closed

The factory also knows which questions are still open, the current proposal's hash, and the pull request with its head commit.

**Who can direct it.** Only people with write access to the repository direct the factory. A command from anyone else, or from any bot, including the factory itself, changes nothing.

**The commands:**

- **solve** takes the issue, unless the factory is already working on it. It's allowed again after the factory got stuck, found the issue unsupported, or saw its pull request closed.
- **choose F&lt;n&gt; &lt;option&gt;** answers a question the factory asked. Once every open question is answered, the factory drafts again with the answers. An answer to a question that isn't open changes nothing, and it gets a note.
- **revise** drafts again, reading the comments. It works while the factory is asking, proposing, stuck, unsupported or closed.
- **ratify &lt;hash&gt;** ratifies the current proposal, and only it, when the hash names it (12 or more hex digits). These ratify nothing, and get a note:
  - a ratify while a question is still open
  - a ratify when no proposal is waiting
  - a ratify naming an earlier proposal's hash

  For an amendment, the base branch must still hold the lock it amends. If it doesn't, nothing is ratified.
- **retry** looks at a pull request again. It only works after that pull request failed.

**Building and merging.** After a ratification, the factory commits it, writes the code and opens a pull request. It merges only when all of these hold at once:

- CI's gate passed on the pull request's current head commit.
- The head it merges is that same commit.
- The pull request changes only its one project.
- The project's lock is exactly the ratified proposal.

If CI's gate fails, the issue needs a person, and nothing merges until a writer says retry. If someone else merges or closes the pull request, the factory records that.

**It keeps no state of its own.** Everything it knows is in its own posts, so it can stop and restart at any point.

Model one issue, with two proposals and two head commits, so the checks stay small. Keep the model finite without counting events or steps, because the proved core will run inside the real watcher, and a count would cap it.

Project: factory/protocol

## Discussion

**@gitdek:**

An agent posted this for @gitdek, with his permission to act for him overnight (D-0061). Every statement restates a decision he already ratified:

- only writers direct the factory, and no bot does (D-0014, D-0036, D-0041)
- ratification is exact (D-0034)
- nothing is ratified while a question is open (D-0045)
- an amendment lands only on the lock it amends (D-0046)
- the merge conditions (D-0033, D-0036, D-0047)

He'll reread it in the morning, and he can revise it.
