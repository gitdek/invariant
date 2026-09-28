---
id: D-0096
title: One decision graph for every project, in an embedded store
date: 2026-09-28
door: one-way
status: proposed
proposed_by: agent
source: "@gitdek's direction on 2026-09-28: one embedded graph store holds the decisions of all his projects, since flat files scattered across many repositories get disorganized. An agent researched the engines the same day."
---

# D-0096 · One decision graph for every project, in an embedded store

**Goal.** Every project's decisions live in one embedded graph store. Agents and people can ask it what depends on a decision, what implements it, and what reopening it would touch. The record keeps what `decisions/log.md` gives today: each decision is written down, attributed, ratified by @gitdek where it must be, and reviewable in a pull request.

## Why now

`decisions/log.md` is one table, and it doesn't scale. On 2026-09-28, parallel pull requests collided in it three times, and D-0091 and D-0092 had to be renumbered. A project brought in from existing code, or a large one, will have many more decisions than this repository's 95. And the connections that matter are only implied: one decision refines another, a SPEC line rests on one, code implements one. An agent finds those connections by reading text.

@gitdek wants one store for all his projects, rather than files scattered across many repositories, and chose an embedded graph store for its traversal.

## What it would be

- **The store.** One SQLite file in WAL mode, opened through `modernc.org/sqlite`, a pure-Go driver with no cgo. Its tables are `node` (projects, decisions, statements, SPEC lines, issues and pull requests, code locations), `edge(src, type, dst)` indexed both ways, and an append-only `journal`. The edge types are `refines`, `supersedes`, `implements`, `reopens`, `cites` and `belongs-to`.
- **Writes go only through Invariant.** A CLI and MCP tools (`decide`, `ratify`, `supersede`) check the schema, then write the change and its journal row in one transaction. Triggers refuse any update or delete of the journal. Ratifying stays @gitdek's command, pinned by hash as statements are.
- **A journal in git.** Every write also adds one line of JSONL to `decisions/journal.jsonl` in the affected project's repository. A pull request shows every decision change, and the store can be rebuilt from the journals. The database file stays out of git.
- **Agents query through tools.** Named traversals answer what depends on a decision, what implements it, and what reopening it touches. Each is a tested query with a depth cap. Any SQL an agent writes runs on a read-only connection.
- **Every process opens the store directly.** The watchers, the dashboard and each agent's MCP server open the same file. No daemon owns it, and a write that finds another in progress waits.
- **CI checks the graph.** It rebuilds the store from the journal, then checks that every cited decision exists, that the SPEC never cites a superseded one, and that every SPEC line traces to a decision. The dashboard draws the graph.
- **A way out.** The store sits behind one Go package, so a later move to LadybugDB changes that package, not its callers.

## The engines, as of 2026-09-28

| Engine | Status | Go | Several processes | Query language | License |
| :-- | :-- | :-- | :-- | :-- | :-- |
| SQLite through `modernc.org/sqlite` | 3.53.4 released 2026-07-24; file format pledged stable to 2050 | Pure Go | Many readers, one writer at a time | SQL with recursive queries | Public domain; BSD-3 driver |
| LadybugDB, the Kùzu fork | v0.20.4 released 2026-09-10; very active, 11 months old | cgo; the Go binding lags at v0.17.0 | One read-write process; readers only when none writes | Cypher, GQL extension | MIT |
| DuckDB with DuckPGQ | DuckDB 1.5.6 released 2026-09-28; DuckPGQ calls itself a research project | cgo | One read-write process, or several read-only | SQL, SQL/PGQ | MIT |
| Kùzu | Archived 2025-10-10, when Apple bought the company | cgo, frozen | One read-write process | Cypher | MIT |

CozoDB and Cayley are dormant. SurrealDB's license is BSL, and it has no embedded Go client. FalkorDB Lite is Python only, under SSPL. Oxigraph and NeuG have no Go binding. GraphLite and Turso have no releases, or no recursive queries yet, and the pure-Go graph databases found are each under a year old.

A local test on 2026-09-28, with 1 million nodes and about 3 million typed edges, found:

- A 3-hop traversal took 0.08–0.2 ms, and everything that depends on a node mid-graph, 500,000 rows, took 0.91 s.
- Three writer processes and six readers ran for 20 seconds, making 3,651 writes and about 27,300 traversals with no errors. The slowest write took 4.5 ms.
- A traversal that tracked depth ran for more than 10 minutes before it was stopped. That is the reason for the named tools.

LLMs write SQL most accurately: 47% correct with no examples, against 34% for Cypher and 3% for SPARQL, in a NeurIPS 2024 benchmark (arXiv 2411.05521).

## Options considered

- **A. LadybugDB as the store now.** It has native Cypher and shortest paths. But its storage format still changes, integrity fixes land this month, it needs cgo, and one process would have to own the store for every project. It's the planned upgrade once its Go binding and storage settle.
- **B. SQLite as the graph (recommended).** It meets every requirement, and traversal runs through tested tools instead of a graph language.
- **C. One file per decision in git, with a derived index.** Every change is reviewable, but across many repositories the files scatter, and the graph is rebuilt rather than kept. @gitdek set this aside.
- **D. Keep the flat log.** Parallel changes collide, and it doesn't scale.
- **E. A hosted graph database as the record.** It needs a server, access control and backups. It could come later, as a read-only copy for views across an organization.

## Plan

1. **The store package.** The schema, the journal and its triggers, WAL with a busy timeout and immediate transactions, and the named traversals with their tests.
2. **The tools.** `invariant decide`, `ratify` and `supersede` in the CLI, and the same tools, plus the traversals and read-only SQL, over MCP.
3. **Migration.** Import this repository's 95 decisions and copythis-ad's, with every mention of a decision ID in the docs and code as an edge. Write each repository's journal.
4. **CI and the dashboard.** The graph checks run in CI. The dashboard draws the graph, and `decisions/log.md` becomes a view generated from the store.
5. **The rules.** AGENTS.md says decisions are recorded with `invariant decide`, and the factory's agents use the MCP tools.

## Acceptance

1. Every decision in both repositories is in the store. Every ID cited in their docs and code resolves, and each journal rebuilds a store identical to the original.
2. An agent answers what depends on D-0068, and what implements D-0082, through the tools, with citations.
3. CI fails a pull request that cites a decision that doesn't exist, or a superseded one from the SPEC.
4. Two writers and several readers in separate processes work at once with no errors, and the journal refuses edits.

## Questions for @gitdek, with recommendations

1. **B, or another option?** Recommend B, with A as the planned upgrade.
2. **Decision IDs across projects.** Recommend each project's own numbering under its name, such as `invariant/D-0096` and `copythis-ad/D-0001`, so every ID in today's docs and code still resolves.
3. **Where the store lives.** Recommend one file on the machine that runs the factory, rebuilt from the journals anywhere else, CI included.
4. **The journal's place.** Recommend `decisions/journal.jsonl` in each repository, one line per write. An edge to another project's decision is checked against that project's journal.
5. **When.** Recommend slice 12, before the Codex backend, since every agent's work after it writes to the store.

## What would reopen this

- LadybugDB's Go binding tracking its core, and a storage format it promises to keep, which would make A the store.
- Decisions shared across an organization, which would bring in E as a read-only copy.
- Traversals the named tools can't express, often enough that agents need a graph language.
