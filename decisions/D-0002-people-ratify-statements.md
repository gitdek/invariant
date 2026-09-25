---
id: D-0002
title: People ratify the formal statements; the factory proves them
date: 2026-09-25
door: one-way
status: ratified
ratified_by: "@gitdek"
source: kickoff design review (Claude Code session 2f96fee2)
supersedes: kickoff brief §1–2, where the factory derives and proves its own invariants
---

# D-0002 · People ratify the formal statements; the factory proves them

**Decision.** The factory drafts the statements that code is proved against, such as TLA+ invariants and properties. D-0011 settles the exact set. A person ratifies each statement before the factory builds against it. Ratified statements are pinned by hash, and the factory cannot change them; changing one takes a new decision.

When the factory can't formalize an issue without choosing between interpretations, it doesn't choose. It posts the fork on the issue as a decision request.

**Options considered**

- People ratify, and the factory proves. ← chosen; Claude also recommended this one
- The factory writes the statements and proves them, and a person reviews the PR.
- The factory writes the statements, proves them, and auto-merges. This is the brief as written.

**Why.** A proof only shows that the code matches the statement it was proved against. If the factory writes the statement, the proof certifies the factory's own reading of the issue. The word "proven" then discourages the scrutiny that would catch a wrong reading. Separating statement from proof keeps people on what must be true and tools on showing that it is. Nobody has to read the diff.

**What would reopen it.** Ratification turning into the bottleneck. If the median time to ratify goes over 24 hours, consider letting the factory ratify its own two-way-door statements.
