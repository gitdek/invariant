# Decision log

Ordered and append-only. To change a decision, add a new entry that supersedes it instead of editing the old one. Two-way doors get one line. One-way doors link to a full record.

| ID | Date | Door | Status | Decision | Who |
| :-- | :-- | :-- | :-- | :-- | :-- |
| D-0000 | 2026-09-25 | — | ratified | The [kickoff brief](sources/2026-09-25-kickoff-brief.md) is the baseline. Later entries override it. | @gitdek |
| [D-0001](D-0001-factory-in-go.md) | 2026-09-25 | one-way | ratified | Invariant is written in Go. | @gitdek |
| [D-0002](D-0002-people-ratify-statements.md) | 2026-09-25 | one-way | ratified | People ratify the formal statements; the factory proves them and can't change them. Forks it can't resolve become decision requests. | @gitdek |
| [D-0003](D-0003-go-with-gobra.md) | 2026-09-25 | one-way | ratified | Generated code is Go, verified with Gobra. TLA+ and TLC still check the design. | @gitdek |
| D-0004 | 2026-09-25 | two-way | ratified | Auto-merge from the first factory PR: `invariant/gate` is a required check, and GitHub's native auto-merge merges on green. Pinned-statement checks and scope rules must exist first. Claude had recommended starting in shadow mode. | @gitdek |
| D-0005 | 2026-09-25 | two-way | ratified | First slice: `02-twophase-commit`. Claude had recommended the settlement ledger. | @gitdek |
| D-0006 | 2026-09-25 | two-way | ratified | A logo and a polished README from day one. | @gitdek |
| D-0007 | 2026-09-25 | two-way | ratified | Hosted at `github.com/gitdek/invariant`: private now, public later. | @gitdek |
| D-0008 | 2026-09-25 | two-way | ratified | Portfolio piece on puglisij.com: the brief's project card, Trace Explorer, Dual Proof Terminal and PR receipt badge. | @gitdek |
| D-0009 | 2026-09-25 | two-way | proposed | Brand: the mark is one state orbiting a fixed point, with the invariant in the accent color. The wordmark is monoline with accent-dotted i's. See [`docs/brand`](../docs/brand/). | Claude |
| [D-0010](D-0010-role-of-lean.md) | 2026-09-25 | one-way | open | The role of Lean 4 now that code-level proofs use Gobra. | — |
| [D-0011](D-0011-ratification-scope.md) | 2026-09-25 | one-way | open | What gets ratified, and what ties the Go to the TLA+ model. | — |
| [D-0012](D-0012-license.md) | 2026-09-25 | one-way | open | License. Must be decided before the repository goes public. | — |
| [D-0013](D-0013-slice-plan.md) | 2026-09-25 | two-way | proposed | Build the gate first, then synthesis, then GitHub, with acceptance criteria for slice 1. | Claude |
| D-0014 | 2026-09-25 | two-way | proposed | Only users with write access can trigger the factory. Synthesis runs with no secrets and no network. The gate rejects diffs that add module dependencies, use cgo, or edit `.github/` or pinned specs. Required before D-0004's first auto-merge. | Claude |
| D-0015 | 2026-09-25 | two-way | proposed | The showcase uses only real tool output: the Trace Explorer replays a real TLC trace, and the Dual Proof Terminal shows Gobra and Go (per D-0003). Public copy states the checked bounds instead of claiming "100%". | Claude |

**Source.** Every entry above comes from the kickoff design review between @gitdek and Claude, 2026-09-25 (Claude Code session `2f96fee2`).

**Status.** `ratified`: the owner approved it. `proposed`: drafted and awaiting ratification. `open`: a fork nobody has decided. `superseded`: replaced by a later entry.
