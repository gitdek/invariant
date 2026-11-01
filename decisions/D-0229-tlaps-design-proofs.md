---
id: D-0229
title: TLAPS proves a ratified invariant of the model at every size
date: 2026-10-31
door: one-way
status: ratified
ratified_by: "@gitdek"
proposed_by: agent
source: "#360, from @gitdek's EasyProver fact-check on 2026-10-31: \"Yeah let's try doing that\""
---

# D-0229 · TLAPS proves a ratified invariant of the model at every size

**Goal.** Close the one gap in what Invariant claims of a design: TLC checks a model's invariants only at the ratified bounds. Gobra and Nagini prove the code's operations against their contracts at every size, and the check one size larger compares code and model once more, but nothing has proved the design itself beyond the bounds. With TLAPS, the TLA+ Proof System, a project can have the gate prove a ratified safety invariant of its model in every behavior, for every value of its constants.

## Why now

@gitdek asked for a fact-check of EasyProver (Ganguly et al., [arXiv 2610.38174](https://arxiv.org/abs/2610.38174)), an agentic Lean prover, against Invariant. Its lesson for Invariant is its split between untrusted search and trusted acceptance. Agents search for proofs as they like. A separate check accepts a proof only when the statement is unchanged, nothing is left unproved and every axiom is allowed. Invariant already works that way for code: agents never touch a ratified lock, and the gate in CI decides. Its designs, though, were never proved at all. Since every project already has a ratified TLA+ model, TLAPS proves them in place, with no second model in Lean.

## What it is

- **The proof is search; the gate is acceptance.** A coding agent, or a person, writes a proof at `.invariant/proofs/<Inv>.tla`. The gate writes the module around it, and the theorem that counts, `Spec => []<Inv>`, itself. The proof can only define an inductive invariant `IndInv_<Inv>` and prove three first-order lemmas whose statements the gate fixes. The gate's own last step is temporal, by PTL, which LS4 decides.
- **Nothing is taken on trust that the gate can check.** A proof can't add an axiom, assumption, constant, variable, instance or module, and can't omit a proof. tlapm runs with `--strict`, so an omitted or missing proof is an error, as is any failed obligation. It doesn't trust its exit status alone, since tlapm exits 0 with failed obligations without `--strict`.
- **Constructs tlapm proves unsoundly are refused, and the refusals are checked complete.** Several soundness issues were open when this was built:
  - [tlaplus/tlapm#309](https://github.com/tlaplus/tlapm/issues/309): a term tlapm doesn't translate under a binder, such as an unexpanded `ENABLED` or a temporal formula, proves false theorems with every backend but LS4.
  - [#304](https://github.com/tlaplus/tlapm/issues/304): overlapping `CASE` arms do under SMT.
  - [#324](https://github.com/tlaplus/tlapm/issues/324): negative exponents do.
  - [#235](https://github.com/tlaplus/tlapm/issues/235): the parser drops a set-enumeration element of the form `e \in S`, reading `{e \in S}` as the empty set.
  - [#310](https://github.com/tlaplus/tlapm/issues/310): tlapm names each term it abstracts for a backend by 20 bits of an unchecked hash, so a collision proves a false theorem: by PTL, by SMT or Zenon on temporal formulas, and, as testing the pinned tlapm showed, by SMT on two primed hidden definitions, with no temporal operator at all.

  Neither the model nor the proof may use `ENABLED`, `\cdot`, temporal operators, `CASE` expressions, exponents, or a set enumeration whose element is a membership, and the proof may not name the PTL or LS4 backend. The gate also refuses a proof when any two definition names, the proof's or the model's, would abstract to the same atom (D-0243). The model's spec must be the gate's, `Init /\ [][Next]_vars`, and it may hold no assumption or unproved theorem. The gate carries the list of tlapm's known soundness tests at the pinned commit; a test maps every known issue to a refusal a representative triggers or to a reason it can't arise that it checks (the len/DOMAIN issue [#51](https://github.com/tlaplus/tlapm/issues/51) needs the old SMT encoding, which only a debug flag the gate never passes selects), and a test against the pinned tlapm keeps that list matching its suite — so a newly known unsoundness can't pass unmapped (D-0231, D-0240).
- **It's evidence of its own.** The receipt reports it on a row of its own, "Design · TLAPS", beside TLC's row, never folded into it or into what the receipt says of the code. The proof is bound to the SHA-256 of the model and of the proof, since `Init` and `Next` aren't pinned. It names the TLAPS library facts it cites, which those modules state and tlapm doesn't re-check, and it records tlapm's commit and each backend's version.
- **It's pinned.** tlapm's only current builds come from a rolling pre-release, 1.6.0-pre. The gate pins its x86_64 Linux tarball by SHA-256, built at commit `4744c0f`. The tarball bundles Z3 4.8.9, Zenon 0.8.4, Isabelle2025 and LS4. It sits on the Temurin image TLC runs in, pinned by digest, in an image built for linux/amd64 from a recipe in the repository, as Nagini's is. A replaced asset fails the build instead of installing another tlapm.

## Options considered

| Option | What it gives | What it costs |
| :-- | :-- | :-- |
| **A. TLAPS on the ratified model, the gate writing the theorem** (recommended) | Every size for the design, with no second model. The theorem and the acceptance are the gate's, so a proof can only be wrong by not going through | A new tool in the trusted base, with a weaker trust model than a kernel's. Proofs for richer models (sequences, cardinality) take real work, by an agent or a person |
| B. A Lean model and proof beside the TLA+ model, as EasyProver's targets are | Lean's small kernel checks every proof | A second model that must be shown to say what the ratified one says, which is its own unproved step. It doubles what people ratify or trust |
| C. More bounds for TLC: a second size, or a larger one | Nothing new to trust | Still finite. "Every size" stays a claim about the code only |
| D. Keep designs checked only at the bounds | Nothing to build | The gap EasyProver's lesson points at stays open |

## Trust

A TLAPS proof is weaker than a Lean proof. tlapm translates obligations for backends, and SMT and Zenon results aren't certified. Isabelle can check Zenon's proofs, but the gate doesn't ask it to. Open soundness issues, listed above, show the translation can be wrong. The gate refuses the constructs those issues touch, keeps TLC's check at the bounds beside the proof, and reports TLAPS's result apart from everything else. A proved invariant says that, under TLAPS's trust model, the model never breaks it at any size. It says nothing about the code.

Two caveats stand with the ratification:

- **The completeness check trusts tlapm's own `known:` marking.** It maps every soundness test tlapm marks known. An unsoundness tlapm hasn't found, or hasn't marked known, isn't on the list, and the gate rules it out only if a refusal happens to cover its construct, as the name-collision check (D-0243) covers a #310 route no known test exercises.
- **The pin moves only with the completeness test rerun.** A new tlapm means re-deriving the known-soundness list at its commit, rerunning `TestTLAPSClosesEveryKnownSoundnessIssue` and `TestTLAPSRealKnownSoundnessTestsMatch`, and checking that the new tlapm still names its atoms as `tlapsAtom` does. The list-match test fails until the list's commit is the pin's.

## Questions for @gitdek, with recommendations

Ratified on 2026-11-01 as recommended, on @gitdek's standing authorization, after two adversarial reviews of #364: A, the pinned pre-release, and a failed selected proof failing the gate.

1. **A, B, C or D?** Recommend A.
2. **Pin the rolling pre-release, or wait for a stable release?** Recommend pinning it. 1.5.0, the last stable release, is from 2022 and has soundness fixes 1.6.0 doesn't need. A replaced asset fails closed, and moving the pin is a decision, as TLC's was (D-0016).
3. **Fail the gate when a selected proof fails?** Recommend yes. Selecting an invariant claims the evidence, so a proof that doesn't go through fails the gate, as an unreached witness does.

## What would reopen it

- tlapm publishing a stable 1.6 release, or removing the pre-release's asset: move the pin, or mirror the tarball.
- A soundness issue the gate's refusals don't cover: widen them, or drop the evidence until it's fixed.
- A need for liveness or refinement at every size: those are temporal proofs the gate doesn't write yet.
- A model the refusals rule out that a project needs proved: support its construct once tlapm proves it soundly.
