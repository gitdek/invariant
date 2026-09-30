# Prove that the store's journal only grows (#177, part 4)

Issue #195 in gitdek/invariant, opened by @gitdek.

Part 4 of #177's four parts, built by hand while the factory is paused (D-0131).

The machine's one store journals every decision line as it's written (D-0096, D-0097), and a rebuild replaces a project's graph with what one checkout holds. What must be true:

- The store's journal only grows: every line it ever held, it holds.
- It keeps the lines of branches that were never merged, and of branches that were abandoned.
- A rebuild adds a checkout's lines and drops none.

The proved core goes in `factory/store-journal`, and the store journals and rebuilds with it.

The code is package `storejournal`: a Go core that holds the store's journal and each checkout's lines, and takes each of the model's steps, `Write`, `TakeIn`, `Rebuild` and `Abandon`, each for a checkout. Every operation decides for itself whether it runs: where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes. `Try` tries every step the model's Next names, for every checkout, in every state, and lets the code refuse, and `Abstract` gives a state as the model sees it (D-0090). The store journals each line, and rebuilds a project, as the core does: a rebuild adds a checkout's lines and drops none.
