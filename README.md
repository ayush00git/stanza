<p align="center">
  <img src="app/public/stanza.svg" alt="Stanza" width="140" />
</p>

<h1 align="center">Stanza</h1>

<p align="center">
  A structure-based, resistance-aware small-molecule design pipeline for covalent targets.
</p>

---

## What it is

Stanza is a drug-design loop that treats a **resistance mutation** as a first-class
input. Given a protein target (by UniProt accession) and a point mutation, it rebuilds
the mutant pocket from an experimental structure, asks Claude for candidate molecules
conditioned on that pocket, docks each candidate into a **matched wild-type / mutant
structure pair**, and ranks the results.

The backend is Go (Gin, `:8080`). Cheminformatics and structural biology run in Python
helpers (`scripts/`) that the Go services shell out to — RDKit, PDBFixer/OpenMM,
OpenBabel, fpocket, and AutoDock Vina. The frontend is React + TypeScript + Vite with
Mol\* structure viewers (`app/`). Persistence is Postgres via embedded, ordered SQL
migrations (`store/migrations/`); without a database the server degrades gracefully to
in-memory runs.

The reference target is **KRAS G12C**, and the interesting part of Stanza is what it does
*not* claim about it.

## The covalent-selectivity insight

Read this before trusting any number Stanza prints.

KRAS G12C's selectivity is **covalent**, not shape-based. A drug slides into the
switch-II pocket of wild-type and mutant KRAS with essentially identical *reversible*
affinity — pan-KRAS binders engage WT, G12C, G12D, G12V and G13D at Kd ≈ 10–40 nM, and
adagrasib itself binds wild-type KRAS tightly and non-covalently. What the mutant alone
offers is a **Cys12 thiol** for the warhead to bond. AutoDock Vina scores
*non-covalently* and is blind to that bond.

Two consequences shape the whole design:

**1. `selectivity = wt_score − mutant_score` is the honest non-covalent margin, and it
reads ≈ 0 for a covalent target.** That is the correct answer, not a bug. Gly12→Cys12
barely perturbs the reversible contact set, so `wt_score ≈ mutant_score` to within
~0.1 kcal/mol by construction. Non-covalent docking cannot separate the tracks, and it
should not.

**2. The covalent signal is a dimensionless feasibility ∈ [0,1], reported *beside* the
affinity and never folded into it.** It is measured from the docked geometry
(`scripts/covalent.py`):

```
feasibility = distance_score × angle_score
```

| Term | Full credit | Zero credit | Grounding |
|---|---|---|---|
| `distance_score` | reach ≤ **3.50 Å** | reach ≥ **4.00 Å** | 3.50 Å is the Bondi S···C van der Waals contact (C 1.70 + S 1.80); 4.00 Å is the published covalent-competence line |
| `angle_score` | within **±15°** of ideal | beyond **±40°** | ideal is **105°** (Bürgi–Dunitz, sp2 Michael acceptor) or **180°** (SN2 backside attack on a haloacetamide) |

Only Vina modes within **2.0 kcal/mol** of the best mode may contribute geometry, so a
floppy ligand cannot buy reach with a pose the receptor never actually holds. `reach` is
the **median** warhead-carbon → Cys12-SG distance across replicate docking seeds.

An earlier version added a constant **4.0 kcal/mol "covalent credit"** to the mutant
score. It was removed, end to end. Covalent potency is **kinetic** (`kinact/KI`, spanning
~76 → ~35,000 M⁻¹s⁻¹ from ARS-853 to adagrasib), and wild-type Gly12 has no thiol at all,
so the discrimination is *unbounded*, not a few kcal/mol. Expressing it in kcal/mol was a
category error, and a single constant cannot rank binders that span two orders of
magnitude in efficiency. `models.CovalentDock` now carries **no energy** — only the
feasibility and the geometry that produced it.

## How it works

Seven stages, run per resistance run:

1. **Structure acquisition** — an experimental holo → apo ladder (with residue
   verification), falling back to AlphaFold. `services/structure_acquisition.go`.
2. **Mutagenesis** — builds a matched WT/mutant pair from **one** base structure so both
   tracks share a backbone frame. Exactly one side chain is rebuilt; missing loops are
   deliberately not modelled. `scripts/mutate.py` (PDBFixer), `services/mutagenesis.go`.
