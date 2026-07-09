# Covalent Selectivity — Validity Audit

An audit of Stanza's KRAS G12C covalent pipeline against the experimental
literature, mapped onto the code that implements it. Every claim below was
independently searched, sourced, and adversarially checked.

**One-line verdict.** The pipeline can honestly claim *"this molecule's warhead can
reach Cys12 in the switch-II pocket."* It cannot claim selectivity, and the
`selectivity` column is not an energy.

---

## What the pipeline computes today

```
selectivity = wt_score − (mutant_score − covalent_credit)      services/dual_dock.go:95
covalent_credit = 4.0 kcal/mol            if reach ≤ 3.5 Å     services/covalent.go:45,51
                = linear ramp → 0         at reach = 5.0 Å
                = 0                       beyond 5.0 Å
reach = min(warhead C → Cys12 SG) over 20 Vina modes × 5 seeds  scripts/covalent.py:193
```

`wt_score` and `mutant_score` are raw AutoDock Vina affinities against receptors
built from PDB 6OIM chain A with residue 12 set to Gly and Cys respectively
(`services/known_sites.go:79`).

**The load-bearing fact:** with the credit removed, `wt_score ≈ mutant_score` to
within ~0.1 kcal/mol in every observed case. Therefore
`selectivity ≈ covalent_credit`, and `covalent_credit` is a function of exactly one
measured quantity — `reach`. Everything else is bookkeeping.

---

## Findings, mapped to code

### ✅ Correct: the receptor and the template

| Claim | Evidence |
|---|---|
| 6OIM is KRAS G12C + sotorasib (MOV) covalently on Cys12, + GDP + Mg²⁺, 1.65 Å, chain A | RCSB 6OIM; Canon et al. 2019 *Nature* 575:217 |
| 6OIM is the community-standard S-IIP docking template; AMG-510 is redocked into it as a positive control | published G12C virtual-screening workflows |
| The switch-II pocket is cryptic — "not apparent in previous structures of Ras" | Ostrem et al. 2013 *Nature* 503:548 |
| AlphaFold does **not** reproduce the open S-IIP; it collapses to the closed apo state | Meller et al. 2023 *JCTC*, "Accelerating Cryptic Pocket Discovery Using AlphaFold" |
| 7ROV is KRAS **G12D** + GMPPCP + cyclic peptide MP-9903 — correctly rejected | RCSB 7ROV |
| `SC_BOND = 1.81 Å` is the right thioether C–S length | standard bond tables (1.81–1.83 Å) |

`services/known_sites.go` and `services/mutagenesis.go` are sound. Stripping GDP+Mg
is defensible for **rigid-receptor** docking (no protein atom moves), but would be
wrong the moment MD or flexible-switch docking is added — the nucleotide and Mg²⁺
hold the switch regions in place.

### ✅ Correct, and the single best-grounded constant

`ReachIdeal = 3.5 Å` (`services/covalent.go:45`) is *exactly* the Bondi van der
Waals contact distance for S···C (C 1.70 Å + S 1.80 Å = 3.50 Å). In a non-covalent
dock the warhead carbon and SG are non-bonded spheres that cannot approach closer.
It is the correct full-credit anchor for a non-covalent pose.

### ❌ Wrong: `ReachMax = 5.0 Å` is beyond the competence line

Published covalent-docking practice draws "capable of forming a covalent bond" at
**< 4 Å** S···electrophile. Approved covalent drugs show S-to-electrophilic-carbon
distances of 2.98–3.78 Å in their co-crystals. Engines that decide a bond has formed
use ~2.8 Å.

Stanza awards 25–50% credit at 4.0–5.0 Å (`services/covalent.go:51`). That tail is
outside anything the literature calls covalently competent.

### ❌ Missing: there is no angle gate

A near-attack conformation requires **both** a distance (≤ 3.2 Å) **and** an angle:
the Bürgi–Dunitz trajectory of ~105° for nucleophilic attack on a trigonal centre.
QM on the actual thio-Michael reaction puts the transition-state S–Cβ at 2.38–2.70 Å
with a strongly preferred synclinal approach.

`scan_reach` (`scripts/covalent.py:193`) measures distance only. A warhead can sit
3.4 Å from SG with an alkene plane that makes attack geometrically impossible, and
Stanza will pay it full credit.

### ❌ Biased: `min` over 20 modes × 5 seeds

