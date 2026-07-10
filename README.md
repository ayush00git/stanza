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

## In thirty seconds

**What it is:** a warhead-reach triage filter with auditable error bars. Not a selectivity
predictor, not an affinity predictor, not a drug-discovery engine.

**What it can claim:** *"Of the molecules proposed, these carry a cysteine-reactive warhead
that, in a rigid-receptor dock of the sotorasib-opened switch-II pocket, can reach the Cys12
thiol within van der Waals contact **and** along a Bürgi–Dunitz trajectory that permits
nucleophilic attack, from a pose the receptor actually holds."* Every number behind that
sentence — reach, angle, contributing Vina mode, seed-to-seed spread — is inspectable.

**What it cannot claim:** a binding affinity, a covalent selectivity, or a rank order among
covalent binders (that is kinetic, and feasibility is blind to it).

### Validated against a known answer

`selectivity` is structurally ≈0 on KRAS G12C — the mutation's advantage is covalent, not
shape-based — so that target can never demonstrate the dual-track machinery works. **BCR-ABL
T315I is the complementary case**: a *steric* resistance mutation, two real drugs, and an
answer known in advance. Pass criteria were fixed before the docks finished.

| | WT | T315I | selectivity | expected |
|---|---|---|---|---|
| **imatinib** | −12.59 | −12.24 | **−0.35** — defeated | negative (loses ~1000×) |
| **ponatinib** | −11.58 | −12.03 | **+0.45** — survives | ≈ 0 (designed to tolerate it) |

Separation **0.80 kcal/mol** against **0.13** of seed noise (~6×). Imatinib redocks to its
1IEP crystal pose at **1.07 Å** (symmetry-corrected), so the setup is sound independently of
the resistance question. **PASS** — on a target the pipeline was never tuned for.

**And the magnitude is wrong.** Experiment puts imatinib's penalty at ~4 kcal/mol; we recover
0.35, under a tenth. T315I resists only partly by steric bulk — much of it is destabilising
the DFG-out conformation imatinib requires, and a dock into one frozen frame is architecturally
blind to that. **Right answer, right reason, wrong size:** trust the sign and the ordering,
never the number. Method, caveats and the metric that flattered the pose by 1.8×:
[`docs/features/11-abl-t315i-positive-control.md`](docs/features/11-abl-t315i-positive-control.md).