3. **Pocket analysis** — fpocket on both tracks, plus the WT→mutant delta.
   `services/fpocket.go`, `services/mutation_pockets.go`.
4. **Dual-track docking** — AutoDock Vina into both pockets over shared box and seeds.
   `services/dual_dock.go`.
5. **RDKit validation** — parse, canonicalize, dedupe (run-scoped by InChIKey), and a
   drug-likeness pre-filter (MW, QED, Rule-of-Five, optional synthetic accessibility)
   before spending the docking budget. A curated site widens the gate to the weight window
   it declares. `scripts/validate.py`, `services/validation.go`.
6. **Generation** — Claude proposes SMILES via a tool call, conditioned on the pocket
   context, the WT→mutant delta, curated site guidance, and the scored history of what
   has already been docked. `services/generation.go`.
7. **Selectivity scoring and ranking** — a composite fitness over four normalised terms.
   `scoring/selectivity.go`.

### The reference target is curated, not derived

KRAS G12C is built on **PDB 6OIM** (sotorasib covalently bound to Cys12), *not* the
AlphaFold model. The switch-II pocket is **cryptic**: it only opens around a bound
inhibitor and is absent from apo / AlphaFold structures, where the drug docks weaker and
leaves the warhead beyond bonding range. This is curated in `services/known_sites.go` as
a `SiteTemplate` (which structure to build the pair on) plus a `SiteGuidance` (the
covalent mechanism, the His95/Tyr96/Gln99 pharmacophore, a 430–620 Da weight window, and
the prior art the generator must not simply re-derive).

The weight window reaches the drug-likeness pre-filter as well as the prompt. It has to:
Lipinski's rule of five is a heuristic for oral absorption, not a law, and this pocket is
only addressable by molecules that break it. Under the default 500 Da ceiling the
pre-filter discards **sotorasib (533 Da), ARS-1620 (540) and adagrasib (574)** — every
approved-or-clinical G12C inhibitor — and adagrasib fails the QED floor besides. A filter
that drops the approved drug cannot judge a molecule designed to resemble it.

### Determinism and noise control

- **Ligand conformers** are generated by RDKit ETKDG under a fixed seed
  (`scripts/ligprep.py`). `obabel --gen3d` is unseeded and returned a different structure
  every call, which mattered for the covalent reach distance.
- **Both tracks are docked under the same replicate seeds** (`{42, 1337, 7}`, three), and
  every reported affinity is the **median**. Vina's search occasionally settles in a bad
  local minimum per (molecule, receptor, seed); a single-seed answer once reported an
  outlier as fact — a molecule whose true margin is +0.09 kcal/mol was published at +1.03.
  Replicates run concurrently in a bounded pool.
- **Exhaustiveness is 16**, twice Vina's default. At 8 the search is *bimodal* on some
  ligands: it finds either a deep pose with the warhead 5.8 Å from the thiol or a
  shallower one at 3.85 Å, and the covalent verdict follows whichever it found. Seeds
  cannot fix that — they resample the same two basins. Vina is bit-deterministic given
  (seed, cpu, box, ligand), so this is basin selection, not RNG.

  Raising exhaustiveness **narrows** the problem; it does not remove it. On two measured
  ligands it took one from a straddling verdict to a stable one and cut the other's
  flip-prone three-seed subsets from 3-in-10 to 0-in-10. Others still show a reach spread
  of several ångström at 16. For those, the answer is `uncertain`, and that is the correct
  answer.
- A molecule whose covalent call **flips with the docking seed** is flagged `uncertain`,
  surfaced to the user, and contributes **0** to fitness — ranking a coin flip on its
  median would launder noise into signal. It is the backstop for ligands whose search
  stays genuinely bimodal, and it fires in practice, not just in principle.
- The docking box reaches Vina at three decimal places. For a ligand with a bimodal search
  that quantisation is not cosmetic: it selects a basin.

### Fitness

The leaderboard fitness (`scoring/selectivity.go`) is a weighted sum of four
pool-normalised terms. The default split is tuned for a covalent target:

| Term | Weight | Note |
|---|---|---|
| Covalent feasibility | 0.40 | the only covalent evidence a docked pose yields |
| Mutant potency (−mutant_score) | 0.30 | next-best discriminator |
| Drug-likeness (QED) | 0.20 | keeps the board drug-like |
| Non-covalent selectivity | 0.10 | ≈ 0 for a covalent target; down-weighted, not dropped, so genuinely non-covalent runs still use it |

