# Existing Python applications

How the gate checks an existing Python application as it is, without changing a line: adopting it, exploring every state, its dependencies, the evidence it records for each component in ordinary Go and Python, trusted contracts for what the application calls, and proving a function in place. [The README](../README.md#the-gate) lists every check the gate runs, and [the gate's page](gate.md) what each claim on a receipt needs.

Python 3.12, 3.13 and 3.14 are supported. Execution uses the separately pinned Python 3.13 image, which also supplies the plumbing sandbox's interpreter. CI's general verification jobs use 3.12, its pinned-Python jobs use 3.13 ([D-0304](../decisions/journal/D-0304.jsonl)), and `invariant/python-latest` runs the Python tests under 3.14 ([D-0309](../decisions/journal/D-0309.jsonl)); supporting 3.14 does not change that execution image.

## Manually adopting an existing Python application

`invariant verify` checks an existing Python application against its model with authenticated read-back, sampled or, for an exhaustive manifest, explored in every state the application reaches, every step tried and one size larger ([Exploring every state](#exploring-every-state), #342). It uses one staged copy and one dependency image for the whole run. Explicit components get independent evidence; omitting them keeps a legacy receipt with no invented inventory. No Nagini annotation is required, and a `# +nagini` marker does not enable proof on this path: a function is proved here only when a check selects it for [proof in place](#proving-a-function-in-place). The factory doesn't draft or synthesize this mode yet.

Here is a complete small application layout. Paths in `existing` and `components` are relative to `app`; model, code and conformance paths are relative to `verification/lifecycle`.

```text
app/
  requirements.txt
  deps/application.txt
  deps/constraints.txt
  tasks/__init__.py
  tasks/lifecycle.py
  tasks/application.py
  tasks/next.txt
  tests/__init__.py
  tests/test_tasks.py
  verification/lifecycle/
    driver.py
    .invariant/invariant.json
    .invariant/ratified.lock
    .invariant/specs/Lifecycle.tla
```

The two `__init__.py` files are empty. `requirements.txt` contains `-r deps/application.txt`. `deps/application.txt` contains these two lines:

```text
packaging>=24,<25
-c constraints.txt
```

`deps/constraints.txt` contains `packaging==24.2`, and `tasks/next.txt` contains `running` followed by a newline. Only application-root requirements select the environment, unless the manifest names an `environment` script, as below; a verification-local requirements file cannot replace them. Installation admits public PyPI wheels, with original inputs, canonical requirements and actual wheel hashes recorded. Installation may need network; execution is offline. Omitting requirements uses the pinned standard-library image.

`tasks/lifecycle.py`:

```python
from pathlib import Path
from packaging.version import Version

def advance(state):
    assert Version('1.9') < Version('1.10')
    if state == 'pending':
        return Path(__file__).with_name('next.txt').read_text().strip()
    return 'done'
```

`tasks/application.py`:

```python
from .lifecycle import advance

def start():
    return 'pending'

def tick(state):
    return advance(state)
```

`tests/test_tasks.py`:

```python
import unittest
from tasks.application import tick

class TaskTests(unittest.TestCase):
    def test_step(self):
        self.assertEqual(tick('pending'), 'running')
```

`verification/lifecycle/driver.py` reads actual calls through the gate's stateless lifecycle handle:

```python
from invariant_observe import functional
from tasks.lifecycle import advance
from tasks import application

def drive():
    with functional(advance, application.start,
                    argument='state', field='task') as life:
        life.step(application.tick)
        life.step(application.tick)
        life.step(application.tick)

if __name__ == '__main__':
    drive()
```

The complete `.invariant/specs/Lifecycle.tla` is:

```tla
---- MODULE Lifecycle ----
EXTENDS Naturals
CONSTANT Limit
VARIABLE task
vars == <<task>>
Init == task = "pending"
Next == \/ /\ task = "pending" /\ task' = "running"
        \/ /\ task = "running" /\ task' = "done"
        \/ /\ task = "done" /\ UNCHANGED task
Spec == Init /\ [][Next]_vars
Safe == task \in {"pending", "running", "done"} /\ Limit >= 1
Seen == task = "done"
Bad == task' = "broken"
Fair == WF_vars(Next)
Eventually == <> (task = "done")
====
```

`.invariant/invariant.json`:

```json
{
  "name": "Task lifecycle",
  "language": "python",
  "code": ".",
  "module": ".invariant/specs/Lifecycle.tla",
  "conformance": "driver.py",
  "existing": ["tasks/lifecycle.py"],
  "components": {
    "scope": ["tasks"],
    "checks": [{
      "target": {"source": "tasks/lifecycle.py", "symbol": "advance"},
      "tests": [{"source": "tests/test_tasks.py", "symbol": "TaskTests.test_step"}],
      "conformance": [{"source": "verification/lifecycle/driver.py", "symbol": "drive"}],
      "invocations": [
        {"id": "unit", "kind": "test", "check": {"source": "tests/test_tasks.py", "symbol": "TaskTests.test_step"}, "command": "python", "args": ["-m", "unittest", "tests.test_tasks.TaskTests.test_step"], "working_dir": ".", "inputs": ["tasks/next.txt"]},
        {"id": "sample", "kind": "conformance", "check": {"source": "verification/lifecycle/driver.py", "symbol": "drive"}, "command": "python", "args": ["driver.py"], "working_dir": "verification/lifecycle", "sampling": {"runs": "2", "steps": "4", "seed": "1"}, "inputs": ["verification/lifecycle/.invariant/specs/Lifecycle.tla", "verification/lifecycle/.invariant/ratified.lock"]}
      ]
    }]
  }
}
```

For the omitted-components variant, remove the entire `components` member and its preceding comma. Keep `existing`, which may also select directories containing Python sources. The gate discovers lexical bindings for execution, runs `unittest` discovery of `test_*.py` files from the application root and executes the manifest driver with default maximum budgets 1000 runs / 40 steps / seed 1. It creates no component inventory or coverage for neighbors. Explicit configurations require an executable invocation for every selected test/driver; they never fall back to discovery.

Start a **new, unratified** `.invariant/ratified.lock` with the following proposal. This example supplies no decision, ratifier or pinned hashes:

```json
{
  "bounds": {"Limit": "2"},
  "statements": [
    {"name": "Spec", "kind": "spec", "says": "Runs start pending and follow the lifecycle."},
    {"name": "Safe", "kind": "invariant", "says": "Task state is pending, running or done."},
    {"name": "Seen", "kind": "witness", "says": "Completion is reachable."},
    {"name": "Bad", "kind": "bug", "expect": "Safe", "says": "The invariant catches a broken task state."},
    {"name": "Fair", "kind": "fairness", "says": "Continuously enabled lifecycle progress eventually occurs."},
    {"name": "Eventually", "kind": "property", "says": "Every fair run eventually completes."}
  ]
}
```

A human reviews the model, bounds, statements, selected code and driver before ratifying. In the adopting repository, record a proposed decision with `invariant decisions decide -door two-way -status proposed "Adopt the lifecycle model and bounds after human review."`; use the actual returned decision ID as the new lock's `decision`. After review, the human runs `invariant pin app/verification/lifecycle` to pin the reviewed statements, which prints the lock's proposal hash, and `invariant decisions ratify -by @YOUR_LOGIN -proposal sha256:HASH D-NNNN`, substituting their own login, that hash and the real decision ID. The lock holds only for the proposal that ratification names, so the gate, `invariant decisions check` and CI's ratification check fail it if its statements or bounds change afterwards, or if its decision was only recorded as ratified, or another decision supersedes it ([D-0208](../decisions/journal/D-0208.jsonl), [D-0219](../decisions/journal/D-0219.jsonl)). A ratify line that names a proposal needs an Invariant CLI built from #352's change or later, since an older one refuses a journal line with a field it doesn't know. An agent may record that ratification only when that person explicitly asks. Do not copy a fixture identity or another project's hashes, invent a ratifier, or repin an existing ratified lock without its authorizing decision.

With host Python 3 and Docker available, run:

```sh
invariant verify -out receipt app/verification/lifecycle
```

The command writes `receipt/receipt.json`, `receipt/receipt.md` and, in `receipt/traces/`, each counterexample TLC found, such as the known bug's. Passing yields exit 0; a failed check yields a receipt and exit 1; unsupported preflight or setup failure yields exit 2. TLC checks Init and Next for the authenticated observations at `Limit = 2`, in batches for larger samples. The receipt shows actual executions, selected versus actual launcher settings, model/includes, dependencies, resources, observations and independent outcomes. Failed/skipped/unexecuted tests, runtime/import errors or invalid/missing observations fail, as do pin, ratification or design obligations. Successful independent evidence survives. Component rows say origin `gate` or `supplied`; public attribution always treats incoming data as supplied and cannot change the gate verdict. Neither origin is an authorization token. A component's proof row stays unchecked unless a check selects it for [proof in place](#proving-a-function-in-place) and the proof qualifies, so unselected neighbors always do; assumptions stay assumed, and runtime tests confer no target coverage or invented assertions.

This is **sampled tested-against-the-model evidence**: exhaustive design checking does not make the implementation exhaustive, prove it, check every attempted step or cover every size. To test the application in every state it reaches, explore it instead, as below. Fractions and unsupported observation values fail TLC encoding explicitly; integer values are preserved exactly. D-0183's collector trust boundary and stateless handles remain as described below.

The gate/receipt execution policy is recorded as [D-0185](../decisions/journal/D-0185.jsonl), and this real suite passed on #328's final head:

```sh
INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify ./internal/conformance ./cmd/invariant -run '^TestPythonApplicationGate' -count=1 -timeout 45m
```

Default acceptance checks execute actual local Python with Docker transport and TLC doubles, and claim no real Docker/TLC validation. They run under Python 3.12 or newer; below it, the trusted-contract and exploration-execution cases skip, since the runner needs 3.12. The opt-in command requires real Docker, TLC and wheel installation and fails if prerequisites are absent. CI runs it, with every other Python opt-in set, on each pull request and push to main whose change can affect it ([D-0286](../decisions/journal/D-0286.jsonl), [D-0291](../decisions/journal/D-0291.jsonl)).

`TestPythonApplicationGatePrivateMounts`, added under [#399's ratified plan](../.invariant/plans/issue-399.json), runs automatically in the existing Linux integration shards, with no environment opt-in ([D-0197](../decisions/journal/D-0197.jsonl)). It runs the standard-library application fixture with explicit components through real Docker Python and TLC: the honest application passes, and an invalid transition produces TLC's `BadStep` after both the Python tests and driver execute successfully. This exercises access to the private mounts through the real gate. To run it alone:

```sh
go test -tags integration ./internal/verify -run '^TestPythonApplicationGatePrivateMounts$' -count=1 -timeout 20m
```

### Exploring every state

An exhaustive manifest, `"exhaustive": true`, makes each conformance invocation an exploration, which the gate's own harness does ([#342](https://github.com/gitdek/invariant/issues/342)). The driver calls `invariant_observe.explore(start, steps)` once. `start` makes a fresh lifecycle with `functional`, `instance` or `module` and returns it unentered; a list of starts gives several initial states. Each of `steps` is a `Step(name, take, args=None, *, refusal=None)`, named exactly as the model's `Next` names it, with every argument list to try in the spec's encoding. `take(life, state, *args)` calls `life.step(...)` once, through the application entry that takes the step, or, for modules, `life.hold(...)` or `life.release(...)` as described below. It returns without an operation only where the environment's bounds rule the step out; `state` is a copy of what the gate read back. For every state the application reaches, and every step and argument list, the harness makes a fresh lifecycle, replays through the application the path that first reached the state, and has `take` try the step. Every state is read back from the application, never from the driver.

Here is an unchanged counter, with a sibling and a resource:

```text
app/
  counter/__init__.py
  counter/core.py
  counter/step.py
  counter/step.txt
  counter/service.py
  tests/__init__.py
  tests/test_counter.py
  verification/counter/
    driver.py
    .invariant/invariant.json
    .invariant/ratified.lock
    .invariant/specs/Counter.tla
```

`counter/__init__.py` and `tests/__init__.py` are empty, and `counter/step.txt` contains `1`. The selected target, `counter/core.py`:

```python
from .step import next_count

def increment(count):
    return next_count(count)
```

`counter/step.py`:

```python
from pathlib import Path

def next_count(count):
    return count + int(Path(__file__).with_name('step.txt').read_text())
```

`counter/service.py` holds the entries the application calls it through:

```python
from .core import increment

def start():
    return 0

def inc(count):
    return increment(count)

def peek(count):
    return count
```

`tests/test_counter.py`:

```python
import unittest
from counter import service

class CounterTests(unittest.TestCase):
    def test_inc(self):
        self.assertEqual(service.inc(0), 1)
```

The model names its steps, as an exploration needs. `Limit` is the environment's bound: the counter takes no size, and the environment increments it at most `Limit` times.

```tla
---- MODULE Counter ----
EXTENDS Naturals
CONSTANT Limit
VARIABLE count
vars == <<count>>
Init == count = 0
Inc == count < Limit /\ count' = count + 1
Done == count = Limit /\ UNCHANGED count
Next == Inc \/ Done
Spec == Init /\ [][Next]_vars
Safe == count <= Limit
Full == count = Limit
====
```

`verification/counter/driver.py` tries `Inc` everywhere but where the environment has incremented `Limit` times, and `Done` everywhere. `peek` returns the count without calling the target, so where the model forbids `Done`, the application refuses it:

```python
from invariant_observe import Step, explore, functional
from counter.core import increment
from counter import service

Limit = 2

def start():
    return functional(increment, service.start, argument='count', field='count')

def inc(life, state):
    if state['count'] >= Limit:
        return
    life.step(service.inc)

def done(life, state):
    life.step(service.peek)

STEPS = [Step('Inc', inc), Step('Done', done)]

def drive():
    explore(start, STEPS)

if __name__ == '__main__':
    drive()
```

`.invariant/invariant.json`:

```json
{
  "name": "Counter",
  "language": "python",
  "code": ".",
  "module": ".invariant/specs/Counter.tla",
  "conformance": "driver.py",
  "exhaustive": true,
  "existing": ["counter/core.py"],
  "components": {
    "scope": ["counter"],
    "checks": [{
      "target": {"source": "counter/core.py", "symbol": "increment"},
      "tests": [{"source": "tests/test_counter.py", "symbol": "CounterTests.test_inc"}],
      "conformance": [{"source": "verification/counter/driver.py", "symbol": "drive"}],
      "invocations": [
        {"id": "unit", "kind": "test", "check": {"source": "tests/test_counter.py", "symbol": "CounterTests.test_inc"}, "command": "python", "args": ["-m", "unittest", "tests.test_counter.CounterTests.test_inc"], "working_dir": ".", "inputs": ["counter/step.txt"]},
        {"id": "explore", "kind": "conformance", "check": {"source": "verification/counter/driver.py", "symbol": "drive"}, "command": "python", "args": ["driver.py"], "working_dir": "verification/counter", "sampling": {"runs": "200", "steps": "10", "seed": "1"}, "inputs": ["verification/counter/.invariant/specs/Counter.tla", "verification/counter/.invariant/ratified.lock"]}
      ]
    }]
  }
}
```

In an exploration, `runs` bounds the lifecycles, one for each state and step, and `steps` the steps each one takes, its replay and its attempt, so it must be at least the model's depth. They bound the run at the ratified bounds only: the run one size larger has the most a declaration may give, and its time allowance bounds it instead. The lock's proposal has bounds `{"Limit": "2"}` and statements `Spec`, `Safe` and `Full`, reviewed, pinned and ratified as for sampling, and `invariant verify -out receipt app/verification/counter` runs it.

At `Limit = 2` the harness reaches counts 0, 1 and 2 in six runs: five attempts, three of them refusals, and `Inc` left untried at 2, where only the environment's bound rules it out. TLC checks the starting state against `Init`, every attempt against `Next`, and that every step was tried in every state but where only the environment's bounds rule it out, by evaluating each step again with those bounds one larger. Configured tests run and are recorded as for sampling, as their own outcomes. The gate passes only when the states reached are the model's, here 3, and every step was tried.

The driver names its bound, so the check one size larger applies, as it does to every exhaustive driver. TLC runs with `Limit = 3`, and once it finishes there, the exploration runs again with a private copy of the driver whose `Limit = 3`, mounted read-only over the staged driver, in the same staged snapshot and dependency image. Its 4 states and depth 4 must match TLC's. A counter that stopped at 2 by itself would pass at the ratified bounds and fail here: it carries a bound. If TLC can't finish one size larger, finds a problem in the model there, or the exploration doesn't finish within twice TLC's allowance, nothing is claimed and nothing fails. A driver that names no bound has no check one size larger.

What each result earns:

- **Tested in every state** (component `exhaustive_states`): the states reached equal the model's, with a passing attempt check.
- **Every step tried** (`every_step`): a passing, nonempty attempt check, about tried steps in the states reached, not proof.
- A failed attempt check fails the gate and blocks both labels; the transitions TLC checked stay checked. A state the application never reaches fails its exhaustive claim the same way, and a sampled run never earns either label, even one that visits every state.
- The run one size larger is recorded apart from the ratified bounds' results, in `application.larger` and `larger`: its driver copy and hash beside the staged driver's, its bounds, its execution, and how its counts compare.
- Uncovered components stay unchecked, tests confer no target coverage, and nothing here is a proof or a claim at every size.

Only in an explored run may an entry refuse a step without calling the selected target ([D-0200](../decisions/journal/D-0200.jsonl)). Without a refusal declaration, the entry must return normally with the state read back unchanged, and any application exception fails the run. A start or replay that doesn't reproduce its state, a `take` that performs two operations or uses another run's lifecycle, a lifecycle made outside a start, a forbidden handle attribute access and an exploration that never calls the selected target all fail with no accepted observations. `functional` and `instance` steps take no arguments, as in sampling; `module` steps forward them. Several starts need distinct initial states; a start whose initial state another start reaches tries that state's steps itself, whichever start comes first ([D-0207](../decisions/journal/D-0207.jsonl)). Omitting components works as for sampling, with the invocation named `explore` at the default budgets.

For an explored entry that refuses by raising an exception, declare exactly which exception counts ([#392](https://github.com/gitdek/invariant/issues/392), [D-0279](../decisions/journal/D-0279.jsonl)):

```python
from invariant_observe import Refusal, Step

Step('Submit', submit, [[1]], refusal=Refusal(app.Refused, status_code=409))
```

`Refusal(exception_type, **attributes)` requires an `Exception` subclass and matches its exact class, not subclasses. Each declared attribute must be present and match both type and value: only built-in `None`, `bool`, `int`, finite `float` and `str` values are allowed, without coercion or subclass values. A declaration with no attributes matches the exact class alone. Malformed declarations fail before exploration. The exception must escape the actual application entry through its own call stack, including ordinary helpers, or through its returned awaitable, before any selected-target call or application-thread start during that attempt, even a thread already joined. The state read back must be unchanged. An exception from a detached task cannot be a refusal, and a failed detached task fails the run. `functional` and `instance` support synchronous refusals; `module` also supports async entries. Unmatched exceptions, changed state and calls to the selected target before raising fail the run. A normal return is an accepted outcome, including a read-only return that leaves state unchanged. Replays must reproduce the outcome as well as the state. Sampled execution keeps its existing rules.

For a declared step, TLC accepts a refused attempt only when that exact named action, with the attempted arguments, is disabled in the before-state. Every normal return must satisfy the exact named action's before/after relation, including a stuttering action that leaves the state unchanged; another branch of `Next` cannot justify it. Undeclared steps retain their existing checks. Declared refusals are attempted steps, while returning from `take` without a lifecycle operation is still an omission and is allowed only where the environment's bounds rule the step out.

Explored executions have `observation_mode: "explored"` and an `exploration` summary: the declared steps with each entry identity, and the runs, states, depth, attempts, refusals and omitted steps, counted from the authenticated rows. Initial rows add `start`; step rows add `step`, `args`, `replay` and `called`; an `omitted` row records a step left untried. A step with a refusal declaration also records `refusal: {"type": "module.QualifiedName", "attributes": {...}}`. Its transition rows add `outcome: "accepted"` or `"refused"`, and only refused rows carry the matching `refusal` descriptor and `called: false`. Only the declared exception attributes are captured. The summary's `declared_refusals` counts these exceptions separately and is absent when no step declares one; `refusals` includes them plus unchanged legacy attempts, but excludes declared normal read-only returns. Counts exclude replays. JSON and Markdown receipts retain the exception type, exact scalar attributes and outcomes, including large integers, and fingerprints bind them. The exploration policy is recorded as [D-0199](../decisions/journal/D-0199.jsonl), refining D-0185, and D-0207 refines it for several starts and the larger run's budgets. The whole #313 matrix, sampled and explored, runs with real Docker, Python, TLC and the installed wheel, and passed on #351's final head:

```sh
INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify ./internal/conformance ./cmd/invariant -run '^TestPythonApplicationGate' -count=1 -timeout 60m
```

## Existing Python application foundation

[#310's ratified plan](../.invariant/plans/issue-310.json) admits Python `existing` manifests with a conformance driver. `project.ResolvePythonApplication` returns an absolute `Root` and slash-separated `ProjectPath`: exactly one strict ancestor of the verification directory must contain every named regular `.py` source or directory containing Python sources. Requirements and Python markers are optional and never select among ambiguous roots. Missing selections, ambiguous roots, unclean paths, absolute paths, parent escapes, backslash aliases, selection `.` and symlinks fail, including links from root to the verification directory.

Only this new mode uses application-relative component scopes, references, assumption sources, invocation working directories and inputs. Manifest model, conformance and code paths stay contained and verification-project-relative. Python lexical identities and exact contract bytes are unchanged, with IDs based on application-relative sources. Scope remains nonrecursive and associated tests and drivers remain input provenance. Legacy Python and TypeScript retain their discovery and identity rules; their work-tree copies use the Git selection described below within their existing copy boundaries.

Under [#391's ratified plan](../.invariant/plans/issue-391.json), `verify.StagePythonApplication` selects files beneath the application root with `git ls-files --cached --others --exclude-standard -z` when that root is inside a Git work tree, including a linked worktree or a directory below the repository root. It sorts and deduplicates the NUL-delimited paths, copies their original working-tree bytes and layout, and omits deleted tracked files. Tracked files remain selected even if an ignore pattern matches them. Nonignored untracked verification files, tests, sibling code and resources are included; ignored untracked payloads are omitted. Outside Git it selects the whole directory, without applying ignore rules. Git failures inside a repository fail staging rather than selecting the whole directory. A root reached through a symlink is placed by its real path. Both modes exclude Git metadata and `.venv` and `__pycache__` directories, and refuse selected symlinks, symlink parents and nonregular files. A tracked file replaced by a directory stages the files Git lists under it. Submodules under the application are refused by name, including uninitialized submodules, and so are nested repositories.

Before image selection, staging refuses by name any ignored explicit `existing` selection, `environment` script, verification project, component source reference or invocation input, including trusted-contract inputs, so an application root its repository ignores is refused too. Naming an ignored directory fails; ignored descendants not explicitly named are omitted from an admitted directory. Existing staging exclusions still refuse explicit declarations. `StagePythonApplication` requires a missing or empty destination disjoint from the application, with no symlink path components but the platform's temporary directory and its ancestors, some of which are links on macOS. Errors return no workspace; callers own and remove the destination, including partial copies.

The workspace exposes `Root`, the reloaded staged `Project`, an `Inventory` resolved from staged bytes, sorted `Files` hashing every copied file, and `FileSelection`, serialized as `file_selection`: `git` or `directory`. Each copied file also belongs to explicit inventory provenance, without adding production scope. Session provenance and fingerprints bind the selection method and every staged file, including admitted siblings, resources, tests and drivers. JSON receipts retain `file_selection`; Markdown explains “git ls-files” or “whole directory” for ordinary executions and runs one size larger. Fingerprints contain no host paths and remain stable across relocation with the same files and selection method; later original edits cannot alter the snapshot. Dependency selection, tests, conformance, proof inputs and exploration one size larger consume that same snapshot; only the larger run's private rewritten driver overlays its staged path.

Under [#400's ratified plan](../.invariant/plans/issue-400.json) ([D-0285](../decisions/journal/D-0285.jsonl)), Python application receipts count and hash every staged regular file at or beneath each `existing` selection, including files in `bin/` and `out/`. The JSON `existing` entries and Markdown “Existing code” row use those counts and hashes. Each SHA-256 hashes files in sorted application-relative path order, framing each as its path and newline, decimal byte length and newline, then original bytes. Files omitted from the snapshot contribute nothing; edits to any included file change its selection's digest.

The shared `copyTree` copier applies the same Git selection within its existing exclusions, and skips a selected symlink or other nonregular file as it does outside Git. TypeScript's `StagePackage` keeps its fixed package metadata and explicit `existing` boundaries, filters directory descendants by Git selection and refuses ignored explicit files. `HashExisting` hashes and counts those selected bytes, retaining its directory exclusions, including `bin/` and `out/`. Outside Git, and in a root the repository ignores with nothing tracked under it or with all tracked ordinary files deleted from the work tree, these copiers select the whole directory within their existing exclusions, as for `synthesize`'s default output under the ignored `out/`. A surviving tracked entry keeps Git selection, as does a nonignored root. Git failures, submodules and nested repositories still fail staging; strict Python application staging still refuses ignored declarations. The TLA-only staging path is unchanged. These changes build on [D-0054](../decisions/journal/D-0054.jsonl), [D-0180](../decisions/journal/D-0180.jsonl), [D-0181](../decisions/journal/D-0181.jsonl) and [D-0199](../decisions/journal/D-0199.jsonl), refining the Git selection policy in [D-0278](../decisions/journal/D-0278.jsonl).

Go and TypeScript reports expose optional `FileSelection`, serialized as top-level `file_selection`, for the root actually staged: Go's module, standalone TypeScript's project or existing TypeScript's containing package. Its value is `git` or `directory`, including `directory` for the ignored-root fallback above. Markdown explains “git ls-files” or “whole directory”, and the report fingerprint binds the method. Model-only reports omit it; older reports without selection evidence do not infer a method when rendered.

`PythonApplicationWorkspace.SelectImage(ctx)` uses `toolchain.PythonDepsImage` on the staged application root and its recursive requirements inputs. Verification-local requirements and later original edits cannot replace them. Without root requirements it returns `toolchain.PythonImage`, nil dependencies and no Docker call. Otherwise it returns the immutable dependency image and existing original/canonical input and artifact evidence, preserving validation, isolation and cache reuse. Errors yield no image and never fall back; no new installer or application command runs.

An application that runs as `uv run serve.py` declares its dependencies in the script, in a [PEP 723](https://peps.python.org/pep-0723/) `# /// script` block, and uv ignores any `requirements.txt` beside it, which may belong to the application's tools. For such an application, the manifest names the script as `environment`, relative to the application root, and `SelectImage` builds the environment from the block instead, through the same preflight, pinned pip parser, canonical form, installer and wheel evidence ([#320](https://github.com/gitdek/invariant/issues/320)). The block must be the script's only `script` block and declare at least one dependency. Its `requires-python` must admit the pinned Python, and `[tool.uv]` settings, which can change resolution, are refused rather than ignored. The receipt names the script and its block's digest. The image's identity follows the block, so editing the rest of the script reuses it. The application's files never change.

`verify.PreparePythonApplicationSession(ctx, p, dst, PythonApplicationSessionOptions{})` prepares a directly callable test and sampled-observation adapter under [#313's ratified child plan](https://github.com/gitdek/invariant/issues/313#issuecomment-6536681769) (`sha256:0380e0c90425c12cb24fed241112820b07758fecb6d812130b71424a857d2366`) and [#315's implementation plan](../.invariant/plans/issue-315.json). It validates every invocation before image selection, stages the selected application files, selects one immutable dependency image (or acquires the pinned standard-library base), and reuses it for every `session.Run(ctx, key)`. `Invocations()` and `Provenance()` return detached views; keys include target, check, kind and ID. The destination follows `StagePythonApplication`'s ownership rules. `Close()` releases private runtime/output resources, is idempotent and prevents further runs; callers still remove the destination, including partial copies after preparation failure.

Explicit tests require literal command `python` and ordered arguments `-m unittest` followed by explicit dotted selections, with no discovery, options or implicit suite. Safe environment values are preserved exactly; interpreter, import, loader, proxy and adapter controls are reserved. Tests have effective `runs=1` and no declared sampling. Under [#318's ratified plan](../.invariant/plans/issue-318.json), conformance executes the configured script as `__main__` from its declared working directory, with literal ordered argv. Its path must resolve to both the manifest driver and the selected conformance source. The selected driver declaration must actually execute; the adapter never implicitly calls `drive()`. Decimal `runs`/`steps` in 1–2147483647 are maximum lifecycle/step budgets, and `seed` in 0–2147483647 seeds Python's random generator (defaults 1000/40/1). Exceeding a budget fails. Declared controls and effective settings remain separate. Proof invocations are rejected.

The staged runtime supplies `invariant_observe`. A functional lifecycle calls a staged application initializer, owns its scalar return as current state, passes it as the first argument to the surrounding application entry, and reads the selected target's actual argument and return:

```python
from invariant_observe import functional
from tasks.lifecycle import advance
from tasks import application

def drive():
    with functional(advance, application.start, argument="state", field="task",
                    expected_initial={"task": "pending"}) as life:
        life.step(application.tick, expected_before={"task": "pending"},
                  expected_after={"task": "running"})

if __name__ == "__main__":
    drive()
```

Here `application.start()` returns `"pending"`, and `application.tick(state)` returns `advance(state)`. These are unchanged application functions outside the driver. The entry must be distinct from the selected target and execute exactly one selected call; its returned state must equal that call's result. Functional state is value-semantic: strings, booleans, finite numbers or `None`, with no mutable-object identity claim.

For ordinary instances, declare state-key-to-attribute readers instead. For example, `Task.advance` updates `self.state`, and `Application.tick` calls that method on its task:

```python
from invariant_observe import instance
from tasks.application import Task, Application

def drive():
    task = Task()
    with instance(task.advance, fields={"task": "state"},
                  expected_initial={"task": "pending"}) as life:
        life.step(Application(task).tick, expected_before={"task": "pending"},
                  expected_after={"task": "running"})

if __name__ == "__main__":
    drive()
```

The gate reads scalar attributes directly from that receiver's own `__dict__` initially and at call entry/return, and checks the final entry state. The observed receiver must be the exact bound instance. The collector retains the original receiver independently of the lifecycle handle; replacing its target or receiver fails even if the replacement has the same state. Contexts may coexist and interleave; each gets an ordered run number and an execution-local instance number, with initial sequence 0 and steps 1 onward. Each context must close after at least one step. Initial/pre/post expected dictionaries are optional comparisons, never evidence or state overrides. State must continue from the preceding post-state, including when expectations are omitted.

The `functional` and `instance` forms support only ordinary undecorated synchronous module functions and instance methods with staged lexical code and intact module/class bindings. An explicitly selected instance wrapper outside the `existing` files must directly import a selected source and call its intact staged module function inside every observed target step; an import alone earns no evidence. This preserves existing instance sessions without admitting unrelated targets. These forms refuse async/generators, decorators, concurrency and mutable state, and require literal-scalar defaults on targets and entries. All forms refuse closures, wrappers, substituted code or bindings, unbound/static/class methods, custom metaclasses or attribute access, slots, property/descriptor readers, callback readers and non-JSON evidence. Reentrant/nested selected calls, missing calls outside the explored exceptions, multiple selected calls, cross-instance substitution, empty/unclosed/reused contexts, import/runtime failures and caught observation errors cannot produce accepted evidence. Application source is never rewritten.

[#321's ratified plan](../.invariant/plans/issue-321.json) adds `module(target, *, fields, expected_initial=None, timeout=5.0)`. It reads copied JSON state from the selected staged module function's own globals. Targets and distinct staged module entries outside the driver can be synchronous or async. Registration decorators are accepted only when the live binding remains the lexical function itself; the collector never unwraps. `life.step(entry, *args, expected_before=None, expected_after=None, **kwargs)` forwards arguments solely as inputs. Module entries may also have arbitrary object defaults, including framework declarations such as `Form('')`: the original positional default tuple, keyword default mapping and each keyword default value must remain unreplaced, including before the first step and between steps. These objects supply no evidence. Targets still require literal-scalar defaults.

For a complete standard-library web-shaped example, the unchanged application `tasks/lifecycle.py` can register an async submission handler and start a worker:

```python
import asyncio
import threading
import uuid

tasks = {}
routes = {}

def register(fn):
    routes[fn.__name__] = fn
    return fn

@register
def advance(key):
    tasks[key]["status"] = "done"

@register
async def submit(upload, *, content_type="application/octet-stream"):
    assert upload is not None and content_type
    await asyncio.sleep(0)
    key = uuid.uuid4().hex
    tasks[key] = {"status": "reserved"}
    threading.Thread(target=advance, args=(key,)).start()
    return {"task": key}
```

Select `advance` as the target and use this conformance driver:

```python
from invariant_observe import module
from tasks import lifecycle as app

def drive():
    with module(app.advance, fields={"status": "tasks[*].status"},
                expected_initial={"status": []}, timeout=5.0) as life:
        life.step(app.submit, object(), content_type="image/png",
                  expected_before={"status": []},
                  expected_after={"status": ["done"]})

if __name__ == "__main__":
    drive()
```

Paths start with an ASCII identifier (`[A-Za-z_][A-Za-z0-9_]*`) naming a module global, followed by any number of `.identifier` dictionary keys, `[nonnegative decimal index]` list indices or `[*]` projections. For example, `tasks[*].status`, `rows[0].value` and `meta` are valid. Projections traverse exact dictionary values in insertion order or exact list elements and return a list of the remaining suffix, including `[]` for no elements. Leaves are deeply copied exact dictionaries with string keys, lists and finite JSON scalars. Missing keys/indices, wrong container types, subclasses, cycles, malformed paths, attributes, coercions, evaluation and callbacks fail. Expectations compare the entire copied state with type-sensitive equality; they never supply state.

A normal module step reads before the entry, checks continuity, then reads after the entry and all threads it started (including descendants) have finished. Reservations and worker mutations belong to that step; intermediate target-entry/return state need not match. Except for the explored zero-call cases above, exactly one completed, nonrecursive selected call must occur beneath the entry, its returned awaitable or an owned thread. Coroutine resumptions count as one call. Awaitables run in a fresh event loop that closes afterward; unfinished coroutines (including manually suspended or retained unstarted coroutines), failed auxiliary tasks and cancellations fail. The gate finds unfinished coroutines by scanning the heap with exact type comparisons made in C, so the scan runs no application code and costs a step no trace or profile event per heap object (#796). Ownership follows thread objects and start events, never reusable IDs or timing alone. A step waits for a worker once its `Thread.start` has returned, so a thread another worker is still starting is waited for through that worker, however long the start takes (#405). Preexisting, unrelated and driver-started threads fail, even if they never call the target. One total deadline covers the entry, awaitable and draining every owned thread: `timeout` must be a positive finite number, defaults to 5 seconds and cannot exceed 60. A separate watchdog process terminates the disposable runner with a diagnostic and no accepted observations on timeout, even while application code holds the interpreter's GIL, for non-daemon workers or for code catching cancellation. The deadline starts once the watchdog is running, so the watchdog's own start on a loaded machine spends none of the budget. A run a deadline stops fails as a runner that stopped before sealing its observations, not as a collector authentication failure (#645, [D-0356](../decisions/journal/D-0356.jsonl)). Module contexts may interleave, using module identity for instance numbering and the same continuity rules as other forms.

An explored module can observe a reservation separately from its worker's completion with `life.hold(entry, *args, expected_before=None, expected_after=None, **kwargs)` and `life.release(*, expected_before=None, expected_after=None)` ([#393's ratified plan](../.invariant/plans/issue-393.json), [D-0284](../decisions/journal/D-0284.jsonl)). Each is one operation inside a `take`, with the same budgets, binding, continuity and expectation checks as `step`. Only module exploration admits them; sampled, functional and instance execution refuse them. All application arguments, including a keyword named `hold`, pass through to the entry.

`hold` pauses the first selected frame on an owned worker at its first event, before any target code runs. The entry, its returned awaitable and every other owned thread must finish before the reservation is read back. A target on the entry's own stack cannot be held, and a call completed without the gate's pause cannot be held afterward. A hold that starts no selected call fails unless the entry produces a valid declared refusal. Ordinary steps and declared refusals may intervene while a worker is held; a refusal starts no new work and leaves the previous hold intact. Any second selected start while held fails the entire execution, including from an ordinary step or when the driver catches the error.

`release` invokes no application entry: it resumes that same frame and drains its worker and descendants under one fresh module deadline before reading the result. With nothing held, it is the unchanged legacy refusal under [D-0200](../decisions/journal/D-0200.jsonl). A release step cannot declare a `Refusal`, since it has no new entry to raise one. Frame and thread-object ownership, coroutine identity, the independent watchdog and poisoning still apply. A run may end with work held; the harness resumes and drains it before the next start, with no cleanup observations. Failed or unfinished cleanup fails the whole execution and discards all observations. Worker events during release belong to that step; outside active operations and harness cleanup they poison execution. Replays reproduce operations as well as states and declared outcomes.

For example, a single-slot application's `submit` reserves `slot["status"] = "reserved"` and starts `advance` on a worker. `advance` changes the status to `"done"`; a busy `submit` raises `Busy` with `status_code = 409` before starting any work. Its `reset` restores `"idle"` at the start of a run. This driver exposes `Submit` and `Finish` to the model while leaving that application unchanged:

```python
from invariant_observe import Refusal, Step, explore, module
from service import jobs as app

def start():
    app.reset()
    return module(app.advance, fields={"status": "slot.status"},
                  expected_initial={"status": "idle"})

def submit(life, state):
    life.hold(app.submit, expected_before=state,
              expected_after={"status": "reserved"})

def finish(life, state):
    after = {"status": "done"} if state["status"] == "reserved" else state
    life.release(expected_before=state, expected_after=after)

def drive():
    explore(start, [Step("Submit", submit,
                        refusal=Refusal(app.Busy, status_code=409)),
                    Step("Finish", finish)])

if __name__ == "__main__":
    drive()
```

The model's `Submit` reserves an idle or completed slot and is disabled while reserved; `Finish` completes a reserved slot and is disabled otherwise. The reservation is observed before `advance` executes its first statement, and completion belongs to `Finish`. Only these transitions add `operation: "hold"` or `"release"` to the evidence. `called` counts starts: a successful hold is `true`, its release is `false`, and a declared refusal during hold is `false`. Release rows use the selected target and its source hash as `entry` and `entry_sha256`, including empty releases. Go independently checks operation, form, mode, held status, continuity and replay; changed state with `called: false` is allowed only for a matching held release. Ordinary, initial and omitted rows omit `operation`. JSON and fingerprints retain it, and attempts retain the model's step names and declared outcomes. These exceptions to the completed-call rule build on [D-0186](../decisions/journal/D-0186.jsonl), [D-0199](../decisions/journal/D-0199.jsonl) and [D-0200](../decisions/journal/D-0200.jsonl).

Module rows add `form: "module"` and retain exact paths in `readback`; initial entry is the target. Receipts retain raw JSON lists and dictionaries in execution observations. Sampled and explored TLC recordings and component observations share the same value semantics: lists recursively become ordered TLA+ sequences and dictionaries become ordinary records. Each component `from` and `to` remains a map of field names to TLA+ value strings, using the existing module-value conversion and conformance encoder ([#837's ratified plan](../.invariant/plans/issue-837.json)). An empty list becomes `<<>>`, and `["queued", "queued", "done"]` becomes `<<"queued", "queued", "done">>`; nested lists, duplicates, order and record fields survive. Booleans, strings and exact integers keep their types, so `9007199254740993` stays distinct from `"9007199254740993"`. Application keys never act as `$seq`, `$set`, `$fn` or `$mv` tags: keys that cannot encode as TLA+ record identifiers fail explicitly, as do `None`, fractions and other unrepresentable values.

A component state conversion error identifies the target, check, invocation, run, step, before/after side and field. It returns an error without committing partial component evidence or replacing a value with empty text, for module and non-module observations alike; raw execution evidence and prior component attribution remain intact. JSON round trips and safe literal Markdown retain the encoded values and stable fingerprints, and changing a list's contents, duplicates or order changes the component fingerprint. Non-module observations and attempted-step arguments keep their existing conformance encoding. Replay rows stay in raw executions and are omitted from component observations; transition, exhaustive-state and attempted-step qualifications remain independent. Recording conversion still streams to TLC through the existing file-backed path. Authentication, freshness, poisoning, sampling budgets, stateless handles and the assurance limits below apply to all three forms. This extends [D-0183](../decisions/journal/D-0183.jsonl) through the existing [D-0185](../decisions/journal/D-0185.jsonl) sampled gate, for both explicit and omitted components; #837 records the receipt repair to D-0170, D-0175, D-0199 and D-0333.

The module observation policy is recorded as [D-0186](../decisions/journal/D-0186.jsonl), refining D-0183, and both opt-in suites passed on #331's final head:

```sh
INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1 go test ./internal/verify -run '^TestPythonApplicationExecutionModule' -count=1 -timeout 30m
INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify -run '^TestPythonApplicationGateModuleObservations$' -count=1 -timeout 30m
```

Successful driver results have `observation_mode: "sampled"` and ordered `observations`, with an empty `tests` array. Each raw record includes `kind`, `run`, `instance`, `sequence`, the full invocation `key`, lexical `target`, `driver`, `symbol` and `entry`, their source hashes, and `readback` (`{"task":"$return"}` or `{"task":"state"}`). Initial records carry `state`; transitions carry actual `before` and `after`, for example `{"task":"pending"}` and `{"task":"running"}`. No assertions or test outcomes are invented. This remains sampled even if a driver imports a legacy exploration harness: The public gate now checks these observations with TLC and attributes actual results under #327. Exhaustive-state and attempted-step coverage need an exhaustive manifest and `invariant_observe.explore` (#342, above). Proof runs apart from this path, as [proving a function in place](#proving-a-function-in-place) says.

Go independently requires complete, nonempty, correctly bound output and exit 0. Invalid driver execution returns `Success=false`, diagnostics and no accepted observations. A fresh execution identity prevents replay even for the same session/key. The runner also authenticates the exact ordered collector stream with a per-execution HMAC key, consumed and removed before application imports and kept out of the application-readable request. Copying request bindings into fabricated output cannot earn success. Lifecycle handles hold no state, so a driver can't substitute evidence through them: every binding, read-back and continuity check uses the collector's own records. Reading handle attributes other than `step`, `hold`, `release` and the `with` protocol, or writing or deleting any handle attribute, poisons execution even if the driver catches the error. Access to `hold` and `release` does not admit their use outside module exploration. The trust boundary is the gate's own code. Python in the same process can always reach the collector's module, objects, frames and closures, so no in-process collector can rule out a driver that tampers with them. That driver's bytes are bound to every observation it makes, and authentication defends against output written or replayed outside the collector, not against such tampering. The authentication footer and execution identity never enter portable JSON or fingerprints. Provenance binds both runner and collector assets, the full staged tree and dependency evidence. Changes before, during or between executions invalidate observations without another image selection. Repeated and relocated identical runs have identical portable results. Successful drivers cannot alter, promote or replace independently observed runtime-test outcomes.

Under [#801's ratified plan](../.invariant/plans/issue-801.json), a failed Python execution can also carry `harness_error`: the first collector failure, retained before exception wrappers, caught errors, poisoned lifecycle operations or terminating cleanup can replace it. The collector writes this diagnostic separately from its observations. Go reads it only for a failed execution and normalizes it like `diagnostics`, replacing staging/runtime/output paths and volatile execution, image and container identities. The execution retains the full diagnostic context, including the first error's traceback and later failures. Older receipts may omit `harness_error`.

The affected conformance result and an actually executed run one size larger retain that cause. **What failed** identifies the target, check and invocation and shows the first cause; full tracebacks remain in the execution record rather than the short summary. Explicit-component and legacy application receipts render all application text literally, including diagnostics, names, links and markup. Neither `harness_error` nor process diagnostics enter the execution fingerprint or authorize observations: failed and incomplete streams still lose every observation, exploration count and observation mode. Collector errors are not application refusals. [D-0279](../decisions/journal/D-0279.jsonl)'s declared-refusal matching and named-action checks, rejection of undeclared application exceptions, and [D-0356](../decisions/journal/D-0356.jsonl)'s deadlines and stopped-before-sealing diagnostics remain unchanged. The observation and gate foundations remain [D-0183](../decisions/journal/D-0183.jsonl) and [D-0185](../decisions/journal/D-0185.jsonl).

The adapter uses absolute Python with site/user-site and implicit paths disabled, effective hash seed 0, trusted imports before application root and driver directory before dependency libraries, including namespace packages. The default executor uses offline linux/amd64 Docker, `--pull=never`, memory/process/CPU limits, a read-only root and input/runtime mounts, separate writable output, all capabilities dropped, no new privileges and a size-limited `/tmp` tmpfs with `nosuid,nodev,noexec`, with cancellation cleanup. `SelectImage` and `Execute` options replace those operations for orchestration or recording; `RunPythonApplicationCommand` exposes the same default executor. Since #342 a command also carries `Mode`, `explored` or `sampled`, which the request passes to the collector, and `Overlays`, files a custom executor must mount read-only over the staged paths they name, as the default does. `session.RunLarger(ctx, key, sizes)` runs an explored invocation again one size larger, with a driver copy as its overlay, and returns the copy's staged path and hash beside the execution. No image fallback, dependency installation or Nagini execution occurs on Run.

Before each application command, the executor queries the selected Docker daemon with `docker info --format '{{json .SecurityOptions}}'` ([#399's ratified plan](../.invariant/plans/issue-399.json), [D-0283](../decisions/journal/D-0283.jsonl)). Ordinary Docker runs with explicit `--user` set to the gate's effective host UID:GID. Rootless Docker uses `--user 0:0`, whose container root maps to the host daemon owner, so it can open that owner's private mounts. Failed or malformed discovery and rootful user-namespace remapping are refused with a diagnostic before any application container launches, without fallback. Directory and authentication-file permissions stay private; the identity choice preserves the isolation, image, interpreter, environment, overlays and cleanup described above. The application's Nagini proof runs as the same identity, for the same reason ([D-0287](../decisions/journal/D-0287.jsonl)).

Structured observations record actual unittest IDs separately from configured lexical checks, using the executed method's staged source and lexical location. Passed, failed, error, skipped, expected failure, unexpected success and not-run outcomes remain distinct; skipped cases retain their reason and are never exercised or passed. Missing, substituted, malformed, incomplete or unsuccessful execution cannot earn success, and valid preceding observations survive later failure. Provenance hashes every staged file and the runner and retains original/canonical dependency input hashes and actual wheel evidence. Result fingerprints bind declared and actual settings and ordered observations, excluding host paths, local image IDs and operational diagnostics. Snapshot content, membership, symlinks and replacements are checked around preparation and every execution. Original bytes stay unchanged; runtime-test records infer no assertions, target coverage, proof, conformance or project assurance.

Shared preflight, `verify.Run`, `Python.Check` and prepared sessions admit sampled and, since #342, exhaustive existing Python applications, with or without components. Preflight and `verify.Run` admit a proof selection in the form [in-place proof](#proving-a-function-in-place) supports, beside conformance on the same function or an `observe` selection on another check with exhaustive conformance, and refuse any other; `Python.Check`, which returns no proof, refuses every one. Unknown sampling keys, missing selected invocations and mismatched drivers fail before verification tools or dependency installation; malformed explicit configurations never fall back to legacy execution. So does a [trusted contract](#trusted-dependency-contracts) whose backing test has no executable invocation. Model-only checking, supplied attribution, legacy Python/marked files and TypeScript retain their behavior. [D-0181](../decisions/journal/D-0181.jsonl) records the preceding session/runtime-test policy. The observation/execution policy, with its trust boundary, is recorded as [D-0183](../decisions/journal/D-0183.jsonl), and `INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL=1 go test ./internal/verify -run '^TestPythonApplicationExecution' -count=1 -timeout 30m` passed on #323's final head. The suite runs meaningful actual-Python checks without Docker and enables real Docker only explicitly, failing on missing prerequisites.

The direct adapter also builds on [D-0013](../decisions/journal/D-0013.jsonl), [D-0029](../decisions/journal/D-0029.jsonl), [D-0047](../decisions/journal/D-0047.jsonl), [D-0170](../decisions/journal/D-0170.jsonl), [D-0171](../decisions/journal/D-0171.jsonl) and [D-0180](../decisions/journal/D-0180.jsonl). This foundation builds on [D-0054](../decisions/journal/D-0054.jsonl), [D-0158](../decisions/journal/D-0158.jsonl), [D-0168](../decisions/journal/D-0168.jsonl), [D-0177](../decisions/journal/D-0177.jsonl), [D-0178](../decisions/journal/D-0178.jsonl), [D-0122](../decisions/journal/D-0122.jsonl).

## Component configuration and evidence in ordinary Go and Python

The first slice of [#212](https://github.com/gitdek/invariant/issues/212) adds optional `components` metadata to `.invariant/invariant.json`. It inventories Go source and resolves intended checks. [#304's ratified plan](../.invariant/plans/issue-304.json) extends discovery to explicit Python scopes. [#234](https://github.com/gitdek/invariant/issues/234) adds an API to attribute supplied proof results to those components and render their evidence. [#257](https://github.com/gitdek/invariant/issues/257) retains target assumptions, inert invocation declarations and exact input provenance in that inventory. [#274's ratified plan](../.invariant/plans/issue-274.json) adds independent supplied assumption records and optional actual proof provenance. [#279's ratified plan](../.invariant/plans/issue-279.json) adds independent supplied conformance results, with separate qualifications for checked transitions, exhaustive states and tried steps. **Sampled and explored existing Python applications support explicit component verification.** Other explicit code configurations return `verify.ErrComponents` before verification tools. Python syntax discovery may run its isolated parser. Model-only checking through `verify.RunModel` remains available. Omitting `components` preserves legacy verification and infers no component inventory. Python manifests with nonempty `existing` support discovery and staging, plus directly callable test and sampled driver sessions with explicit components under #315 and #318. The public gate supports these manifests, sampled or explored, with or without components; omitted components create no inventory.

For example, a Go manifest can include:

```json
{
  "components": {
    "scope": ["core"],
    "checks": [
      {
        "target": {"source": "core/ops.go", "symbol": "(*Counter).Step"},
        "proof": {
          "contracts": [{"source": "core/ops.go", "symbol": "(*Counter).Step"}]
        },
        "tests": [{"source": "core/ops_test.go", "symbol": "TestStep"}],
        "conformance": [{"source": "drivers/explore.go", "symbol": "Drive"}],
        "assumptions": [
          {
            "id": "serialized",
            "statement": "Calls to Step are serialized.",
            "reason": "The caller holds the counter lock.",
            "source": "notes/caller-contract.md"
          }
        ],
        "invocations": [
          {
            "id": "seeded",
            "kind": "conformance",
            "check": {"source": "drivers/explore.go", "symbol": "Drive"},
            "command": "go",
            "args": ["run", "./drivers", "--seed", "7"],
            "working_dir": ".",
            "env": {"MODE": "test"},
            "sampling": {"seed": "7", "runs": "3"},
            "inputs": ["data/fixture.json", ".invariant/specs/Counter.tla", ".invariant/ratified.lock"]
          }
        ]
      }
    ]
  }
}
```

The component path base is the application root for Python manifests with nonempty `existing`, and the verification project otherwise. In the following rules, project-relative component paths use that base. `scope` names a nonempty list of package directories relative to that base (`.` names its root), each read without descending into subdirectories. The inventory includes every non-test source file for the manifest's language and every named function and method declared there, including unselected neighbors and files with no functions. Go discovery is independent of build tags. Python discovery includes synchronous, asynchronous, decorated and nested declarations, including declarations inside control-flow suites. Strings, comments, lambdas, aliases and classes themselves add no function components. `checks` may be empty. Each target and its selected contracts must be in scope; test and conformance references may name files elsewhere inside the project. Tests must refer to declarations in `_test.go` for Go, or `test_*.py` or `*_test.py` for Python; those files are excluded from production discovery. A `proof` requires at least one contract. Loading validates every reference, requires selected contracts to have attached Gobra annotations for Go or supported inline Nagini calls for Python, and rejects duplicate scopes, targets or associations, missing or ambiguous declarations, malformed source and paths that escape the project. Paths must be clean, relative and use `/` separators, spelled exactly as their directories list them; all source and scope paths reject symbolic links, including links inside the project.

Each check may also declare `assumptions` and `invocations`; existing manifests need neither. An assumption requires nonblank `id`, `statement` and `reason`, plus a `source` naming a regular file inside the project. Its ID is unique within the target and remains stable when its text or source bytes change. An assumption alone is enough to configure a target, but grants no proof or other evidence. A target with no proof, tests, conformance or assumptions is invalid.

An invocation requires `id`, `kind`, `check`, `command` and `working_dir`. IDs and commands must be nonblank. Its `kind` is `proof`, `test` or `conformance`; `check` uses the source-and-symbol reference shown above. A proof invocation must name its own target with configured proof; a test or conformance invocation must name a selection of that kind for its target. Invocation IDs are unique within `(target, kind, check)`, so one selected check can have several invocations. Optional `args` is an ordered string list; `env` and `sampling` are maps of explicit string values with nonblank keys. The command and all values remain literal: no command runs, no shell expands them, and no environment is inferred.

`working_dir` must name an existing project-relative directory, with `.` allowed. Optional `inputs` explicitly lists regular files; duplicates within an invocation are errors. Assumption sources, inputs and working directories reject path aliases, escapes and symlinks, including symlinks that point inside the project. Inputs may include fixtures, configuration, dependency manifests and lockfiles, TLA+ models and includes, and `ratified.lock`. Their exact bytes, including ratified bounds, are hashed without interpreting them or requiring new model inputs in old manifests. These declarations do not attest that an invocation ran or that all its execution inputs were declared; later adapters can compare caller-supplied execution snapshots with the declarations ([D-0168](../decisions/journal/D-0168.jsonl)).

Each Go component has an ID such as `go:example.com/app/core#(*Counter).Step@core/ops.go`: language, the import path from the nearest enclosing `go.mod` within the project, the symbol and its project-relative source path. Functions use `Func`; methods use `Type.Method` or `(*Type).Method`. Every initializer uses `init[1]`, `init[2]`, and so on. Blank-named declarations use `_[1]`, `Type._[1]` or `(*Type)._[1]`. Ordinals start at 1 for each receiver-qualified symbol in each file, in declaration order, even for a single declaration, and selections use those numbered symbols too. IDs survive checkout relocation and body or line-number edits; reordering declarations with the same ordinal base can change which declaration an ordinal selects.

Python IDs are `python:<module>#<symbol>@<source>`, for example `python:core.ops#Counter.step@core/ops.py`. The module is the source path relative to the component path base without `.py`, with `/` replaced by `.`, retaining `__init__`: `core.py` and `core/__init__.py` remain distinct. Class nesting adds a dot, and function nesting adds `.<locals>.`, as in `outer.<locals>.Local.method`. Repeated declaration names in each lexical namespace, including containing classes, receive `[1]`, `[2]`, and so on in source order; unique names have no ordinal. Descendants inherit their parents' ordinals. Selections use the complete lexical symbol, including ordinals; bare ambiguous names fail. Relocation, host import paths, configuration ordering, decorators and ordinary body or line edits leave IDs unchanged. Reordering repeated declarations can change which declaration an ordinal selects.

Python contract hashes concatenate the exact original expression bytes of direct standalone, unqualified `Requires`, `Ensures`, `Exsures` and `Decreases` calls in the owning function's immediate suite, in source order without separators. Multiline calls and one-line suites are supported. Each span covers the call expression through its closing parenthesis, retaining internal comments, indentation, original encoding and CRLF bytes. Surrounding comments, indentation and newlines, bodies, the `# +nagini` marker, decorators, strings, nested suites and nested declarations do not contribute. Qualified calls, aliases and calls used inside assignments or other expressions are unsupported contracts. No supported call means an empty contract hash. This is syntax discovery, with no name resolution or assurance claim; the marker still belongs to the separate legacy Nagini proof path.

Python discovery requires `python3` (Python 3.8 or newer) on the host, with syntax support determined by that runtime. The plumbing sandbox is built from the pinned Python image, so its `python3` is the one the collector runs under in production, recorded beside the image's digest as `toolchain.PythonVersion`. CI runs the Python tests under the runner's default `python3` in `invariant/verify` and its tests' shards, and again under that minor version in `invariant/python`'s shards, which `invariant/gate` requires. Where `INVARIANT_PYTHON_PINNED` is set, as there and in the sandbox, a test fails if `python3` isn't the image's minor version ([D-0189](../decisions/journal/D-0189.jsonl)). The embedded parser runs with `-I -S`, uses only the standard library and receives source bytes on stdin: it ignores `PYTHONPATH`, excludes project imports and site startup hooks, and never imports or executes inspected code, decorators, defaults or annotations. It installs no dependencies and invokes no project commands. These discovery rules build on [D-0023](../decisions/D-0023-more-languages.md), [D-0024](../decisions/D-0024-checking-typescript-and-python.md), [D-0031](../decisions/journal/D-0031.jsonl), [D-0152](../decisions/journal/D-0152.jsonl) and [D-0158](../decisions/journal/D-0158.jsonl), and #304's Python identity and contract-hashing rules are recorded as [D-0177](../decisions/journal/D-0177.jsonl).

`project.ResolveComponents` returns a canonical inventory: sorted scope, files, component IDs, checks and association IDs. Assumptions sort by ID, invocations by `(kind, check, id)`, and each invocation's inputs by path; argument order is preserved. Resolved invocation checks use canonical component IDs, and assumption sources and invocation inputs become `{path, sha256}` snapshots. Every nested slice and map belongs to the snapshot, so later configuration or file edits cannot change it. Each file and component carries the SHA-256 of its complete source file; a Go component separately hashes the exact attached doc-comment block, including its final newline, when it contains Gobra annotations. A component without a supported contract has an empty contract hash. Hashes use `sha256:<lowercase hex>`. The inventory and selections carry no assurance or verification evidence.

The inventory's `files` and `components` still describe scoped production only. Its separate `provenance` contains `declarations`, the unique selected test and conformance component snapshots sorted by ID; `files`, the whole selected source files, assumption sources and invocation inputs sorted and deduplicated by path; and `modules`, every nearest `go.mod` used to resolve production or selected identities, also sorted and deduplicated. Python derives module identities solely from paths and has no `provenance.modules`. A `go.mod` named in `inputs` also appears in `provenance.files`. Selecting a test or driver outside scope does not inventory unrelated files or add production coverage. A driver independently included in production scope stays unchecked without its own qualifying proof.

`(*verify.Report).AttributeComponentProofs(*project.ComponentInventory, []verify.ComponentProofResult) error` accepts an explicit inventory on a report that is not model-only. Each result keeps its existing `Target`, `Verifier`, `Passed`, `Inputs`, `Verified`, `Contracts` and `Errors` fields and JSON names. Targets must be distinct configured production proof targets; all declaration references use canonical IDs and matching source and contract snapshots. Extra known references, including selected test and driver declarations in provenance, remain valid without transferring proof. The API runs no tools. Future execution adapters must establish these supplied facts from actual runs; configuration and legacy function names cannot supply them.

Proof results may also supply `Invocation *project.ResolvedComponentInvocation`, `FileInputs []project.ComponentFile` and `Tool *verify.ComponentProofTool`, omitted from JSON when absent under `invocation`, `file_inputs` and `tool`. An actual invocation must match a selected `(kind=proof, check=target, id)`, command, ordered arguments, working directory, environment, sampling and input snapshot set. Actual file snapshots are separate from declaration `Inputs`: a nonempty set must include the target source, and every file must match the production, provenance or module snapshots. Known extra files beyond the invocation's declared inputs are allowed. Invocation and file provenance can be supplied independently. Tool `Name`, `Version` and optional `Pins map[string]string` use JSON `name`, `version` and `pins`; they are opaque supplied data. Existing proof-only callers need none of these fields or a new provenance object, and attribution synthesizes none of them.

`(*verify.Report).AttributeComponentAssumptions(*project.ComponentInventory, []verify.ComponentAssumptionRecord) error` accepts supplied records on the same terms. Each record has `Target string`, `Input project.Component` and `Assumption project.ResolvedComponentAssumption` (`target`, `input`, `assumption` in JSON). The production target snapshot and selected assumption must match exactly, including its ID, statement, reason and source snapshot. Records are unique by `(target, assumption.id)` and remain explicitly **assumed** and **unverified**. A declaration without a supplied record creates no assumption evidence; neither a record nor a declaration proves its target or neighbors.

A target is **proved** only when its result passed, names a verifier, has no errors, reports no unaccounted boundary, includes that exact target among the verified IDs, covers every selected contract, and supplies matching snapshots for every verified or checked-contract ID. Missing, failed or incomplete results leave it **unchecked**. A proof never transfers to a neighbor or to a declaration used as a contract, even when the same run names it as verified; each needs its own qualifying target result. Unknown or duplicate references, unselected targets and stale snapshots are rejected without changing the report.

`(*verify.Report).AttributeComponentConformance(*project.ComponentInventory, []verify.ComponentConformanceResult) error` accepts independently supplied conformance results on the same terms. Each result names an exact production `Target`, selected canonical conformance `Check` and required `Invocation`; `(target, check, invocation.id)` must be unique. Different checks and repeated invocations remain separate records. The invocation must match its declaration, including kind, check, ID, command, ordered arguments, working directory, environment, sampling and input snapshots. Bare names, unknown or unselected references, duplicates and stale supplied snapshots are errors. Missing facts can leave a result unchecked, but a missing invocation is an error.

`ComponentConformanceResult` retains JSON `target`, `check`, `invocation`, `runner`, `inputs`, `file_inputs`, `model`, `bounds`, `mode`, `outcome`, `checked`, `runs`, `steps`, `states`, `transitions`, `model_states`, `observations`, `attempts` and `diagnostics`. `Runner *ComponentProofTool` identifies the supplied runner by name, version and optional pins. `ComponentConformanceModel` holds `module` (the project-relative model source path) and `inputs` (source and explicitly supplied include snapshots). `ComponentConformanceBounds` holds `values` and `sources`. A `ComponentConformanceObservation` records `run`, `step`, `action`, ordered `args`, and `from` and `to` state maps. Optional `ComponentAttemptResult` separately retains `outcome`, `attempts`, `states`, ordered `observations` and `diagnostics`. Modes are `sampled` or `bounded-exhaustive`; conformance and attempt outcomes are `passed`, `failed`, `timed_out`, `skipped`, `empty` or absent. Counts must be nonnegative. Qualification preserves all supplied modes, outcomes and nested values; diagnostics do not override an outcome.

Successful conformance needs matching actual declaration snapshots for the target, every selected proof contract and the driver, plus actual file snapshots for their sources and every invocation, model and bounds input. Model and bounds source snapshots must match declared invocation inputs; `model.module` must identify a snapshot in `model.inputs`, and `bounds.values` must equal the report's ratified bounds. `Design.Spec` names a behavior, not the model source. Attribution compares supplied snapshots without reading files, parsing models or locks, or finding omitted includes or dependencies. Outside-scope drivers add provenance only; conformance grants no proof to a target, contract, driver or neighbor.

A conformance row is **tested** only when its actual outcome is `passed`, its mode is present, runs, steps and states are positive, its runner has a name and version, `checked` includes its selected check, and provenance is complete. Otherwise it is **unchecked**. Declarations, positive counts, package success and legacy conformance alone establish no component evidence. The following qualifications all require that tested result:

| JSON qualification | Additional requirement |
| :-- | :-- |
| `transitions_checked` | Positive `transitions`. |
| `exhaustive_states` | `bounded-exhaustive` mode, positive equal `states` and `model_states`, and either absent attempts or successful nonempty attempts. Sampled runs never qualify. |
| `every_step` | Attempts with outcome `passed` and positive `attempts` and `states`. This describes tried steps in observed states, not proof or exhaustive state coverage. |

Absent attempts grant no `every_step` claim, but alone do not block exhaustive states. Present failed, timed-out, skipped, empty or missing-outcome attempts block exhaustive states while preserving successful checked transitions. A run with only checked refusals can qualify for exhaustive states without a positive transition count.

`(*verify.Report).AttributeComponentRuntime(*project.ComponentInventory, []verify.ComponentRuntimeResult) error` accepts independently supplied runtime records on an explicit report that is not model-only. Each result names a production `Target`, selected canonical test `Check` and required selected `Invocation` of kind `test`, unique by `(target, check, invocation.id)`. The invocation must match its declaration's check, ID, literal command, ordered arguments, working directory, environment, sampling and input snapshot set. Different checks and repeated invocations stay distinct. Missing invocations, bare names, unknown, duplicate, unselected or stale references reject the whole batch.

`ComponentRuntimeResult` retains JSON `target`, `check`, `invocation`, `runner`, `inputs`, `file_inputs`, optional `model` and `bounds`, `outcome`, `tests` and `diagnostics`. Runner identity and pins reuse `ComponentProofTool`; optional context reuses `ComponentConformanceModel` and `ComponentConformanceBounds`. Every supplied declaration and file snapshot is retained, including known extra inputs beyond the invocation's declaration. Each `ComponentRuntimeTestResult` has a selected canonical `check`, `exercised`, `outcome`, optional `assertions` and `diagnostics`; checks are unique within a result. Each `ComponentRuntimeAssertionResult` has a nonblank `id`, optional `source` snapshot, `exercised`, `outcome` and optional `diagnostics`. Assertion IDs are opaque and unique within their test and invocation, rather than component identities. A supplied source must match a known input file. At every level, outcomes are `passed`, `failed`, `timed_out`, `skipped`, `empty` or absent; diagnostics remain ordered data and cannot replace outcomes. Unreported tests and assertions remain absent, even when selected or when a process or legacy check passed.

Runtime qualification requires a runner name and version, matching actual target and selected-contract declarations and source files, and all invocation input snapshots. Each actual test also needs its own matching declaration and source. Ordinary runtime tests need no model or bounds context; when supplied, model and bounds sources must match declared invocation inputs, `model.module` must identify a supplied model snapshot, and bounds values must match the report's ratified bounds. Missing required actual snapshots or incomplete supplied context leave evidence **unchecked**; mismatches reject it.

| Runtime evidence | Qualification with the required provenance |
| :-- | :-- |
| Test **tested** | The actual test has `exercised:true` and outcome `passed`, independently of process success. No assertion records are required. |
| Assertion **passed** | Its parent test and the assertion were actually exercised, its own outcome is `passed`, and its matching source snapshot is present in actual `file_inputs`, including extra supplied files. It can pass even when its test or process failed. |
| Runtime row **tested** | The process outcome is `passed` and an actual qualifying test names the row's selected `check`. |

Other qualifications remain **unchecked**, preserving unsuccessful, missing-outcome and unexercised records. A passing test or process cannot make a failed or unexercised assertion pass. Runtime evidence establishes no target code coverage, proof, exhaustive conformance, every-step coverage or validated assumption.

All five attribution operations, the four here and [backing tests](#trusted-dependency-contracts), replace only their own evidence kind; an empty slice clears only that kind. Each validates a detached inventory, incoming records and every retained kind, including on clears, before committing once. Duplicate scopes, declarations, files, selections and local identities, unknown references and inconsistent snapshots are rejected; matching overlaps between production, provenance and module collections are valid. Retained proof, conformance and runtime attempts must still match their original selections, targets, contracts, tests, drivers, invocations and supplied inputs, even when incomplete; omitted actual snapshots do not remove the original inventory's anchors. Changes to outside-scope drivers, test sources, assertion sources, non-Go inputs, dependencies, model sources or includes, bounds sources, report bounds or invocation metadata invalidate retained evidence that depends on them. Changed selections cannot promote or demote retained proof coverage. Retained assumptions must still match their target and selected declaration, including its content and source. Any error leaves report JSON bytes and stored and recomputed fingerprints unchanged. Success deep-copies all nested slices and maps, including retained evidence, preserves legacy checks, ratified data and gate pass or fail, and finalizes the fingerprint. Old proof callers remain valid without execution metadata, including extra known verified declarations, checked contracts and matching snapshots.

JSON adds `components`, containing the complete `inventory`, `proofs` and optional `assumptions`, `conformance`, `runtime`, `contract_tests`, `contracts` and `contract_diagnostics`. Proof rows remain `{target, status, result?}`, one per production component sorted by ID, unchecked without their own qualifying proof. Assumption rows are `ComponentAssumptionEvidence` with `{target, status:"assumed", verified:false, record}`, sorted by target and assumption ID; `verified` is present even though false. Conformance rows are `ComponentConformanceEvidence` with `{origin?, target, check, status, transitions_checked, exhaustive_states, every_step, result}`, sorted by `(target, check, result.invocation.id)`; all three booleans remain present when false. Runtime rows are `ComponentRuntimeEvidence` with `{origin?, target, check, status, tests?, result}`, sorted by the same tuple. Their `ComponentRuntimeTestEvidence` rows have `{check, status, assertions?}`, sorted by check, and `ComponentRuntimeAssertionEvidence` rows have `{id, status}`, sorted by ID. Empty assumptions, conformance and runtime may be omitted; declarations never synthesize missing records. Markdown keeps every production component's independent proof row and adds separate conformance, runtime, assumption and input-provenance views. Configuration-only selections and declared assumptions are visible separately from supplied records; missing results create no evidence rows. Each supplied kind shows exact target and check identities, source and contract hashes, actual invocation metadata, ordered arguments, environment and sampling, tool identity and pins, declaration and file inputs, model and bounds, observations, outcomes and diagnostics. Input-only tests and drivers remain provenance, never extra production coverage. Reports say **component evidence**, with no overall assurance score, project-wide proof or every-size claim, including after JSON decoding with a stale assurance label. The explicit prose and footer acknowledge supplied results and declared assumptions, always assumed / unverified, and explain that the component API runs no tools; legacy footers still say "tool output only".

Markdown qualifies conformance from supplied outcomes and provenance, even after decoding JSON with stale status flags: sampled observations never establish exhaustive states; positive matching bounded-exhaustive states may establish every state with absent attempts, but never every step. Present failed, timed-out, skipped, empty or missing-outcome attempts block exhaustive-state and every-step claims without erasing successful observations. Every-step coverage requires successful nonempty attempts and states. Runtime preserves independently qualifying tests and assertions when enclosing outcomes fail, while process success invents no exercise, assertions or target code coverage. Unknown mode, outcome or status strings remain inert text and cannot establish success. Missing optional metadata renders without inventing evidence; assumptions remain assumed / unverified even with stale decoded labels.

Throughout explicit component receipts, literals use escaped `<code>` elements: fingerprints, statement names and pins, bounds, toolchain versions, images and recipes, and names, actions, paths, hashes and states in evidence rows and failure descriptions. Each original value is escaped once before assembling prose and markup, so underscores, ampersands, backticks, literal entity strings and line endings retain their characters without double escaping. Untrusted text cannot create HTML, links, table cells or rows, or forge component associations or statuses; this includes assumption text and origins, invocation metadata, observations, outcomes and diagnostics. Hash and image digest shortening is unchanged. Rendering preserves the report and original JSON values, fingerprints, gate outcomes, component proof coverage and all legacy Markdown.

The fingerprint binds the complete scope and files, including unchecked neighbors and files without functions, component and contract identities, selected checks, proof snapshots, outcomes and verifier pins. It also binds selected test and driver snapshots, declared and supplied assumption identities, targets, snapshots, labels, statements, reasons and source bytes, declared and actual invocation identities and check associations, commands, ordered arguments, working directories, explicit environment and sampling maps, each invocation's input references and exact bytes, actual file inputs, tool identity and pins, and the nearest module files. Every conformance and attempt field is bound: identities, qualifications, runner pins, modes, outcomes, checked references, counts, declaration and file snapshots, model identity and includes, bounds values and sources, observations and diagnostics. Every runtime field is bound too, including each test and assertion's identity, qualification, exercise flag, outcome, source and diagnostics, plus runner pins, invocations, every supplied input and optional model and bounds context. Optional evidence presence is bound. Attribution owns every nested value. Canonicalization applies after attribution and JSON decoding without mutating the report: reordering reference sets, runtime tests and assertions or record completion order, or relocating the checkout, does not change the fingerprint; arguments, observations and diagnostics remain meaningful ordered sequences. Existing volatile metadata stays excluded, including timestamps, build output, the host's Go version, trace output paths and the coding agent, as required by [D-0135](../decisions/journal/D-0135.jsonl).

The first slice of [#229](https://github.com/gitdek/invariant/issues/229) corrects legacy assurance claims as described above. Successful legacy Go, Python and TypeScript receipts retain their Markdown, fingerprints and unverified-function lists. No component scope is inferred: a successful legacy proof still lists the functions outside its proof, and its project-wide label does not establish proof of those neighbors. The legacy assurance policy is [D-0161](../decisions/journal/D-0161.jsonl); component direction and inventory are [D-0152](../decisions/journal/D-0152.jsonl) and [D-0158](../decisions/journal/D-0158.jsonl).

These five APIs attribute supplied proof, assumption, conformance, runtime and backing-test records only: no execution, discovery, instrumentation, execution protocol, adapter or gate connection is added, and no filesystem lookup is performed during attribution ([D-0169](../decisions/journal/D-0169.jsonl), [D-0170](../decisions/journal/D-0170.jsonl)). [#244](https://github.com/gitdek/invariant/issues/244)'s evidence foundation for the narrowed step 1 of [#210](https://github.com/gitdek/invariant/issues/210) now includes completed Markdown and JSON receipts through [#296](https://github.com/gitdek/invariant/issues/296), with component dashboard views still later work: bounded or sampled conformance, runtime checks and trusted assumptions as separate evidence kinds alongside proof. #210's plan is narrowed to the path ask3d needs: #294 then checks unchanged Python against a model, backs trusted dependency contracts with tests and proves selected Python functions in place, each connecting the gate for its path. Its Go steps, the checks on callers against proved contracts and the CI check on evidence regressions wait until ask3d shows they're needed ([D-0172](../decisions/journal/D-0172.jsonl)). TypeScript discovery is outside this ordinary Go and Python scope. Explicit code verification outside sampled and explored existing Python still returns `verify.ErrComponents` before verification tools run. Explicit Python configurations support discovery and direct application test, sampled and explored driver sessions; explicit TypeScript discovery remains rejected; model-only checking remains available and its reports reject all five attribution APIs, including clears. Existing proof policy is [D-0163](../decisions/journal/D-0163.jsonl), declaration provenance is [D-0168](../decisions/journal/D-0168.jsonl), independent proof and assumption attribution is [D-0169](../decisions/journal/D-0169.jsonl), conformance attribution is [D-0170](../decisions/journal/D-0170.jsonl), and runtime attribution is [D-0171](../decisions/journal/D-0171.jsonl). [#296](https://github.com/gitdek/invariant/issues/296)'s receipt policy, refining D-0169, is recorded as [D-0175](../decisions/journal/D-0175.jsonl). Keeping this description current is required by [D-0122](../decisions/journal/D-0122.jsonl).

## Trusted dependency contracts

A function proved in place still calls things its prover can't verify: the standard library, I/O, a third-party package, or the application's own code beyond the proof's boundary. Operations on dicts, lists, sets, strings and tuples, and the builtins Nagini models, aren't among them: they're the verifier's own trusted model, as in the legacy cores ([D-0230](../decisions/journal/D-0230.jsonl)). [#343](https://github.com/gitdek/invariant/issues/343), the third step of [#294](https://github.com/gitdek/invariant/issues/294)'s plan, inventories those boundaries as **trusted contracts**: assumptions about a real operation, which its backing tests can support. A trusted contract is never a proof of the operation, and declaring one shows no call, no test run and no proof that used it. The policy is [D-0198](../decisions/journal/D-0198.jsonl).

An existing Python application's `components` can declare them beside its checks. Paths are relative to the application root, as its other component paths are:

```json
"trusted_contracts": [
  {
    "id": "json.loads",
    "operation": {"kind": "standard_library", "module": "json", "symbol": "loads"},
    "contract": {"source": "verification/tasks/contracts.py", "symbol": "loads"},
    "tests": [{"source": "verification/tasks/test_contracts.py", "symbol": "LoadsTests.test_parses"},
              {"source": "verification/tasks/test_contracts.py", "symbol": "LoadsTests.test_rejects_malformed"}],
    "invocations": [{"id": "loads", "kind": "test",
      "check": {"source": "verification/tasks/test_contracts.py", "symbol": "LoadsTests.test_parses"},
      "command": "python", "args": ["-m", "unittest", "verification.tasks.test_contracts"], "working_dir": "."},
      {"id": "loads-malformed", "kind": "test",
      "check": {"source": "verification/tasks/test_contracts.py", "symbol": "LoadsTests.test_rejects_malformed"},
      "command": "python", "args": ["-m", "unittest", "verification.tasks.test_contracts.LoadsTests.test_rejects_malformed"], "working_dir": "."}]
  },
  {
    "id": "packaging.Version",
    "operation": {"kind": "dependency", "distribution": "packaging", "module": "packaging.version", "symbol": "Version"},
    "contract": {"source": "verification/tasks/contracts.py", "symbol": "version"},
    "tests": [{"source": "verification/tasks/test_contracts.py", "symbol": "VersionTests.test_orders"}],
    "invocations": [{"id": "version", "kind": "test",
      "check": {"source": "verification/tasks/test_contracts.py", "symbol": "VersionTests.test_orders"},
      "command": "python", "args": ["-m", "unittest", "verification.tasks.test_contracts.VersionTests"], "working_dir": "."}]
  },
  {
    "id": "store.save",
    "operation": {"kind": "application", "source": "app/storage/store.py", "symbol": "save"},
    "contract": {"source": "verification/tasks/contracts.py", "symbol": "save"},
    "tests": [{"source": "verification/tasks/test_contracts.py", "symbol": "SaveTests.test_returns"}],
    "invocations": [{"id": "save", "kind": "test",
      "check": {"source": "verification/tasks/test_contracts.py", "symbol": "SaveTests.test_returns"},
      "command": "python", "args": ["-m", "unittest", "verification.tasks.test_contracts.SaveTests"], "working_dir": "."}]
  }
]
```

| Field | Rule |
| :-- | :-- |
| `id` | Unique, nonblank, with no spaces or control characters. |
| `operation` | `standard_library`: a dotted `module` and `symbol` of the pinned Python runtime, builtins included; a contract on a builtin or container operation Nagini models still has required backing tests, though an in-place proof never assumes it. `dependency`: a `distribution` name, normalized as PEP 503 does, with a dotted module and symbol. `application`: a `source` and `symbol` naming one declaration outside a test file, in scope or not. Two contracts can't bind one operation: the standard library's and a dependency's are told apart by their full dotted name alone, however a manifest splits it between `module` and `symbol`. |
| `contract` | A declaration with supported inline Nagini calls (`Requires`, `Ensures` and the rest), outside scope and test files, which isn't the operation itself or another contract's operation. Two contracts can't share one. Its `Exsures` calls say what the operation may raise, each naming its exception by a builtin or a name an absolute import at the module's top level binds; declaring none for an exception says the operation never raises it under its precondition. |
| `tests` | At least one declaration in a test file. |
| `invocations` | Declared as a check's are, each of kind `test` and naming one of the contract's tests. A backing test is supported only through a result for one of its declared invocations, so each test needs one. |

Every path must be spelled exactly as its directory lists it, so a case-insensitive disk can't give one file a second identity.

`project.ResolveComponents` returns them as `trusted_contracts`, sorted by ID, with canonical contract, test and application-operation IDs, and adds their declarations and source files to `provenance`, never to production scope. Their identities survive relocation and reordering, and a changed `Requires` or `Exsures` changes the contract's hash. Each contract's `exsures` lists the dotted paths its `Exsures` exceptions resolve to, read without running the module ([D-0221](../decisions/journal/D-0221.jsonl)), and a contract with one the inventory can't resolve fails to load. Other manifests can't declare them, and manifests without them resolve as before.

A report binds each contract to the identity it records, never to a name or a mutable tag: the Python runtime pinned by digest (the dependency image's base, or the Python image), and for an application operation its declaration's snapshot, or for a dependency the one complete resolved wheel, by name, version, filename and SHA-256. `(*verify.Report).AttributeComponentContractTests(*project.ComponentInventory, []verify.ComponentContractTestResult) error` attributes supplied backing-test results as a fifth independent kind. A result names its contract, one of its tests as `check`, a matching declared invocation, the runner, the binding it ran against, the `implementation` the run resolved, each test's `exercise` (its calls into that implementation), and its inputs, outcomes and diagnostics, in the runtime result's forms. A backing test supports its contract only when it was exercised and passed in a process that passed, with the binding the report records, actual snapshots of the contract, the test, an application operation and every invocation input, and calls into an implementation the binding matches: the declaration's own source, the bound wheel's name and version with its file in the installed RECORD, or no distribution for the standard library, and at least one of its assertions passed with none failed, and no exception its contract's `Exsures` doesn't declare escaped those calls. No result may report it unsuccessful: one failed, skipped or unexercised run, one that reached no real implementation, one that checked nothing or had an assertion fail, or one that saw an undeclared exception, vetoes another's pass, and a `conflicting_support` diagnostic says so. A contract is **tested** only when every backing test is.

Proof results add optional `assumed`, the trusted contracts the verifier's inputs actually assumed, each with its declaration among the attempt's inputs, and `unaccounted`: a `missing_contract`, naming the operation the attempt reached in the inventory's own form, or an `unaccounted_assumption`, described and naming its operation where it has one. Neither may name an operation a declared contract binds. An unaccounted boundary leaves the target unproved. Every attribution derives each contract's `binding`, `support` and `use`, which is `declared` until a proof attempt reports assuming it, and `contract_diagnostics` for unresolved operations, conflicting support, unsupported contracts, assumed contracts that aren't tested, missing contracts and unaccounted assumptions. A snapshot, binding or retained association the report no longer holds is stale, of every evidence kind: a known declaration, file or invocation whose contents changed, or a retained record whose anchors did. It rejects the whole attribution with `verify.ErrStaleComponentEvidence`, leaving the report unchanged, so a changed contract, test, invocation, operation, runtime or wheel can't keep its support. Clearing the backing tests recovers a report whose support alone went stale; when another kind rests on the same change, such as a proof that assumed the contract, only a new report records the changed inputs. The fingerprint binds all of it.

Receipts add a trusted-contracts view, with each contract's operation, what it may raise, binding, `trusted · tested` or `trusted · untested` support and its use; a view of backing-test results, with their origin, the implementation each run resolved, each test's calls into it and the exceptions that escaped them, declared or not; and a diagnostics view, which a report without contracts also shows when a proof reports an unaccounted boundary. The receipt derives support, use and each component's proof status again from the report's own results, so decoded JSON can't carry a forged standing or proof.

### Backing tests run in the gate

[#350](https://github.com/gitdek/invariant/issues/350), the fourth step of #294's plan, runs every declared contract's backing tests when `invariant verify` checks the application, in the same staged copy and the same offline dependency image as its other tests ([D-0206](../decisions/journal/D-0206.jsonl)). Tests support trusted assumptions; they never prove the operation. A contract in the example above is the abstraction a proof will rely on:

```python
# verification/tasks/contracts.py
from nagini_contracts.contracts import *

def loads(text: str) -> object:
    Requires(len(text) > 0)
    Exsures(Exception, True)
    ...
```

and its backing tests exercise the real operation, checking its results, its errors and, for an operation that changes its arguments, the change:

```python
# verification/tasks/test_contracts.py
import json
import unittest

class LoadsTests(unittest.TestCase):
    def test_parses(self):
        self.assertEqual(json.loads('{"task": "pending"}'), {"task": "pending"})

    def test_rejects_malformed(self):
        with self.assertRaises(json.JSONDecodeError):
            json.loads('{')
```

`test_rejects_malformed` sees `json.loads` raise `json.JSONDecodeError`, a `ValueError`, so the contract must declare it: a contract says its operation raises nothing it doesn't declare. A declared class covers its subclasses, and this contract declares `Exception` because a proof that assumes it reads its `Exsures` too, and the pinned Nagini models only `Exception` and `ConnectionRefusedError` among exception classes ([D-0230](../decisions/journal/D-0230.jsonl)).

Each backing test runs through one of its contract's declared invocations; a backing test with none fails preflight. Before any test loads, the runner resolves the operation to its real implementation: the application's declaration in its staged source, the standard library's code from the runtime's own library or built in, or a dependency's code from the installed libraries, whose distribution's RECORD must list the file the code is in. A class resolves to methods matched to declarations in its source and to code compiled from that source, and a method named on its class, as `Version.__init__`, only when it matches a declaration there under that name. A compiled qualified name alone doesn't establish ownership: generated dataclass methods and annotation helpers aren't declarations, even when Python gives their code class-qualified names. The runner then counts the calls each test method itself makes into that implementation, directly or through code it calls, and records each outermost unittest assertion the test method makes, with its outcome ([D-0216](../decisions/journal/D-0216.jsonl), [D-0234](../decisions/journal/D-0234.jsonl)). Calls and assertions that unittest or the runner make don't count, except where the callable form of `assertRaises` or `assertWarns` calls what the test handed it, as in `assertRaises(E, f, x)`, and neither do those from `setUp`, `tearDown` or a cleanup. A call counts only into the very code or C callable resolved, run with the globals it was defined with, a class's method only on that class, an instance or a subclass, by the receiver's real type, and a bound method only on its very object, so a look-alike with the same name, a method moved onto another class, or a copy compiled or loaded again from the same source under another module name, counts nothing ([D-0241](../decisions/journal/D-0241.jsonl)). Each thread keeps its own assertion depth, so a worker thread inside an assertion hides none the test method makes. It also records each exception type that escapes any call into the implementation during the test, from any caller in any thread, and whether a class the contract's `Exsures` names covers it, subclasses included ([D-0221](../decisions/journal/D-0221.jsonl), [D-0235](../decisions/journal/D-0235.jsonl), [D-0242](../decisions/journal/D-0242.jsonl)). Each declared class is pinned once the test modules have loaded, so no test method can swap it. It can't evaluate a precondition, so every exception a backing test sees escape must be declared, whether its call met the precondition or not.

A backing test supports its contract only when it passed, exercised, in a run that passed, made calls into the implementation the report binds, had at least one assertion pass and none fail ([D-0217](../decisions/journal/D-0217.jsonl)), and saw no exception escape that the contract doesn't declare. A test that reached only a stub, a mock or a substitute, one with no assertion that passed, one that saw an undeclared exception, a skipped test, a failed one, one that ran twice in one invocation, or one its invocation never ran supports nothing, and any one of those runs vetoes another's pass. A contract whose tests ran without all supporting it gets an `unsupported_contract` diagnostic, and the receipt's failures name the contract and why. The gate passes only when every declared contract is tested, whether or not a proof uses it yet; a tested contract no proof uses stays a declaration. Each backing test's build step passes only where it supports its contract, through `verify.Run` and `Python.Check` alike.

Resolution and receipt reasons agree on Python 3.12, 3.13 and 3.14: a class with only generated methods has an unresolved implementation; a class with declared methods resolves, but a test that calls only its generated helpers leaves the contract untested for missing calls. The reason follows what the runner observed, without a Python-version exception ([D-0216](../decisions/journal/D-0216.jsonl), [D-0234](../decisions/journal/D-0234.jsonl), [D-0241](../decisions/journal/D-0241.jsonl), [D-0304](../decisions/journal/D-0304.jsonl)).

Runtime observation has limits. Nothing ties a passing assertion to the call it checks: a test that makes the real call and asserts something else, checks a mock while a helper makes the real call, patches what the real call uses, as `mock.patch('json.decoder.JSONDecoder.decode')` around a real `json.loads` does, or calls a subclass that overrides part of the class supports its contract, so the assertion requirement keeps out a test that asserts nothing, not one that asserts the wrong thing. Some operations can't be observed at all, and the runner refuses to resolve them before any test loads, so their contracts stay untested for now: a class with no method of its own, such as a type written in C or a dataclass whose methods are all generated, including `pstats.StatsProfile`; a method that's inherited, assigned or made by a factory, as `functools.total_ordering`'s comparisons are, a property named on its class, and a function whose code is a closure's; slot wrappers, method-wrappers and C special methods like `dict.__getitem__`, which syntax reaches; and builtins that import or class statements reach, as `__import__`. A C function counts only where Python code calls it, and what it raises when other C code calls it, as `map(d.pop, keys)` does, goes unseen, as does a C call's raise in a thread started through `_thread` directly; calls in other threads aren't counted. Knowing what escaped a call takes Python's `sys.monitoring`, so backing tests need Python 3.12 or newer, as the pinned image is. A backing test runs in the same process as the runner, so a test that tampers with the runner's own records, or a test module that swaps a declared exception class while it loads, is out of scope, as it is for drivers. The gate doesn't check the code around a proof against its preconditions; #210 leaves that for later.

`INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify -run '^TestPythonApplicationGateTrustedContracts' -count=1 -timeout 45m` runs the gate's contract cases with real Docker, Python and TLC, including a contract on the installed `packaging` wheel.

### Proving a function in place

[#353](https://github.com/gitdek/invariant/issues/353), the fifth step of #294's plan, proves a selected function in its real module with the pinned Nagini: no `core.py`, no `# +nagini` marker on the file, and the rest of the module left as it is ([D-0213](../decisions/journal/D-0213.jsonl)). You annotate the function with Nagini's inline contract calls where it lives, beside neighbors Nagini can't read, such as an `async` handler:

```python
# tasks/admission.py
from nagini_contracts.contracts import *
from packaging.utils import is_normalized_name

LIMIT = 3

def admit(name: str, admitted: int) -> int:
    Requires(admitted >= 0)
    Requires(admitted < LIMIT)
    Ensures(Result() >= admitted)
    Ensures(Implies(Result() > admitted, len(name) > 0))
    if is_normalized_name(name):
        return admitted + 1
    return admitted

async def report(name: str) -> str:
    return f"admitted {name}"
```

and select it as a check's proof, its own inline contract the proof's contract, with no proof invocation, since the gate runs only its own pinned Nagini. Conformance on the same function, or the observed-call route below, shows the application runs the function it proved. The module must be among the manifest's `existing` sources:

```json
{"target": {"source": "tasks/admission.py", "symbol": "admit"},
 "proof": {"contracts": [{"source": "tasks/admission.py", "symbol": "admit"}]},
 "conformance": [{"source": "verification/tasks/driver.py", "symbol": "drive"}]}
```

A decision that refuses a request can instead be observed inside the entries of another check's exhaustive exploration ([D-0416](../decisions/journal/D-0416.jsonl), refining [D-0260](../decisions/journal/D-0260.jsonl)). For example, a service's `admission` function returns 403 for the wrong site, 400 for the wrong engine, 409 while busy and 0 on admission. Its entry raises the driver's declared refusal before starting the selected worker when admission fails. Add `observe` to the worker's existing check, keeping its conformance invocation, and a separate proof selection:

```json
[
  {"target": {"source": "tasks/worker.py", "symbol": "run"},
   "observe": [{"source": "tasks/admission.py", "symbol": "admission"}],
   "conformance": [{"source": "verification/tasks/driver.py", "symbol": "drive"}],
   "invocations": [{"id": "explore", "kind": "conformance",
     "check": {"source": "verification/tasks/driver.py", "symbol": "drive"},
     "command": "python", "args": ["driver.py"], "working_dir": "verification/tasks",
     "inputs": ["verification/tasks/.invariant/specs/Tasks.tla", "verification/tasks/.invariant/ratified.lock"]}]},
  {"target": {"source": "tasks/admission.py", "symbol": "admission"},
   "proof": {"contracts": [{"source": "tasks/admission.py", "symbol": "admission"}]}}
]
```

This is the `components.checks` list; set `exhaustive: true`, include both modules in `existing`, and include their directories in `components.scope`. `observe` accepts module functions in the existing application. It resolves and pins their source/symbol declarations and requires exhaustive conformance on the check that declares it. The separate proof check needs no conformance invocation of its own.

Each completed call inside an entry appears in completion order in that attempt observation's `calls`, with `target`, `source_sha256`, `caller`, `arguments` by parameter name and `result`. `caller` is the staged file whose code made the call: the nearest frame beneath it that runs application code. Positional, keyword and default arguments are recorded as Python binds them. The collector copies arguments on entry and results on return: finite JSON scalars, lists and string-keyed dictionaries, with exact integers, including integers larger than a float can represent. Callable substitutions, unsupported values and inconsistent replays invalidate the execution. Calls made outside entries, during initialization or during `release` earn no evidence; observing a decision does not count as calling or starting the selected worker. Held-call reservation, release and read-back keep their existing rules.

The observed function qualifies only when the same complete, successful exploration invocation records it on both accepted and declared-refused non-replay attempts, against the proof's source bytes, beside passing transition, state and attempted-step conformance. An accepted attempt is the accepted outcome of a step that declares a refusal. Only calls the application's own code makes count: a call whose `caller` is one of the manifest's `existing` sources, or lies under one, and isn't the driver. A call that a driver, a helper outside those sources or a callback of theirs makes is recorded with its caller and counts for nothing, since it shows nothing about the application. A replay is compared with the attempt it repeats by the calls made, in any order, since calls on an entry's worker threads complete in an order that varies. A declaration alone, calls on only one outcome, replay-only calls, a sampled run or stale, incomplete or failed evidence leaves the proof not qualified, and the proof's diagnostic says which of these it lacks. Receipts retain the calls and declarations, bind them into fingerprints and check qualification again from the matching execution and conformance; missing coverage appears in the proof diagnostics.

Every operation beyond the function needs a [trusted contract](#trusted-dependency-contracts), here one on `packaging.utils.is_normalized_name` whose abstraction says what the proof relies on:

```python
# verification/tasks/contracts.py
def normalized(name: str) -> bool:
    Ensures(Implies(Result(), len(name) > 0))
    ...
```

`(*verify.PythonApplicationSession).Prove` builds Nagini's input from the session's staged snapshot and runs the pinned Nagini on it, offline, in its own linux/amd64 image, where none of the application's requirements is installed; `ProofInput` builds the input alone. The input is a module, `invariant_target.py`, that copies the function's original bytes, and from its module only the imports of Nagini's contracts and typing, `__future__` imports and literal constants holding immutable values nothing in the module rebinds. Each other name the function reads is one of these:
- a builtin Nagini models itself (`int`, `float`, `bool`, `str`, `object`, `list`, `dict`, `set`, `tuple`, `len`, `isinstance`, `abs`, `min`, `max`, `range`), whose operations, on dicts, lists, sets, strings and tuples too, are the verifier's own trusted model, as in the legacy cores;
- `Exception` or `ConnectionRefusedError`, the exception classes it models;
- or bound by an import the gate writes to a boundary module that holds the contract's abstraction: its header renamed for the operation, its contract statements copied, `Exsures` included, and marked `ContractOnly`, so Nagini assumes it and never verifies it.

So here the input binds `is_normalized_name` to `normalized`'s contract, and `report` never reaches Nagini, which given the whole file stops on it. Every span of the input maps to the original lines and bytes it copies, with their SHA-256. Each is checked against the original before Nagini runs, a signature renaming only the function, and the files Nagini read are checked again after it runs, so a rewritten or detached implementation can't stand for the application's. Nagini's errors come back at the original lines ([D-0230](../decisions/journal/D-0230.jsonl) refines what's refused below).

| The function reaches | What happens |
| :-- | :-- |
| An operation a trusted contract binds, by its full dotted name, or for the application's own code by its declaration | The input assumes the abstraction, and the attempt reports the contract as assumed. Contracts the function doesn't reach stay declarations. |
| An operation no contract binds, other builtins and I/O such as `print` included | A `missing_contract` in the inventory's own form, a dependency's named for the distribution whose RECORD installed its module (PyYAML's `yaml` is `pyyaml`'s), and Nagini doesn't run. |
| A module no distribution, or several, installed | An unaccounted assumption, and Nagini doesn't run. |
| Nagini's `Assume`, or a Nagini name Invariant's runtime stand-in doesn't export, such as `ToSeq` | Refused before Nagini runs: `Assume` has Nagini take a fact nothing proves, and a name the stand-in doesn't export isn't bound where the application runs ([D-0260](../decisions/journal/D-0260.jsonl)). |
| One of Nagini's names outside a contract statement, or a contract Python couldn't evaluate as Nagini reads it, such as one that divides inside an `Implies` | Refused before any tool runs: the application runs the contract with the stand-in, and Python evaluates every argument when the statement runs. The next section lists what a contract may hold ([D-0260](../decisions/journal/D-0260.jsonl)). |
| Arithmetic the pinned Nagini models apart from Python, in the function or a trusted contract's abstraction, such as `count + 1 // DIV` with `DIV = -2`, or the method behind such an operator called by name, such as `(count + 1).__floordiv__(DIV)`, or that arithmetic by another name, such as `operator.floordiv` or `getattr(count, '__floordiv__')` | Refused before any tool runs: Nagini divides as Viper's Euclidean division does, models the bitwise operators apart from Python on negative ints, and leaves floats uninterpreted. The next section lists the arithmetic it takes ([D-0260](../decisions/journal/D-0260.jsonl), [D-0264](../decisions/journal/D-0264.jsonl), [D-0268](../decisions/journal/D-0268.jsonl)). |
| A call whose contract declares an exception, inside a handler for it | Nagini checks the handler's path. What the contract declares is the inventory's `exsures`, read from its own `Exsures` ([D-0221](../decisions/journal/D-0221.jsonl)). A handler that can catch what the contract doesn't declare is refused: a bare except catches everything. A contract with no `Exsures` says its operation never raises. |
| A name Nagini reads as its own whatever it's bound to, such as `Assume`, `len`, `range` or `Pure` | Refused unless it's Nagini's own export of that name, typing's or the real builtin. The function may bind none of the calls Nagini dispatches by name, and may not import Nagini's names under others. The lists come from the pinned image's sources. |
| Anything else the input can't preserve | Refused before any tool runs, and in the gate at the points the next section says: an `async` function, decorators other than `Pure` (the gate refuses `Pure` too, since conformance binds only undecorated functions), nested functions, lambdas, `global` or `nonlocal`, imports in the function, `*args`, classes, star imports other than Nagini's, attributes deeper than a module's function, a module used other than to call its functions, names bound more than once, conditionally or not at all, a copied statement sharing its line with another or ending in a line continuation, an annotation the input can't bind, a relative import above the top-level package, an async application operation, an exception class Nagini doesn't model, an attribute of any builtin, as `str.maketrans`, refused before any contract is consulted, a non-UTF-8 source, and an abstraction that holds more than contracts, reads more than a function may, or reads a name a star import other than Nagini's could bind. |

What callers reach is the module's binding of the function's name, so the module must bind it by its one def and never rebind it. The module may not write its namespace in ways no binding shows (with `globals`, `vars`, `setattr`, `delattr`, `exec`, `eval`, `__import__`, `sys.modules` or `__builtins__`), and may star-import only Nagini's contracts.

The proof covers the module as production code leaves it, so the application's production sources are scanned for anything that could change the module's names ([D-0239](../decisions/journal/D-0239.jsonl), [D-0244](../decisions/journal/D-0244.jsonl), [D-0247](../decisions/journal/D-0247.jsonl)), or what Invariant's runtime stand-in for `nagini_contracts` or the builtins bind, which the proof takes as they ship ([D-0260](../decisions/journal/D-0260.jsonl)). Test files, by the project's rule (`test_*.py` and `*_test.py`), and the verification project's own code, its driver and helpers, are left out until production code loads them, by an import or by a constant name through importlib and its kin or as a patcher's target. A file named like a test is treated as one even in a production package, such as `tasks/test_helpers.py`, until production code loads it. So a test may patch the module, by name or with `patch.object`, without affecting the proof. A production source may reach the module, `nagini_contracts` or its `contracts`, `builtins`, a package above the module, or an application module whose imports bind one of those, through an import, a relative import, a module that imports it or a star import, but only to read attributes, as `admission.admit(text, 1)` and `admission.LIMIT` do. Refused before any tool runs, in every production source:
- writing an attribute of the module, of `nagini_contracts`, its `contracts` or `builtins`, as `contracts.min = floor` and `builtins.min = floor` do, and rebinding a package's attribute on the way to one, as `tasks.admission = None` does;
- binding the module to a name, as `m = admission` does, passing it to a call, as `poke(admission)` and `patch.object(admission, "LIMIT", 100)` do, and any other use. `getattr`, `hasattr`, `setattr` and `delattr` may take a package on the way to it by a constant naming something else in it;
- reading `__dict__`, `__setattr__` or another special attribute, besides `__name__`, `__file__`, `__doc__`, `__path__`, `__all__` and `__version__`;
- reaching one of these modules by a constant name, where the name goes to something that reaches modules by it: `sys.modules`, `importlib.import_module`, `__import__`, `runpy.run_module`, `pkgutil`'s resolvers, `pydoc.locate` and mock's patchers, as `importlib.import_module("tasks.admission")` and `mock.patch("tasks.admission.LIMIT", 100)` do in production code. Any other string that names the module, as `logging.getLogger("tasks.admission")` does, passes;
- calling `globals()`, `locals()` or `vars()` in a source that holds one of these modules;
- a production source the host's Python can't parse, and a compiled module, a `.pyc`, `.so` or `.pyd`, that production code loads or that stands where the module or a package on its way would be, since the scan can't read either;
- for a package's own `__init__.py`, a submodule with a name the function reads, which importing it would rebind.

More is refused where production code runs beside the module: in the module, the packages on its way, every production source that loads one of those, directly or through others, every production module the gate's own processes load, through the selected tests, the conformance driver and the backing tests, directly or through any source they load ([D-0261](../decisions/journal/D-0261.jsonl)), and everything those load. A `setup.py`, a docs configuration or a tool that nothing there loads doesn't hold the application back. There, these are refused before any tool runs too:
- running code from a path or a code object, with `runpy.run_path`, `runpy.run_code` or importlib's file loaders;
- running or compiling code from a string, with `exec`, `eval` or `compile`, under any name no scope around it binds, since a string can hold `contracts.min = floor`. A parameter or a module's own function named `compile` passes;
- reaching modules through objects, with `inspect.getmodule` or `gc`'s `get_objects`, `get_referrers` and `get_referents`;
- reading `__globals__`, `f_globals`, `f_builtins`, `__builtins__` or `__self__`, as `len.__self__`, the builtins module itself, does, or taking one by name where `getattr`, `setattr`, `delattr`, `hasattr`, `operator.attrgetter`, `__getattribute__` and its kin, or a lookup in `vars()`, `globals()`, `locals()` or a `__dict__`, takes it, as `getattr(admit, "__globals__")` does. A log string that names one passes;
- writing what a function runs, its `__code__`, `__defaults__` or `__kwdefaults__`, by assignment or `setattr`, other than to a literal, which no function runs;
- using `sys.meta_path`, `sys.path_hooks` or `sys.path_importer_cache`, which can load another module in place of one the proof takes as it ships;
- using `sys.modules` other than to look a module up, or test it with `in`, by a constant name, and importing or patching by a computed or relative name.

What the gate still assumes, as [D-0260](../decisions/journal/D-0260.jsonl) lists it:
- that reflection the parse can't follow doesn't write those modules' names, as computed lookups on other objects and calls whose function is computed, such as `getattr(importlib, "import_module")(name)`, could;
- that what the application's dependencies and startup run doesn't, and neither does code a framework loads beside the module through configuration rather than an import;
- that nothing goes around Python's own semantics, as `ctypes` can;
- that no change to the import path loads a `nagini_contracts` a dependency installs in place of the stand-in;
- that nothing replaces the operations the function's contracts stand for at run time;
- that arguments are values of the builtin types their annotations name, not instances of subclasses with operators of their own or of other types, since checking callers is [#210](https://github.com/gitdek/invariant/issues/210)'s. A literal default the annotation doesn't admit is refused, not assumed ([D-0261](../decisions/journal/D-0261.jsonl));
- that the calls exploration records meet the proof's preconditions. A proof that qualifies by observed calls shows the application runs the function. The recorded arguments aren't checked against its `Requires`, since checking callers is #210's;
- and Nagini's own model of the builtins it models, their exceptions included.

The attempt's `ComponentProofResult` names the function as verified only when Nagini passed, with its selected contracts, the declarations it read, the contracts it assumed, its original files and its tool's pins: the recipe, the platform and the input's fingerprint, as `in_place_input`, the pin that marks an in-place result. The image's local ID stays beside them in the attempt, never in the pins, so receipts match between machines. Proof attribution takes it, so beside the contracts' backing tests it shows a proof that rests on tested assumptions. Marked files keep their own adapter.

`INVARIANT_PYTHON_APPLICATION_PROOF_REAL=1 go test ./internal/verify -run '^TestPythonApplicationProof' -count=1 -timeout 60m` runs the pinned Nagini on that function, with `packaging`'s backing tests run in the application's real dependency image. It proves `admit`, rejects a body that breaks its postcondition at the original line, and reports an omitted contract without running Nagini. It also checks a handler for an exception a contract declares, and checks Nagini's names against the pinned image's sources. Last, it proves a function as the input copies it and then refuses the proof once a neighbor writes the module's constant through an alias, before Nagini runs.

[#359](https://github.com/gitdek/invariant/issues/359), #294's last step, has `invariant verify` prove the selected functions as part of the gate ([D-0260](../decisions/journal/D-0260.jsonl)). The refusals happen in two places:

- **At `verify.Check`, before any tool runs:** what the function's own source shows the gate can't prove. That's what the verifier input can't preserve of the function itself, an `async` function, a decorator, `Pure` included, since conformance binds only undecorated functions, `Assume`, a contract that couldn't run as Nagini reads it, and arithmetic the pinned Nagini models apart from Python, as the paragraphs below say.
- **Once the session has staged the application and selected its dependency image, before any test, conformance, backing test or Nagini runs:** a `nagini_contracts` that isn't the stand-in, or another one anywhere else, and what depends on the rest of the application: its bindings, its trusted contracts and its production sources.

Then it runs the application's tests, its conformance and the trusted contracts' backing tests, and then the pinned Nagini on each input. A selected proof passes the gate only when all of these hold:

- Nagini proved the function from its original bytes, with no error and nothing it reached unaccounted for.
- Every trusted contract it assumed has tested support.
- Conformance on the same function passed in the same run, against the same source bytes, or its declared calls qualify on both accepted and declared-refused attempts of one complete exploration with passing transition, state and attempted-step conformance. The gate's collector binds both routes to that very staged declaration, so the function Nagini proved is the one the application runs.

A proof that falls short fails the gate, as an untested declared contract does, and the receipt's failures say why. If Nagini verified it, the report marks its row `unqualified`, a mark later attribution keeps, and the receipt calls it not qualified and leaves it out of the proved count. A receipt counts an in-place result only beside the attempt its `in_place_input` pin names, and says so when the report doesn't hold it. The tests, conformance and contract evidence stay as they ran.

The module that holds a selected function imports `nagini_contracts` to state its contract, and the application has to provide it wherever it runs. Ship Invariant's runtime stand-in, the `nagini_contracts` package in [`internal/verify/naginiruntime`](../internal/verify/naginiruntime), at the application's root, exactly as it is, and no other `nagini_contracts` anywhere else in the application, which a change to the import path could load instead ([D-0260](../decisions/journal/D-0260.jsonl)). The gate checks that the package holds exactly the stand-in's `__init__.py` and `contracts.py`, byte for byte. An installed distribution doesn't count: the only one is Nagini itself, whose `Result()` returns `None`, so `Ensures(Result() >= admitted)` would raise at runtime. The bytes alone don't settle what the stand-in binds when the module runs: another module could add a `min` to it before the function's module star-imports it, or put another module in its place in `sys.modules`. So the scan for writes above protects the stand-in, and the builtins, as it does the module. The function may read only the Nagini names the stand-in exports, its `__all__`, a tuple.

Python runs each contract statement where it stands, and evaluates its arguments then, whatever Nagini reads lazily: a postcondition runs on entry, though Nagini reads it at exit, and an `Implies`' consequent runs whether or not its antecedent holds. So the gate takes only contracts Python can evaluate without failing:

- Nagini's names appear only in contract statements, `Requires`, `Ensures`, `Exsures`, `Decreases`, `Assert` and `Invariant`, called as the stand-in takes them. The stand-in's `Implies` is always true, so `if Implies(count > 100, False) and ...` is refused.
- A contract's arguments hold only names, literals and tuples of them, `Result()`, `Old()` and the stand-in's other names, comparisons, `and`, `or` and `not`, conditional expressions, `+`, `-` and `*`, `//` and `%` by a positive integer literal, written as a plain int constant and not a bool, as `n % 2 == 0` and `n // 10` do, unary `+` and `-`, and calls of `len`, `abs` and `isinstance`. The pinned Nagini models those three builtins. `isinstance` takes only `int`, `bool`, `str`, `bytes`, `list`, `tuple`, `dict`, `set` or `object`. `min` and `max` are left out, since their one-argument form fails on an empty list where Nagini only requires one that isn't.
- A contract may order, do arithmetic on, and take the `len` or `abs` of only what the gate can tell has one builtin type that operation can't fail on: by a parameter's annotation, a literal, a display, the builtin that makes it, the module's literal constant, or, for a local, every binding of it. So a parameter's literal default must be of the type its annotation names, and `limit: int = None` is refused ([D-0261](../decisions/journal/D-0261.jsonl)). `%` takes only an int, since it formats a string, and a sequence is repeated only by an int literal.
- An `Optional` or a `Union` may appear only in `is`, `is not` and `isinstance` tests, since Python would apply anything else to `None` or another type. Nagini proves `Requires(Implies(limit is not None, limit >= 0))`, yet Python evaluates `limit >= 0` on entry and raises `TypeError` for `limit=None`, so the gate refuses it, and `limit // 2` and `limit + 1` there too.
- Anything else is refused: a subscript, a division, a floor division or modulo by anything but a positive integer literal, a power, a shift, a bitwise operator, an attribute read, any other call, a list, set or dict, a comprehension or a lambda. So `Ensures(Implies(len(name) > 0, Result() - count <= len(name) // len(name)))` is refused, since it raises on entry for an empty name.
- `Result()` returns an inert value that answers every operator a contract may apply to it, reflected forms included, made afresh, class and all, on every call. Testing `Result()` for membership needs a tuple the contract lists, since a string can't hold that value.
- A contract reads a local only where every path to it binds the local first, since Nagini checks that a local is bound only where it reads it.

The function's body and contract, and every trusted contract's abstraction the proof assumes, hold only the arithmetic the pinned Nagini models as Python does:

- `//` and `%` only by a positive integer literal. Nagini divides as Viper's Euclidean division does, which isn't Python's for a negative divisor: with `DIV = -2`, it proves `count + 1 // DIV` returns at least `count`, while Python returns `count - 1`.
- No `&`, `|`, `^` or `~`, which Nagini models apart from Python on negative ints.
- No `/` and no float at all: a float or complex literal, `float()`, a float annotation, a float constant or an abstraction that deals in floats. Nagini leaves floats uninterpreted, while Python fails converting an int too large for one.
- Multiplication only of what the gate can tell are both ints, or by an int literal, since repeating a sequence past what CPython can index fails where Nagini's model doesn't.
- None of the methods behind those operators read by name: `__truediv__`, `__floordiv__`, `__mod__`, `__divmod__`, `__and__`, `__or__`, `__xor__` and `__mul__`, with their reflected and in-place forms, and `__invert__`, `__float__` and `__complex__`. Nagini models a call by name as it does the operator: it proves `(7).__floordiv__(n) == -3` for `n = -2`, as it does `7 // n`, where Python gives `-4` ([#380](https://github.com/gitdek/invariant/issues/380), [D-0264](../decisions/journal/D-0264.jsonl)).
- None of that arithmetic's other names, which the pinned Nagini can't translate, so they fail closed today, though a later Nagini could model them as it models the operators ([D-0268](../decisions/journal/D-0268.jsonl)). That's the `operator` module's functions for those operators, such as `operator.floordiv` and `operator.or_`, whether read, imported, aliased or handed to `functools.reduce`, and the module held other than to read one of its functions by name. It's also `divmod`, `pow` other than as `pow(x, y)`, and the builtins module's `float`, `complex`, `divmod` and `pow`. And it's any of those names, or a method's behind an operator, in a string or a module's string constant that a lookup by name takes, or that keys a namespace, as `getattr(x, '__floordiv__')` and `getattr(builtins, 'float')` do. A name computed at run time stays assumed, as other reflection the parse can't follow does.

Shifts and `**` stay, since Nagini's preconditions on them are where Python raises. A core marked `# +nagini` is held to the same rule, as [What a Python proof may rest on](#what-a-python-proof-may-rest-on) says.

### From conformance to tested contracts to proof

The three steps build on each other, and each keeps what the one before established:

1. **Check the application as it is.** A model, a conformance driver and a manifest beside the unchanged code. The gate tests what the application actually does against the model, at the ratified bounds: sampled, or in every state it reaches with every step tried and one size larger ([Manually adopting an existing Python application](#manually-adopting-an-existing-python-application)).
2. **Back the boundary with tests.** Declare a trusted contract for each operation a critical function relies on: the standard library, a dependency or the application's own code. The gate runs each contract's backing tests against the real implementation, and passes only when every declared contract is tested ([Trusted dependency contracts](#trusted-dependency-contracts)).
3. **Prove the function in place.** Annotate the function with Nagini's contract where it lives, and select its proof beside its conformance. The gate proves its own body against its own contract, assuming the trusted contracts it reaches.

The receipt keeps the three apart:

- **Proved:** the selected function's own body, against its own contract, on the condition that the trusted contracts it assumed hold.
- **Tested, never proved:** those contracts, supported by their backing tests.
- **Tested against the model:** the surrounding behavior, at its actual bounds and sampling.
- **Unchecked for proof:** everything else, the function's neighbors in the same module included.

No step proves a dependency, an unselected function or the whole application. In-place proof supports top-level functions that select their own inline contract, within the limits the table above lists. The gate doesn't check callers against a proved function's preconditions; #210 leaves that for later.

`INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify -run '^TestPythonApplicationGate(Proves|Refuses|ObservedDecision793)' -count=1 -timeout 60m` runs the gate's in-place proof cases and the observed admission-decision fixture on a counter application with real Docker, the pinned Python image, TLC, the pinned Nagini and the installed `packaging` wheel, and locally too:

- The valid proof passes, and so does one whose contract holds only what Python runs as Nagini reads it.
- A body that breaks its postcondition only where conformance never goes fails at the original line.
- An omitted contract fails without Nagini running.
- A failing backing test leaves the proof without its condition and fails the gate, and so does failing conformance on the function.
- A changed `packaging` version changes the contract's binding and the fingerprint.
- The stand-in's three refusals: a changed `contracts.py`, another file in the package, and a Nagini name it doesn't export.
- Three applications that change at import time what the proof takes as it ships, by adding a `min` to the stand-in, putting another module in its place in `sys.modules` or rebinding `builtins.min`, are refused before Nagini runs.
- Five contracts Python can't run as Nagini reads them are refused before any tool runs: an `Implies` consequent that divides, `Implies` as a value, a local an `Implies` reads before every path binds it, `Result()` tested for membership in a string, and an `Optional` ordered under an `Implies` guard, which Nagini proves.
- A body that divides by a negative constant, which Nagini models as Viper's Euclidean division, is refused before any tool runs, and so is one that divides by it through `__floordiv__` ([#380](https://github.com/gitdek/invariant/issues/380)).
- A production module only the conformance driver loads is refused when it uses `sys.meta_path`, `sys.path_hooks` or an opaque `exec`, before Nagini runs, and the proof passes beside one that only does what ordinary code does, with a `setup.py` that calls `exec`.

## Application dependencies

Python projects can declare application dependencies in a project-root `requirements.txt`. The gate follows local `-r`/`--requirement` and `-c`/`--constraint` inputs recursively, including shared files in both roles. Supported files are UTF-8, optionally with a leading BOM, with LF or CRLF, comments, single trailing backslash continuations, named PyPI requirements with extras, version specifiers, unevaluated markers and SHA-256 hashes. Only ASCII spaces separate hash options. Includes must stay inside the project at every traversed directory, without symlinks or cycles. URLs, paths, archives, editables, environment interpolation and other pip options are refused, as are invalid encoding, bare CR, encoding declarations in comments, other line separators, unsupported backslashes and ambiguous marker literals containing `#` or option tokens.

Strict preflight runs before Docker. On a fresh daemon, each dependency API, the environment script's included, acquires the existing digest-pinned Python base for `linux/amd64` only when it is missing or unavailable for that platform. Acquisition failure stops the operation with Docker's diagnostic. That base's own pip then parses the original files offline, with `--pull=never`, absolute Python in isolated mode (`-I`), a clean working directory and a read-only project mount. Every report and row field must be present and non-null before policy checks; explicit empty values remain valid. Missing specifiers or markers cannot silently remove a pin or condition. Pip refusals and incomplete or failed reports stop selection and execution, even with a cached dependency image.

Pip's report produces `canonical/requirements.txt` and `canonical/constraints.txt`, one requirement and its hashes per line. Installation reads only those literal-directory copies, without changing the application. The image build uses isolated Python from a clean directory and installs only public-PyPI wheels with `pip --target` into a separate library directory. Only installation has network access; source builds and private indexes are unsupported. Docker-client proxy defaults, including credentials and both cases of proxy variables, are suppressed for gate containers and image builds without changing the user's configuration or daemon selection.

Checks run the base's absolute interpreter with site startup, user sites and implicit source paths disabled, and hash seed 0 effective for compilation, tests, ordinary conformance and larger-bound runs, including projects without requirements. After startup, trusted standard-library and gate paths precede the application root and driver directory, which precede installed libraries. Import resolution also protects application namespace packages from dependency regular packages, including nested namespaces. Installed scripts stay off PATH; `.pth`, `sitecustomize` and `usercustomize` do not run on startup. Imported dependencies execute as part of the application under test and receive no separate proof claim.

One immutable image ID supplies installation evidence and ordinary and larger-bound checks. Receipts and fingerprints record the base, platform, complete recipe identity, original and canonical input hashes, and resolved wheel names, versions, filenames and SHA-256 hashes, also through component attribution. Cached images retain their actual resolution; a fresh build of unpinned requirements may resolve differently. Nagini keeps its separate pinned dependencies. Projects without `requirements.txt` keep the standard-library path. Python existing-code discovery and staging are available through the foundation APIs above; the direct session/test adapter is available under #315, with sampled driver read-back under #318; #327 connects sampled TLC checking, report attribution and the public gate, and #342 exhaustive and attempted-step checks and the check one size larger, under the same startup in both runs. #350 runs trusted contracts' backing tests in the same environment, and #359 proves selected functions in place, with Nagini in its own image.

This implements [#227's ratified plan](../.invariant/plans/issue-227.json), on the existing toolchain, conformance and isolation foundations ([D-0013](../decisions/journal/D-0013.jsonl), [D-0029](../decisions/journal/D-0029.jsonl), [D-0030](../decisions/journal/D-0030.jsonl), [D-0031](../decisions/journal/D-0031.jsonl), [D-0032](../decisions/journal/D-0032.jsonl), [D-0047](../decisions/journal/D-0047.jsonl), [D-0054](../decisions/journal/D-0054.jsonl), [D-0122](../decisions/journal/D-0122.jsonl)). The dependency policy is recorded as [D-0178](../decisions/journal/D-0178.jsonl). This suite checks it with real Docker and PyPI, and passed 44 of 44 on [#309](https://github.com/gitdek/invariant/pull/309):

```sh
INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependenc' -count=1 -timeout 30m
```

## What a Python proof may rest on

A proof is only as good as the prover's model of the language, and the pinned Nagini models some arithmetic apart from CPython. For `n = -2` it proves `7 // n == -3` and `7 % n == 1`, where Python gives `-4` and `-1`. It proves `-4 | 1 != -3`, and it raises nothing for `[0] * n` past what CPython can index, or for `n + 0.5` and `n / 3` with an `n` too large for a float, where Python raises `OverflowError`. So before Nagini runs, the gate reads each file marked `# +nagini` with the host's `python3`, which parses it and never runs it, and refuses one that holds such arithmetic anywhere ([#380](https://github.com/gitdek/invariant/issues/380), [D-0264](../decisions/journal/D-0264.jsonl)). That covers a function or method, its contract, a lambda, a default, and the module's and its classes' own statements, which Nagini reads with its own arithmetic too, so to Nagini a module's `NEG = 7 // -2` is `-3`. The rule is the one [an in-place proof applies](#proving-a-function-in-place):

- `//` and `%` only by a positive int literal.
- None of `&`, `|`, `^` and `~`.
- No float at all.
- Multiplication only of what the gate can tell are both ints, or by an int literal.
- None of the methods behind those operators called by name, such as `n.__or__(1)`.
- None of that arithmetic's other names, such as `operator.floordiv`, `divmod`, three-argument `pow`, `builtins.float` or a lookup by a string such as `getattr(n, '__floordiv__')` ([D-0268](../decisions/journal/D-0268.jsonl)).

A core may hold `/` only in `Acc`'s permission fraction, a positive int literal over one at least as large, as `Acc(s.n, 1/2)`. Nagini reads that as a permission, never as Python's division. A refused file fails the code check, saying where and why, as in `core.py:193: refused before Nagini runs: the line holds a bitwise operator, which the pinned Nagini models apart from Python on negative ints`. Nagini never sees the file, and the rest of the gate's evidence stands. Today's three cores, the two two-phase commits above and the log buffer below, pass, since their only `/` is `Acc`'s `1/2` and `1/4`.

A proved core can rest only on itself, the standard library and the modules Nagini models, so the gate refuses, before Nagini runs, a marked file that imports anything its package holds, marked or not ([#383](https://github.com/gitdek/invariant/issues/383), [D-0275](../decisions/journal/D-0275.jsonl)). It found two ways such an import lets a proof rest on what nothing proved:

- **Nagini takes an imported module's contracts without proving its bodies.** The pinned Nagini proved `fdiv(7, -2) == -3` in a core that imports an unmarked helper whose contract says `Result() == a // b`, and verified a core through a helper whose body breaks its own contract, a helper Nagini rejects when it runs on it alone. A `helper.pyi` stub beside a marked `helper.py` stood in for it, and a plain `json.py` in the package stood in for the standard library's `json`.
- **Where the code runs, Python binds the name to whatever its path finds first.** The project root comes before the package, so a bare `import helper` of a marked sibling, which Nagini would prove, bound to a plain root `helper.py` whose `inc` returns `n + 2`. The gate passed and counted the core's `bump` proved, and `bump(0) == 2` where it ran, against its postcondition.

So the gate reads every import a marked file holds, in a function, a branch or a `try` as at the top, and refuses each of what the package holds: a module, a stub, a directory, a stub package or a compiled module, a test or another marked file among them, as well as a relative import and the package by its own name. A refusal reads like the arithmetic rule's, as in `core.py:3: refused before Nagini runs: the line imports helper, which the package holds as helper.py, and a proved core can rest only on itself, the standard library and the modules Nagini models`, and the receipt names the core's functions as ones Nagini never saw. A marked file the core would import is proved alone. Imports of the standard library, of `nagini_contracts` and of anything the package doesn't hold are unaffected, and a top-level name the standard library provides is the standard library's whatever the package holds, since that's the module Python loads. Nagini proves each marked file in a directory holding only that file, so it reads no module of the project.

The gate also refuses a marked file where Python may load something else in its place, as it refuses a compiled module where [an in-place proof's](#proving-a-function-in-place) module would be ([D-0273](../decisions/journal/D-0273.jsonl), [D-0276](../decisions/journal/D-0276.jsonl)):

- A compiled module of the file's name: an extension such as `core.cpython-313-x86_64-linux-gnu.so` or `core.pyd`, which Python's finder takes before the source, or sourceless bytecode such as `core.pyc`, which it takes only after the source and the gate refuses anyway.
- The bytecode its `__pycache__` holds for it, which CPython 3.13 runs in place of the source when it's unchecked.
- A package of its name, which Python takes before the file.

A function [proved in place](#proving-a-function-in-place) can't reach a neighbor's contracts the way an import does: its verifier input binds every name it reads from beyond its module only to a trusted contract, and Nagini's directory holds only that input. Today's three cores import nothing of their package, so they run as before.
