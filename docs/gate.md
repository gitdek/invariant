# The gate

What the gate's receipt claims about the code, and how a project proves a model's invariant at every size. [The README](../README.md#the-gate) lists every check the gate runs, and [the Python page](python.md) covers existing Python applications.

## What a receipt claims

For projects without explicit components, code assurance comes from successful results: **proved** requires a successful proof naming at least one function; otherwise successful agreement or conformance establishes **tested against the model**; otherwise the code is **unchecked**. The gate records that assurance in JSON. Discovering source functions or passing a build establishes neither proof nor model testing. These claims are separate from the gate's pass or fail result: a failed check still fails the gate, while an unrelated failure does not erase successful evidence.

Markdown receipts and the dashboard derive these claims from evidence even when an older JSON report contains a stale assurance label. **Proved at every size** additionally requires a successful check one size larger. The dashboard says **tested in every state** only for successful exhaustive conformance with positive, matching visited and model state counts and no failed attempt check. A failed attempt check still leaves evidence for the transitions that conformance successfully checked. This correction builds on the tool-output receipts in [D-0013](../decisions/D-0013-slice-plan.md), the proof and conformance paths in [D-0024](../decisions/D-0024-checking-typescript-and-python.md) and [D-0031](../decisions/journal/D-0031.jsonl), and the larger check in [D-0070](../decisions/journal/D-0070.jsonl).

## TLC resource budget

Under [#497's ratified plan](../.invariant/plans/issue-497.json), each gate invocation shares one TLC budget across full and model-only verification, existing Python applications, conformance batches, the check one size larger and stall retries. Independent checks wait for capacity and still report when another check fails. Successful checks keep their configurations, evidence, result order and receipt fingerprints.

The gate reads `NCPU` and `MemTotal` from the selected Docker daemon, including when the client selects a remote daemon. It reserves a quarter of that memory and permits at most one slot per CPU and per available 2 GiB. Each normal slot has a 2 GiB container and a 1536 MiB maximum Java heap. On smaller machines, one container uses the available memory rounded down to whole MiB; known capacity below a 256 MiB container is refused. If discovery fails, times out or returns malformed data, or either capacity is unknown, the fallback is one 1 GiB container with a 768 MiB heap.

`invariant verify -tlc-parallel N PROJECT` and the optional `verify.Options{TLCParallel: N}` argument to `verify.Run` and `verify.RunModel` can lower this concurrency ceiling. Zero, the default, selects automatically; a positive value cannot raise the computed ceiling, and negatives are rejected before tool setup. The limit changes scheduling, not what is checked.

Every TLC container has explicit `--memory` and equal `--memory-swap` limits, disabling extra swap, and `-Xmx` sets its maximum heap to three quarters of its memory limit rounded down to whole MiB. Standalone runners without a `Budget` use the 1 GiB container and 768 MiB heap defaults. Graph dumps use the same limits and admission control.

Waiting for a slot consumes neither the silence window nor `Runner.CheckWithin`'s per-check execution allowance. The caller's overall deadline still applies while waiting and running. Cancellation of queued work starts no container and returns an error wrapping the context cause. An admitted check retains its slot until Docker cleanup completes, including through an identical stall retry; both attempts share the remaining execution allowance and caller deadline.

Java `OutOfMemoryError`, `Java heap space` and `GC overhead limit exceeded` produce operational failures naming the check. They override incomplete success and counterexample output and never trigger a stall retry. An unexpected exit 137 reports that the named check was killed, with possible memory exhaustion: the exit alone does not establish its cause. A kill caused by removing a stalled container still permits the stall retry unless its output reports Java memory exhaustion. These failures provide no partial model-violation evidence, and successful independent checks remain in the report.

This policy builds on the pinned runner ([D-0016](../decisions/journal/D-0016.jsonl)), gate-owned configurations ([D-0017](../decisions/journal/D-0017.jsonl)), conformance batches ([D-0047](../decisions/journal/D-0047.jsonl)), the larger check ([D-0070](../decisions/journal/D-0070.jsonl)), cleanup ([D-0111](../decisions/journal/D-0111.jsonl)) and stall retries ([D-0306](../decisions/journal/D-0306.jsonl)). [D-0315](../decisions/journal/D-0315.jsonl) records the policy.

## Silent TLC checks

Under [#470's ratified plan](../.invariant/plans/issue-470.json), `tlc.Runner.Check` stops and removes a TLC container after fifteen uninterrupted minutes without stdout or stderr, including before its first output. It then runs the identical check once more in a fresh container. Any output bytes restart the silence window, including bytes without a newline on either stream. A check that keeps printing can run as long as its caller allows. `Runner.SilenceTimeout` is a `time.Duration` override for short tests; zero or a negative value uses fifteen minutes.

Both attempts share the caller's original context and remaining time. Cancellation or deadline expiry takes priority over a stall, stops and removes the container without another retry, and returns an error wrapping the context cause and identifying the check as cut off. An already ended context starts no container. The first container is removed before the retry starts.

A second stall returns a `Failed` result whose message says `TLC stopped making progress` and includes the last twenty lines of that attempt's combined stdout and stderr. Checks that finish keep their existing outcomes and diagnostics and are never retried. After a first stall, only the completed retry supplies evidence.

These rules build on the pinned TLC runner ([D-0016](../decisions/journal/D-0016.jsonl)), container cleanup ([D-0111](../decisions/journal/D-0111.jsonl)), and keeping the specification and reference pages current ([D-0122](../decisions/journal/D-0122.jsonl), [D-0294](../decisions/journal/D-0294.jsonl)). The silence-and-retry policy is [D-0306](../decisions/journal/D-0306.jsonl).

## Model parse and semantic failures

When SANY, TLA+'s parser and semantic checker, prevents TLC from checking a module, the failed result's message includes SANY's complete error diagnostics and source locations in printed order before TLC's closing `Parsing or semantic analysis failed.` message. A misindented conjunction retains the offending item's line and column range and the enclosing list's range; undefined operators retain each module, source range and explanation. Routine parsing and progress lines are omitted.

These diagnostics reach the formalizer's check feedback, the gate's JSON and Markdown receipts, and the factory's failure posts and pull request body. Each uses its existing rendering and clipping rules; the result's message itself is not truncated. The raw unframed chunks in `Result.Printed`, outcome classification, other TLC errors and the last-twenty-lines fallback for other failures keep their existing behavior ([#450](https://github.com/gitdek/invariant/issues/450), [D-0298](../decisions/journal/D-0298.jsonl)).

## Proving a model's invariant at every size

TLC checks the design within the ratified bounds, and Gobra and Nagini prove the code's operations at every size. A project can also have the gate prove a ratified invariant of the model itself at every size, with TLAPS, the TLA+ Proof System ([#360](https://github.com/gitdek/invariant/issues/360), [D-0229](../decisions/D-0229-tlaps-design-proofs.md)). The idea comes from EasyProver ([arXiv 2610.38174](https://arxiv.org/abs/2610.38174)): an agent's proof is untrusted search, and the gate alone accepts it, against a theorem the gate writes itself.

The project's manifest selects the invariant, and its proof sits beside the lock, as [`examples/05-connection-pool`](../examples/05-connection-pool) does for `NoSharedConnection`:

```json
"tlaps": ["NoSharedConnection"]
```

`.invariant/proofs/NoSharedConnection.tla` is the body of a module the gate writes. It defines an inductive invariant and proves three lemmas whose statements are the gate's ([D-0231](../decisions/journal/D-0231.jsonl)):

```tla
IndInv_NoSharedConnection == TypeOK /\ NoSharedConnection

LEMMA NoSharedConnection_Init == Init => IndInv_NoSharedConnection
BY DEF Init, IndInv_NoSharedConnection, TypeOK, NoSharedConnection

LEMMA NoSharedConnection_Step == IndInv_NoSharedConnection /\ [Next]_vars => IndInv_NoSharedConnection'
BY DEF IndInv_NoSharedConnection, TypeOK, NoSharedConnection, Next, vars, Acquire, Refuse, Release, Out, Conns

LEMMA NoSharedConnection_Implies == IndInv_NoSharedConnection => NoSharedConnection
BY DEF IndInv_NoSharedConnection
```

The gate extends the ratified model and TLAPS around the proof, then adds the only theorem that counts, `NoSharedConnection_AtEverySize == Spec => []NoSharedConnection`. It proves that by PTL from the three lemmas, and LS4 decides it. tlapm proves the rest; for the pool, SMT took all three lemmas, with no decomposition. The receipt shows it on a row of its own:

```
| Design · TLAPS | ✅ model invariant proved at every size (TLAPS) | `NoSharedConnection`: 8 obligations (1 ls4, 3 smt, 4 trivial) · tlapm `eef4b65` |
```

What the gate refuses, so a proof can only be wrong by not going through:

- A proof may not add an axiom, assumption, constant, variable, instance, module, or `LOCAL` or `RECURSIVE` definition, omit a proof, or cite TLAPS's temporal rules, such as `RuleINV1`, since the gate takes the only temporal step itself. tlapm runs with `--strict`, since without it tlapm exits 0 with obligations failed and passes over a missing proof.
- A proof may extend only TLAPS's standard modules. The receipt names any of their facts it cites, which those modules state without a proof tlapm checks here.
- Neither the model nor the proof may use `ENABLED`, `\cdot`, temporal operators, `CASE` expressions, exponents, or a set enumeration whose element is a membership such as `{e \in S}` (which tlapm reads as the empty set), and the proof may not name the PTL or LS4 backend. These are the constructs open soundness issues in tlapm touch ([tlaplus/tlapm#309](https://github.com/tlaplus/tlapm/issues/309), [#304](https://github.com/tlaplus/tlapm/issues/304), [#306](https://github.com/tlaplus/tlapm/issues/306), [#300](https://github.com/tlaplus/tlapm/issues/300), [#235](https://github.com/tlaplus/tlapm/issues/235), [#310](https://github.com/tlaplus/tlapm/issues/310)). Because tlapm names each term it abstracts for a backend by 20 bits of an unchecked hash, and a primed hidden definition is such a term, the gate also refuses a proof when two definition names, the proof's or the model's, would abstract to the same atom. The refusals are checked complete against the pinned tlapm's own known-soundness tests, so a newly known unsoundness can't slip past (the one exception, the len/DOMAIN issue [#51](https://github.com/tlaplus/tlapm/issues/51), needs an old SMT encoding the gate never selects).
- The model's spec must be `Init /\ [][Next]_vars`. It may hold no assumption or theorem, may extend only Naturals, Integers, Reals, Sequences, FiniteSets and Bags, and may not use `INSTANCE` or `RECURSIVE`.

What it proves, and what it doesn't:

- It proves the model never breaks the invariant, in any behavior, for every value of its constants. TLC's check within the bounds stays beside it.
- It says nothing about the code. What the receipt says of the code doesn't change.
- Its trust model is weaker than a kernel-checked proof's: SMT and Zenon results aren't certified, and tlapm's translation has the open issues above.

tlapm is pinned: 1.6.0-pre at commit `eef4b65`, from an unmodified copy of the release's x86_64 Linux tarball, held by Invariant's own release [`tlapm-1.6.0-pre-eef4b65`](https://github.com/gitdek/invariant/releases/tag/tlapm-1.6.0-pre-eef4b65) and pinned by SHA-256, with Z3 4.8.9, Zenon 0.8.4, Isabelle2025 and LS4, in a linux/amd64 image built on first use ([D-0269](../decisions/journal/D-0269.jsonl)). The tarball also carries a `tla2tools.jar` for tlapm's optional SANY parser, which the gate never selects, so tlapm parses with its own parser, the default. Upstream's release is rolling and replaced its asset twice on 2026-11-01, so the gate fetches that copy instead ([D-0272](../decisions/journal/D-0272.jsonl)). If its bytes ever changed, the image would fail to build rather than install another tlapm. Moving to a newer tlapm means validating its build and mirroring it the same way. To run the real tlapm tests:

```bash
INVARIANT_TLAPS_REAL=1 go test ./internal/verify -run '^TestTLAPSReal' -count=1 -timeout 30m
```

Existing Python applications can't select TLAPS proofs yet.