Taking the **minimum** of a distance over stochastic samples is a downward-biased
estimator — `E[min]` falls as sample count grows. With no angle gate and **no
docking-score gate**, it rewards conformational promiscuity: a large floppy ligand
that samples many orientations wins on reach without ever binding well.

Considering non-top poses is legitimate (the covalently competent pose is often not
the best non-covalent one). An *unguarded minimum* is not.

Our own seed data confirms it: reach varied ±0.16–1.09 Å across five seeds, and one
molecule's credit swung **0.00 ↔ 3.42 kcal/mol** on the RNG alone. The
`uncertain` flag (`models/run.go`) detects this honestly — but detecting noise is
not the same as not measuring noise.

Static docking also overpredicts covalent feasibility for G12C specifically:
multiple acrylamides reach near-reactive proximity to Cys12 in docking, then lose
productive alignment within nanoseconds of MD.

### ❌ Category error: `MaxCredit = 4.0 kcal/mol`

The number is not absurd. Thiol-Michael addition of cysteine to acrylamide has
ΔG_rxn ≈ −8.3 kcal/mol (DFT, M06-2X/SMD); for monoactivated acceptors the
*reversible* equilibrium is only −4.6 to −5.0 kcal/mol. So 4.0 sits in range.

But that is a coincidence, and the framing is wrong in three ways:

1. **Covalent potency is kinetic, not thermodynamic.** It is reported as
   `kinact/KI` (M⁻¹s⁻¹), not a ΔG. For an irreversible warhead there is no
   equilibrium Kd — occupancy → 100% given time.

2. **A flat credit cannot rank covalent binders.** Real efficiencies span
   two-plus orders of magnitude:

   | inhibitor | kinact/KI (M⁻¹s⁻¹) |
   |---|---|
   | ARS-853 | 76 – 336 |
   | ARS-1620 | ~1,100 |
   | sotorasib (AMG 510) | ~9,900 |
   | adagrasib (MRTX849) | ~35,000 |

   Stanza assigns all four the same 4.0 kcal/mol.

3. **WT/mutant discrimination is not a finite ΔΔG.** Wild-type Gly12 has no thiol,
   so the Michael addition *cannot occur*. The discrimination is unbounded (adagrasib
   is reported >1,000-fold selective), not 3 kcal/mol. Capping it at 4.0 is both
   dimensionally and physically wrong.

For scale: 4.0 kcal/mol is ≈ 850-fold in equilibrium affinity at 298 K. It is a
larger perturbation than the entire WT/mutant Vina difference, which is why it
dominates the ranking completely.

### ❌ Over-scored: raw Vina affinities of −7.6 to −9.5 kcal/mol

Real reversible S-IIP binding is extraordinarily weak:

| ligand | reversible affinity | ≈ ΔG |
|---|---|---|
| ARS-853 | Ki ≈ 200 µM (KI ≈ 36 µM) | ≈ −5.0 kcal/mol |
| sotorasib | KI > 100 µM; non-saturating by SPR at 500 µM | weaker than −4.5 |
| adagrasib | reversible Ki ≈ 3.7 µM — the **ceiling** for an optimized drug | ≈ −7.4 |

Stanza's fragments score −8 to −10, i.e. they are predicted to out-bind adagrasib
reversibly. They do not. Two causes:

- Rigid-receptor docking into a pocket that a 561 Da drug pried open pays **no
  reorganization penalty**. This inflates both tracks equally.
- Vina's affinity power is weak (Pearson R ≈ 0.5–0.6) and it over-rewards large,
  lipophilic ligands. A Vina kcal/mol is a "fits the pocket" signal, not a ΔG.

### ⚠️ The WT track is legitimate — but it validates nothing

An earlier concern, that mutating Cys12→Gly on the sotorasib-opened backbone creates
a pocket wild-type KRAS never has, **is refuted**. Vasta et al. 2022
*Nat Chem Biol* (Shokat lab) show WT KRAS is "the most vulnerable of all RAS
isoforms to reversible engagement"; adagrasib and MRTX1257 bind GDP-loaded
non-G12C KRAS tightly and non-covalently. The cryptic S-IIP forms in WT too.

The consequence, though, cuts against us. The pan-KRAS binder BI-2865 binds WT,
G12C, G12D, G12V and G13D all at Kd ≈ 10–40 nM. **Non-covalent docking cannot
separate wild-type from G12C, and should not.** So `wt_score ≈ mutant_score` is not
evidence that our molecules are selective — it is a restatement of the fact that
Vina is blind to the mechanism that creates selectivity.