**Three places this project proved its own headline numbers wrong** — the detail is in
[Limitations](#limitations--roadmap), and it is the reason to trust the rest:

| # | The claim | What measurement showed |
|---|---|---|
| 1 | Covalent selectivity is worth **+2.2 kcal/mol** | It was a *constant* wearing an energy's units. Since WT and mutant bind alike non-covalently, `selectivity = wt − (mut − credit)` collapsed to `selectivity = credit`. Deleted end to end; covalent evidence is now a dimensionless feasibility ∈ [0,1] reported *beside* the affinity. |
| 2 | The generator produces **novel molecules** | It produces novel *scaffolds*. 41/41 pass, zero Murcko collisions, max Tanimoto 0.485 — but a Murcko scaffold strips side chains, so it strips the warhead. 80% carry the same N-acyl saturated N-heterocycle all five reference drugs carry. The honest claim is **"novel scaffolds bearing conventional warhead chemistry."** (Aspirin also scores `novel_scaffold`, at Tanimoto 0.097, and cannot reach Cys12.) |
| 3 | **`feasibility = 1.00`** means the warhead can bond | On 4 of 5 molecules, the stronger check disagrees. Building the actual covalent adduct succeeds for exactly **one** molecule — the one scoring **0.10**. The molecule scoring 1.00 clashes worst, at 1.32 Å. This is a **live, unfixed defect** in a term carrying 0.40 of the fitness weight. It is surfaced, not folded in. |

A fourth, found in our own test suite: the SMILES for sotorasib, adagrasib and ARS-1620 that
guarded the pre-filter were **hand-typed fabrications** — plausible molecules that were not
those drugs, masses off by 28–109 Da. The test passed anyway. Reference structures now load
from `data/prior_art_kras_g12c.json`, by PubChem CID. See [Testing](#testing).

## The covalent-selectivity insight

Read this before trusting any number Stanza prints.

KRAS G12C's selectivity is **covalent**, not shape-based. A drug slides into the
switch-II pocket of wild-type and mutant KRAS with essentially identical *reversible*
affinity — pan-KRAS binders engage WT, G12C, G12D, G12V and G13D at Kd ≈ 10–40 nM, and
adagrasib itself binds wild-type KRAS tightly and non-covalently. What the mutant alone
offers is a **Cys12 thiol** for the warhead to bond. AutoDock Vina scores
*non-covalently* and is blind to that bond.

Two consequences shape the whole design:

**1. `selectivity = wt_score − mutant_score` is the honest non-covalent margin, and for a
covalent target it is uninformative.** That is the correct answer, not a bug. Gly12→Cys12
barely perturbs the reversible contact set, so the two tracks usually agree to within
~0.1 kcal/mol. Non-covalent docking cannot separate them on the mechanism that matters.

It is *uninformative*, not *zero*. Across seven in-window molecules docked into 6OIM, the
margin ran from **−0.83 to +0.30 kcal/mol** (median +0.08). Five sit inside ±0.3; the
outlier is the bulkiest, most rigid ligand. The Cys12 side chain shrinks the pocket by
~48 Å³ (fpocket, Δvolume), so a ligand that fills it can genuinely prefer wild-type on
sterics alone. A large `|selectivity|` therefore reports **steric fit**, never covalent
discrimination — reading it as the latter is the exact error the removed "covalent
credit" institutionalised.

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

It can also claim, now measured rather than assumed, that the steered generator **produces
novel scaffolds inside the declared 430–620 Da window, and that most of them clear the
geometry gate** — 7/7 novel, 5/7 feasible, 1 correctly rejected at 4.52 Å reach, 1
correctly flagged seed-dependent (2.77 Å spread across seeds).

And on a **steric** resistance mutation, where `selectivity` is a physically meaningful
quantity rather than a structural zero, it can claim the machinery **picks the right drug**:
imatinib −0.35 (defeated by ABL T315I), ponatinib +0.45 (survives it), separation 0.80
kcal/mol against 0.13 of seed noise, after redocking imatinib to its crystal pose at 1.07 Å.
The *sign and the ordering* are trustworthy. The *magnitude* is not — we recover 0.35 of an
experimental ~4 kcal/mol, because a rigid DFG-out receptor cannot represent the
conformational component of T315I resistance.

**It cannot claim** a binding affinity, a selectivity (the reported `selectivity` is the
raw non-covalent margin; when it is large it reports steric fit, not covalent
discrimination), a rank order among covalent binders (that is kinetic and feasibility is
blind to it), or that one molecule is a better G12C inhibitor than another. It cannot yet
claim that a high-feasibility molecule forms a *buildable* covalent adduct — on the
evidence so far, the correlation runs the wrong way.

Stanza is a **warhead-reach filter, not a selectivity predictor.**

## Testing

```bash
go test ./...           # Go unit tests (services, scoring, …)
```

Tests do not launch Vina, fpocket or OpenBabel; the docking stages are exercised
end-to-end only against a real toolchain.

Reference structures are **read from `data/prior_art_kras_g12c.json`, never typed into a
test.** An earlier revision hand-wrote SMILES for sotorasib, adagrasib and ARS-1620; all
three were plausible-looking molecules that were not those drugs — wrong InChIKey skeleton,
masses off by 28–109 Da. The pre-filter test built on them passed, because invented
molecules of roughly the right size behave roughly the right way. The structures in the
data file carry their PubChem CIDs; re-fetch them, do not retype them.

Novelty is audited out-of-band, not in the pipeline:

```bash
echo '{"query":[{"id":"m1","smiles":"..."}]}' | python3 scripts/novelty.py
```

The ABL T315I positive control is reproducible from a clean checkout — it fetches the
structure, builds the matched pair, derives the box from the crystal ligand, and runs the
12 docks. Nothing is hand-entered, and Vina is deterministic given (seed, cpu, box, ligand),
so the numbers reproduce bit-for-bit:

```bash
scripts/controls/abl_t315i.sh            # ~8 min, needs vina + obabel + RDKit/PDBFixer
```

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
- **Novelty is now measured, and the novelty is real — but it lives in the ring system,
  not the warhead.** `scripts/novelty.py` scores every molecule against the five published
  switch-II inhibitors (`data/prior_art_kras_g12c.json`, structures fetched from PubChem by
  CID). Across the 41 KRAS molecules generated so far: **41/41 novel scaffold**, zero exact
  or generic Bemis–Murcko collisions, 32 distinct frameworks, max ECFP4 Tanimoto **0.485**
  against any reference (median 0.278) — nothing near the 0.70 analogue line.

  That headline overstates it, because a Murcko scaffold strips side chains and therefore
  strips the warhead. Measured directly, **80%** carry an N-acyl saturated N-heterocycle
  (5/5 references do) and **50%** carry an acyl-piperazine on an arene (4/5 do). The model
  conserves the warhead-delivery module and innovates on the ring system it hangs from —
  which is what the prompt asks for and what a medicinal chemist would do. The defensible
  claim is *"novel scaffolds bearing conventional warhead chemistry,"* not *"novel
  molecules."* Novelty is also **orthogonal to feasibility**: aspirin scores
  `novel_scaffold` at Tanimoto 0.097 and cannot reach Cys12.

- **The tether check contradicts the feasibility score, and the score ignores it.** After
  measuring geometry, `covalent.py` tries to *build* the covalent adduct: bond S to the
  warhead carbon, MMFF-minimise with the cysteine backbone fixed, then verify the S–C bond
  closed to 1.81 ± 0.25 Å and that no ligand heavy atom sits within 2.0 Å of the receptor.
  Of the seven in-window molecules docked, five clear the geometry gate — and **only one
  produces a buildable adduct.** The other four clash (1.32–1.82 Å). The molecule scoring a
  perfect `feasibility = 1.00` clashes *worst*; the one that tethers cleanly (S–C 1.89 Å)
  scores **0.10**.

  A tempting explanation is that a short reach is *too* close — that closing to a 1.81 Å
  bond from 3.31 Å drags the ligand into the pocket wall, while a 3.94 Å pose has room to
  rotate in. **The data do not support asserting that.** Reach versus minimum contact gives
  Spearman ρ = 0.70 over n = 5 (exact permutation p = 0.12), and the relationship is not
  even monotonic: reach 3.54 Å clashes at 1.82 Å while reach 3.68 Å clashes at 1.44 Å. Five
  ligands, five scaffolds, one success — reach is confounded with everything else that
  varies. The contradiction between the two checks is a **measured fact**; the mechanism
  behind it is an **untested hypothesis**, and separating those is the whole point of this
  section.

  `feasibility = distance_score × angle_score` never sees the tether outcome, which is
  recorded only as a `note`. So the 0.40-weight fitness term is ranked on a proxy that the
  stronger structural check disagrees with on 4 of 5 molecules. Two caveats before treating
  the tether as ground truth: the minimisation runs **in vacuum** (the receptor is not in
  the force field, so the ligand relaxes into a wall it never feels), and the receptor is
  **rigid** (real side chains flex). The disagreement is a live, unresolved defect, not a
  settled verdict — it is surfaced rather than folded in.
- **`uncertain` is a backstop, not a solution.** A ligand whose covalent call still flips
  with the seed at exhaustiveness 16 is reported as indistinguishable and excluded from
  the ranking. That is honest, but it means the tool declines to answer rather than
  answering correctly.
- **A rigid receptor recovers the sign of a resistance mutation, not its size.** The ABL
  T315I control gets imatinib's direction right and separates it from ponatinib by 0.80
  kcal/mol — but the experimental penalty is ~4 kcal/mol, so we recover under a tenth of it.
  Resistance that works by shifting the protein's *conformation* (T315I destabilises the
  DFG-out state imatinib needs) is invisible to a dock into one frozen frame. The pipeline
  sees steric bumps. It does not see conformational selection, and no amount of
  exhaustiveness fixes that. Flexible side chains (`--flexres`) would recover part of it;
  an ensemble over conformers would recover more.

Roadmap, in priority order: resolve the feasibility/tether contradiction — minimise the
adduct with the receptor in the force field before trusting either signal, then decide
whether the tether outcome belongs *inside* the feasibility score or beside it; then
genuine covalent docking (gnina's covalent mode or an AutoDock4 flexible-residue protocol)
with a reorganization penalty, which would subsume both. Note that even purpose-built
covalent docking reaches only Spearman ρ ≈ 0.54 against experimental potency — a better
docker raises the ceiling, it does not make the number an energy.

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
