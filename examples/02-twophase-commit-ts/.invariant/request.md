# Implement two-phase commit

A transaction spans several resource managers. Implement two-phase commit so they all agree on the outcome.

- A transaction manager coordinates the resource managers.
- Each resource manager either prepares, which means it's ready to commit, or aborts on its own before it has prepared.
- The transaction manager commits only once every resource manager has prepared. It may abort at any time before it has decided.
- Every resource manager then follows the transaction manager's decision.
- Messages are never lost.

The formal statements this must satisfy are ratified in `ratified.lock`, and their text is in `specs/TwoPhase.tla`.