### ❌ The molecules are recollections, not designs

| # | SMILES | relation to prior art |
|---|---|---|
| 3 | `O=C(CCl)N1CCN(c2ncnc3cc(O)c(F)cc23)CC1` | **86% of heavy atoms** (MCS 19 atoms / 21 bonds) shared with ARS-1620 — the 4-(piperazinyl)quinazoline + acyl warhead with the fluorophenol deleted |
| 1,4,5,6 | acryloyl-piperazine | exact substructure of sotorasib, ARS-1620 and divarasib |
| all | acrylamide / vinyl sulfonamide / chloroacetamide | the three warheads disclosed in Ostrem 2013 |

Whole-molecule ECFP4 Tanimoto to the real drugs is only 0.13–0.40, but that is a
**size artifact**: the proposals are truncations. MCS, not global Tanimoto, is the
honest metric here.

Worse, they are outside the viable size range and truncated in the wrong place:

- Every S-IIP inhibitor with cellular activity is **431–622 Da**
  (ARS-853 433, ARS-1620 431, sotorasib 561, adagrasib 604, divarasib 622).
  Stanza's proposals are **300–393 Da**.
- Molecules 1–4 lack the pendant aryl that occupies the **His95 cryptic groove** —
  the single largest potency driver in this series, and the change that took
  ARS-853 (2.5 µM) to ARS-1620.

Reaching Cys12 with a small acrylamide is genuinely plausible — Ostrem's original
tethering fragments were < 300 Da and did covalently label Cys12. But reaching Cys12
is *necessary, not sufficient*: those fragments bound weakly.

---

## What the pipeline may and may not claim

**May claim.** "Of the molecules proposed, these N carry a cysteine-reactive warhead
that, in a rigid-receptor dock of the sotorasib-opened switch-II pocket, places its
electrophilic carbon within van der Waals contact of the Cys12 thiol." That is a
useful triage filter and it is now measured reproducibly.

**May not claim.** Any of:

- a selectivity in kcal/mol
- a binding affinity
- a rank order among covalent binders
- that a molecule is a better G12C inhibitor than another

---

## Remediation, in priority order

1. **Stop printing kcal/mol for selectivity.** Rename the surfaced quantity to what
   it measures — warhead reach / covalent feasibility. This is a labelling fix and
   it converts a misleading number into an honest one. Touches
   `scoring/selectivity.go`, `models/run.go`, and the UI.
2. **Tighten and gate the geometry.** `ReachMax` 5.0 → 4.0 Å; add a Bürgi–Dunitz
   angle check (~105° ± tolerance) in `scripts/covalent.py`; require the pose
   contributing the minimum reach to also score within a window of the best mode,
   so the minimum cannot be bought with promiscuity.
3. **Fix generation.** Steer away from the ARS-1620 chemotype; target 430–620 Da;
   require an aryl substituent reaching the His95 groove. Otherwise the pipeline
   rediscovers 2016.
4. **Only then**, consider genuine covalent docking (form the bond, rescore the
   adduct) — gnina's covalent mode or the AutoDock4 flexible-residue protocol. Note
   that even purpose-built covalent docking reaches only Spearman ρ ≈ 0.54 against
   experimental potency, and mainstream engines (GOLD, ICM-Pro, DOCKTITE, FlexX)
   deliberately do **not** add a covalent bond term to their scoring functions —
   which is precisely the move Stanza currently makes.

---

## Primary sources

- Ostrem et al. 2013, *Nature* 503:548 — S-IIP discovery; GDP-state trapping; warheads
- Canon et al. 2019, *Nature* 575:217 — AMG 510 / sotorasib; PDB 6OIM
- Patricelli et al. 2016, *Cancer Discovery* — ARS-853, Ki ≈ 200 µM
- Hansen et al. 2018, *Nat Struct Mol Biol* — reactivity-driven G12C inhibition
- Vasta et al. 2022, *Nat Chem Biol* — reversible S-IIP engagement of **wild-type** KRAS
- Meller et al. 2023, *JCTC* — AlphaFold does not open cryptic pockets
- JBC 2025, "Biophysical and structural analysis of KRAS switch-II pocket inhibitors" — SPR non-saturation
