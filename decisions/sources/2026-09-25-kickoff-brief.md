> **Source document.** @gitdek's kickoff brief for Invariant, captured verbatim in the kickoff design review (Claude Code session `2f96fee2`, 2026-09-25). The only changes are formatting: headings, paragraph breaks and a code fence. This is a record, not the spec. Where it conflicts with the [decision log](../log.md), the log wins. Cited as D-0000.

# Project Invariant: Formally Verified Autonomous Code Factory

Subtitle: Autonomous Software Synthesis Powered by TLA+ State Exploration, Lean 4 Interactive Theorem Proving, Closed-Loop GitHub Automation, and Terminal Agent Orchestration

## 1. Executive Summary

Project Invariant represents a paradigm shift in the software development lifecycle, transitioning AI code generation from probabilistic text synthesis to deterministic mathematical verification. Standard LLM coding agents suffer from hallucinations—edge-case deadlocks, boundary failures, or concurrency races that pass simple unit tests but fail under production loads.

Invariant operates as an autonomous, end-to-end Code Factory:
 * Connects directly to GitHub via a Webhook worker or GitHub App.
 * Listens to issue events and feature requests, explores the full state-space using TLA+, and mathematically proves functional invariants using Lean 4.
 * Plugs into terminal coding engines (Claude Code CLI, OpenAI/Codex APIs, or custom agent runtimes) as synthesis backends.
 * Generates zero-panic target code (Rust/C++) backed by formal proof artifacts.
 * Automatically opens Pull Requests equipped with formal verification receipts.
 * Executes an Autonomous Auto-Merge Policy Engine, merging changes without human intervention once formal proofs and CI checks pass.

## 2. End-to-End System Architecture & Lifecycle

```text
GitHub Issue / Trigger (/invariant solve)
                 │
                 ▼
     Invariant GitHub App Worker
                 │
                 ▼
      Synthesis Backend Orchestrator
   (Direct LLM API / Headless Claude Code)
                 │
                 ▼
 ┌───────────────────────────────────────────────┐
 │       Dual-Layer Verification Engine          │
 │                                               │
 │  1. TLA+ / TLC Model Checker                  │
 │     (Exhaustive state exploration / traces)   │
 │                                               │
 │  2. Lean 4 Kernel Proof Checker               │
 │     (Functional correctness / invariants)     │
 │                                               │
 │  3. Closed-Loop Auto-Repair                   │
 │     (Parses counterexamples & self-heals)     │
 └───────────────────────────────────────────────┘
                 │
                 ▼
   Code Extraction (Rust / C++) & Test Suite
                 │
                 ▼
 Git Commit & Pull Request Opened (with Proof Report)
                 │
                 ▼
 Gated Auto-Merge Engine (TLC OK + Lean OK + CI Green)
                 │
                 ▼
 Issue Closed & PR Merged to Default Branch
```

### Lifecycle Phases

 * Event Ingestion & Context Ingestion:
   * Listens for webhook events (issues.opened, issue comments like /invariant solve, or repository dispatch triggers).
   * Clones target repository context, parses problem boundaries, and extracts AST definitions.
 * Dual-Layer Verification Pipeline:
   * TLA+ / TLC Gate: Validates concurrency safety, temporal logic, and deadlocks. If a race condition or counterexample trace is found, TLC error traces feed directly back into the LLM context for iterative self-healing.
   * Lean 4 Kernel Gate: Proves functional correctness, type contracts, and arithmetic bounds. The Lean kernel enforces zero unproven sorry statements or axioms.
 * Commit & Pull Request Synthesis:
   * Emits idiomatic target code (Rust or C++) alongside formal spec artifacts stored under .invariant/specs/ (.tla and .lean).
   * Creates an isolated branch (invariant/issue-<id>-<slug>) and opens a Pull Request.
 * Formal Verification Receipt:
   * Populates the PR with high-contrast proof metrics: total state-space explored by TLC, Lean kernel proof confirmations, and compiler lint checks.
 * Autonomous Auto-Merge Policy Engine:
   * Evaluates branch protection and verification rules.
   * If all gates pass (TLC = 0 violations, Lean = 0 errors, CI = green), Invariant merges the PR (Squash/Rebase via GraphQL), prunes the branch, and notifies the originating issue with proof artifacts.

## 3. Terminal Agent Execution & Integration Modes

Invariant supports two primary integration layers for driving code synthesis engines:

