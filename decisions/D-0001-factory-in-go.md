---
id: D-0001
title: Invariant is written in Go
date: 2026-09-25
door: one-way
status: ratified
ratified_by: "@gitdek"
source: kickoff design review (Claude Code session 2f96fee2)
supersedes: kickoff brief §4, "Built with Node.js/TypeScript using Octokit / Probot"
---

# D-0001 · Invariant is written in Go

**Decision.** Invariant itself is written in Go. That covers the CLI, the orchestrator, the verifier runners, the gate and the GitHub integration.

**Options considered**

- Node.js and TypeScript with Probot, as the kickoff brief specified.
- Go. ← chosen

**Why.** The owner's call. No further rationale was recorded in the review.

**Implications noted at the time**

- One binary can serve every entry point: the CLI, a GitHub Action, a webhook server, and an MCP server that exposes the verifiers to any coding agent.
- The Claude Agent SDK exists only for TypeScript and Python. Go calls the model API directly with `anthropic-sdk-go`, and drives coding agents such as Claude Code or Codex CLI as subprocesses through their JSON streaming modes.

**What would reopen it.** A required integration that exists only as a TypeScript or Python library and can't be reached across a process or network boundary.