For a run with no covalent molecules the feasibility term normalises to zero and drops out
automatically; the pool then ranks exactly as the pre-covalent scorer did.

## What Stanza can and cannot claim

**It can claim:** *"Of the molecules proposed, these carry a cysteine-reactive warhead
that, in a rigid-receptor dock of the sotorasib-opened switch-II pocket, can reach the
Cys12 thiol within van der Waals contact **and** along a Bürgi–Dunitz trajectory that
permits nucleophilic attack, from a pose the receptor actually binds."* That is a
reproducible, unit-honest triage filter whose reach, angle, contributing mode and
seed-to-seed spread are all auditable.

**It cannot claim** a binding affinity, a selectivity (the reported `selectivity` is the
raw non-covalent margin, ≈ 0, meaning only that Vina cannot separate the tracks), a rank
order among covalent binders (that is kinetic and feasibility is blind to it), or that one
molecule is a better G12C inhibitor than another.

Stanza is a **warhead-reach filter, not a selectivity predictor.**

## Testing

```bash
go test ./...           # Go unit tests (services, scoring, …)
```

Tests do not launch Vina, fpocket or OpenBabel; the docking stages are exercised
end-to-end only against a real toolchain.

## Limitations & roadmap

State these plainly; they are not buried.

- **Feasibility is measured from a *free* dock.** Vina has no reason to aim the warhead at
  the thiol. The measurement is reproducible; the method is not rigorous. Genuine covalent
  docking (form the bond, search under the constraint, rescore the adduct) is **not
  implemented**.
- **Raw Vina affinities of −8 to −10 kcal/mol are optimistic.** Real reversible switch-II
  binding is weak (ARS-853 Ki ≈ 200 µM; adagrasib's reversible Ki ≈ 3.7 µM is the ceiling
  for an optimized drug). Rigid-receptor docking into a pocket a real drug pried open pays
  no reorganization penalty. A Vina score here is a *"fits the pocket"* signal, not a
  binding free energy.
- **Generation still leans on prior art.** Asked for KRAS G12C binders without steering,
  the model returned truncated ARS-1620 analogues below the viable weight range — one
  sharing 86% of its heavy atoms with the published compound by maximum common
  substructure. Site guidance now names the mechanism, the His95-groove substituent, the
  430–620 Da window and the prior art to avoid, and the pre-filter no longer deletes the
  molecules that guidance asks for. Whether that is *enough* to produce novel scaffolds is
  **not yet demonstrated**.
- **`uncertain` is a backstop, not a solution.** A ligand whose covalent call still flips
  with the seed at exhaustiveness 16 is reported as indistinguishable and excluded from
  the ranking. That is honest, but it means the tool declines to answer rather than
  answering correctly.

Roadmap, in priority order: confirm that generation now yields novel scaffolds in the
declared weight window; then genuine covalent docking (gnina's covalent mode or an
AutoDock4 flexible-residue protocol) with a reorganization penalty. Note that even
purpose-built covalent docking reaches only Spearman ρ ≈ 0.54 against experimental
potency — a better docker raises the ceiling, it does not make the number an energy.

## References

Constants and claims trace to the primary literature:

- Ostrem et al. 2013, *Nature* 503:548 — switch-II pocket discovery; GDP-state trapping; warheads
- Canon et al. 2019, *Nature* 575:217 — sotorasib (AMG 510); PDB 6OIM
- Patricelli et al. 2016, *Cancer Discovery* — ARS-853; reversible Ki ≈ 200 µM
- Hansen et al. 2018, *Nat Struct Mol Biol* — reactivity-driven G12C inhibition
- Vasta et al. 2022, *Nat Chem Biol* — reversible switch-II engagement of **wild-type** KRAS
- Meller et al. 2023, *JCTC* — AlphaFold does not open cryptic pockets

The full audit, with every claim independently sourced and mapped onto the code, is in
[`docs/features/10-covalent-validity-audit.md`](docs/features/10-covalent-validity-audit.md).

## License

Apache License 2.0. See [`LICENSE`](LICENSE).
</content>
</invoke>
