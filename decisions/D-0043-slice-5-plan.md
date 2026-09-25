---
id: D-0043
title: Slice 5 plan and acceptance criteria
date: 2026-09-25
door: two-way
status: decided
decided_by: Claude
source: slice 5 build (Claude Code session 2f96fee2), carrying out D-0038 to D-0042
---

# D-0043 · Slice 5 plan and acceptance criteria

**Goal** ([D-0042](log.md)). The factory writes TypeScript and Python as well as Go, so it can take issues on @gitdek's other repositories.

## How the factory picks a language

- A label on the issue: `invariant:typescript`, `invariant:python` or `invariant:go`. Without one, the repository's default applies, which is set with `invariant watch -language` ([D-0040](log.md)).
- The formalizer drafts the same TLA+ whatever the language. The proposal says which language the code will be in, and how it will be checked. The language isn't part of the ratified hash: it's how the code is built, not what must be true.

## One layout per language

| | Go | TypeScript | Python |
| :-- | :-- | :-- | :-- |
| The code | its package | `src/`, as a state machine | its package: a Nagini core, `core.py`, and plain `explore.py` |
| Tests | `_test.go` files | `src/*.test.ts`, with `node --test` | `test_<package>.py`, with `unittest` |
| Tied to the model by | agreement | a conformance driver, `conformance.ts` | a conformance driver, `conformance.py` |
| Code-level evidence | Gobra proves it | tested in every reachable state ([D-0038](log.md)) | Nagini proves it ([D-0039](log.md)) |
| People's files | `go.mod`, `go.sum` | `package.json` | none |

The factory writes the language's own project file when it commits a ratification: a `go.mod`, or a `package.json` with no dependencies.

## The gate gains

- **Exhaustive conformance.** A factory TypeScript or Python project's driver explores every state the code can reach, and its manifest says so (`"exhaustive": true`). The gate then requires the driver to visit every state the model reaches. Every step it records must already be a Next step, so the code and the model reach exactly the same states. That's the same guarantee Go's agreement check gives.
- **Scope** also rejects npm dependencies in a `package.json`, and any Python dependency file: `requirements*.txt`, `pyproject.toml`, `setup.py`, `setup.cfg` or `Pipfile`.

## Synthesis

The agent owns the module, the code's directory, the conformance driver and, for Python, the tests beside it. Nothing else it writes reaches the project. The prompt teaches each language what the gate will check:

- **Go:** Gobra's contracts, as before.
- **TypeScript:** erasable syntax only, because Node runs `.ts` directly, and the spec's JSON encoding for the driver.
- **Python:** Nagini's permissions and contracts, from a generic example.

## Slice 5 is done when

1. Unit tests show that a language label picks the layout, the project files and the proposal's wording. Synthesis keeps people's files, including the dependency files, and takes only what the agent owns. Each prompt teaches its language, and scope rejects npm and pip dependencies.
2. Synthesis rebuilds a ratified project in both languages from its statements alone. The log buffer @gitdek ratified on issue #1 is rebuilt as TypeScript and as Python, and each passes the gate:
   - TypeScript: tested against the model in every one of its 87 states.
   - Python: Nagini proves it, and it also passes conformance in all 87 states.
3. The factory takes a labeled issue live. This is also the first run of its own identity ([D-0041](log.md)).

## Built alongside

The factory's GitHub App identity ([D-0041](log.md)):

- `invariant watch -app-id` mints installation tokens from the App's key, and hands them to gh and git through the environment.
- The factory then trusts only its bot's posts, and never takes commands from a bot.
- CI's ratification check rejects any ratifying comment written by a bot.
