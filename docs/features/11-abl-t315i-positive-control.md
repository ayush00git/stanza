# ABL T315I positive control

**Question.** Stanza's `selectivity = wt_score − mutant_score` is structurally uninformative
on KRAS G12C: the mutation confers *covalent* selectivity, so the two tracks bind alike and
the margin is ≈0 by construction. That leaves the dual-track machinery unvalidated — a
number that is supposed to be zero cannot demonstrate that it would be non-zero when it
should be.

BCR-ABL **T315I** is the complementary case. The gatekeeper Thr315→Ile is a *steric*
resistance mutation: it adds side-chain bulk and deletes the hydroxyl that accepts a
hydrogen bond from imatinib's anilino NH. Imatinib loses ~1000-fold potency against it
(~4 kcal/mol). Ponatinib was designed to survive it and retains sub-nanomolar activity.

Two known drugs, one known answer. If the machinery cannot separate them, `selectivity` is
not measuring anything anywhere.

## Reproducing it

```bash
scripts/controls/abl_t315i.sh            # ~8 min: fetch 1IEP, build the pair, 12 docks, analyse
scripts/controls/abl_t315i_analyse.py tmp/abl_control    # re-analyse without re-docking
```

The script is self-contained: it fetches the structure, derives the box from the crystal
ligand, and re-verifies the matched pair before reporting. Vina is bit-deterministic given
(seed, cpu, box, ligand), so the numbers below should reproduce exactly. Completed docks are
cached — delete `tmp/abl_control/out` to force a re-run.

## Method

Everything below uses the production settings from `services/dual_dock.go` —
exhaustiveness 16, `--cpu 2`, seeds {42, 1337, 7}, 20 modes, median over seeds — and the
production receptor prep (`obabel -xr`, non-PDBQT records stripped).

- **Base structure: PDB 1IEP, chain A, 2.10 Å.** ABL kinase domain with imatinib (`STI`)
  bound in the **DFG-out** conformation. This choice is load-bearing: imatinib *requires*
  DFG-out, and docking it into an active-conformation or AlphaFold model fails for reasons
  that have nothing to do with T315I. The pre-existing `P00519` run in the database was
  built from the AlphaFold model `AF-P00519-F1` and was **not** reused.
- **Matched pair.** Both receptors were produced by `scripts/mutate.py` from the same input
  (WT → THR, mutant → ILE), `--keep-chain A --strip-het`. Verified afterwards: **1096 shared
  backbone atoms, max deviation 0.0000 Å**, no heteroatoms, residue 315 the only difference
  (7 atoms → 8).
- **Box.** Centred on the crystallographic imatinib heavy-atom centroid
  (15.614, 53.380, 15.455), edge 24.739 Å — the pocket-sized rule from `boxSizeFor`
  (ligand span + 8 Å padding).
- **Ligands.** SMILES from PubChem by CID (imatinib 5291, ponatinib 24826799), embedded with
  `scripts/ligprep.py --seed 42`. Not hand-typed; see the fabricated-SMILES incident in the
  README's Testing section.
- 2 drugs × 2 receptors × 3 seeds = **12 docks**, 471 s wall clock on 6 cores.

Pass/fail criteria were fixed **before** the docks completed: correct sign for imatinib
(negative = prefers wild-type = resistance), imatinib penalised more than ponatinib, and the
margin compared against the worst seed-to-seed spread.

## Result 1 — redocking control

Does the WT dock reproduce imatinib's crystallographic pose? If not, nothing else means
anything.

| seed | symmetry-corrected in-place RMSD |
|---|---|
| 42 | **1.07 Å** |
| 1337 | **1.08 Å** |
| 7 | **1.07 Å** |

Success line is < 2.0 Å. RMSD is computed in place (no superposition) by mapping both the
crystal ligand and the docked pose onto the SMILES template and minimising over the
template's 4 automorphisms. A naive nearest-neighbour distance reports 0.60 Å — that metric
is a *lower bound* and flatters the pose by ~1.8×; it is recorded here only to note that it
should not be used.

## Result 2 — selectivity

`selectivity = wt − mut`, both scores negative, so **negative selectivity = prefers
wild-type = resistance**.

| drug | WT (3 seeds) | T315I (3 seeds) | median WT | median T315I | selectivity |
|---|---|---|---|---|---|
| imatinib | −12.59, −12.60, −12.55 | −12.16, −12.29, −12.24 | −12.59 | −12.24 | **−0.35** |
| ponatinib | −11.58, −11.67, −11.57 | −12.03, −12.02, −12.03 | −11.58 | −12.03 | **+0.45** |

- Worst seed-to-seed spread on any track: **0.13 kcal/mol**.
- Separation between the drugs: **0.80 kcal/mol**, ~6× the noise.

**PASS.** Correct sign, correct ordering, margin well outside seed noise. Ponatinib does not
merely tolerate the mutation in this measurement — it *prefers* it (+0.45), consistent with
its alkyne linker threading past the bulkier Ile315.

## What this does and does not establish

**Establishes:** the dual-track construction, the matched-frame mutagenesis, the box rule,
the median-over-seeds protocol and the selectivity sign convention are jointly capable of
identifying which of two real drugs a resistance mutation defeats — on a target the pipeline
was never tuned for, with the answer known in advance and the criteria fixed in advance.

**Does not establish** that the *magnitude* is meaningful. Experiment says imatinib loses
~4 kcal/mol; we recover **0.35**, low by roughly an order of magnitude. The reason is
structural, not statistical: T315I resistance is only partly steric bulk. A large part is
that the mutation destabilises the DFG-out conformation imatinib requires. Our receptor is
*frozen* in DFG-out, so the dock is forbidden from seeing that half of the mechanism. It
measures the bump and is blind to the conformational shift.

It also does not establish anything about the absolute affinities. −12.6 kcal/mol for
imatinib into the pocket imatinib itself pried open is a "fits the pocket" signal, not a
binding free energy — the same caveat that applies to the KRAS numbers.

**n = 1 per drug.** Two molecules is a sanity check, not a benchmark. A real evaluation
would run a series (nilotinib, dasatinib — which *is* partly T315I-sensitive — and bosutinib)
and report a rank correlation.