### Mode A: Direct API Orchestrator (Recommended for CI / Production)

 * Uses native SDKs (@anthropic-ai/sdk, openai) inside packages/orchestrator.
 * Exposes verifiers directly as LLM tool definitions:
   * run_tlc_model_check({ spec_path })
   * run_lean_proof_check({ proof_path })
   * create_git_pr({ branch, title, body })
 * Benefits: High performance, zero CLI screen-scraping, structured JSON outputs, and full compliance with terms of service.

### Mode B: Subprocess Terminal Worker (Claude Code / Codex CLI)

 * Spawns CLI tools as sandboxed child processes via child_process.spawn or execa.
 * Drives execution via headless flags (claude --print "...").
 * Feeds stdout/stderr compiler and model-checker traces back into the process standard input for continuous local verification loops.

## 4. The Master LLM Implementation Prompt

Act as a Principal Formal Methods Engineer and Systems Architect. Your task is to bootstrap 'Project Invariant', an autonomous, formally verified code factory that links GitHub issues to mathematically proven, auto-merged pull requests.

### 1. Monorepo Layout
Initialize a clean monorepo with the following architecture:
- packages/github-worker:
  - Built with Node.js/TypeScript using Octokit / Probot.
  - Webhook handlers for `issues.opened`, `issue_comment.created`, and `check_suite.completed`.
  - Git automation engine (clone, branch creation, commit, push, PR creation, and auto-merge via GitHub GraphQL API).
- packages/orchestrator:
  - Agent state machine managing the synthesis-to-proof loop.
  - Pluggable backend for direct LLM API calls or headless terminal agent execution.
  - LLM context management, prompt templating, and auto-repair feedback orchestrator.
- packages/verifiers/tla:
  - Headless TLC model checker runner (Dockerized).
  - Counterexample AST parser that translates state-trace violations into LLM prompt repairs.
- packages/verifiers/lean:
  - Lean 4 toolchain runner (ELAN + Mathlib4 in Docker).
  - Proof diagnostic parser that validates kernel receipts and identifies unproven goals.
- packages/cli:
  - Developer CLI (`invariant init`, `invariant verify`, `invariant synthesize`, `invariant trace`).
- examples/:
  - 01-lockfree-ringbuffer (Rust + TLA+ + Lean 4)
  - 02-twophase-commit (Rust + TLA+ + Lean 4)
  - 03-atomic-settlement-ledger (Rust + TLA+ + Lean 4)

### 2. Auto-Merge & Verification Gate Specification
Implement a strict auto-merge gatekeeper (`InvariantGatekeeper`):
- PRs can only be auto-merged if:
  1. TLC model checker reports zero deadlocks and zero invariant violations.
  2. Lean 4 kernel validates all proofs with zero `sorry` placeholders.
  3. Cargo test & clippy pass cleanly.
- If checks fail:
  - Trigger auto-repair loop (up to 3 iterations).
  - If unfixable, tag PR with `invariant:human-review-needed` and leave an issue comment with the counterexample trace.

### 3. GitHub PR Template Generator
The worker must generate professional PR descriptions containing:
- Summary of verified issue.
- Formal Verification Receipt:
  - States explored by TLC: e.g., 2,412,890 states checked.
  - Lean 4 theorem declarations proven.
  - Invariant safety properties satisfied.
- Target diff summary.

Bootstrap the repository structure, Docker configurations, GitHub webhook receiver, and verification engine now.

## 5. Portfolio Presentation & Positioning (For Puglisij.com)

 * Card Title: ◉ Invariant — AUTONOMOUS FORMAL CODE FACTORY
 * Tags: Autonomous Agents / Formal Verification / Infrastructure
 * Micro-Copy: "An autonomous code factory that turns GitHub issues into formally verified Pull Requests—proven with TLA+ and Lean 4, then merged without human intervention."

### Comparative Value Matrix

| Capability | Traditional AI Code Gen | Invariant Synthesis Engine |
|---|---|---|
| Logic Assurance | Probabilistic / Statistical | Deterministic / Mathematical Proof |
| Concurrency Bugs | Discovered at Runtime | Eliminated at Design Time |
| Verification Method | Unit Testing (Incomplete) | Model Checking (Exhaustive) |
| Runtime Safety | Exception Handling | Proof-based Zero-Panic Guarantee |
| Merging Policy | Manual Review Required | Gated Autonomous Auto-Merge |

### Visual Artifacts & Interactive Showcase

 * The Trace Explorer: An interactive timeline UI stepping through a TLC counterexample trace transitioning from a deadlock into a mathematically sound state machine.
 * Dual Proof Terminal: A split-screen view showing Lean 4 inductive proof tactics validating alongside clean Rust target compilation.
 * The PR Receipt Badge: A visual mockup of the GitHub PR badge showing: 100% States Checked | 0 Axiom Violations | Auto-Merged.
