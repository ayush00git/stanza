# Stanza — Feature Roadmap & Gap Analysis

Stanza's goal is **resistance-aware small-molecule design**: given a target and a
resistance **mutation**, generate chemistry that binds the *mutant* pocket while
sparing the *wild type*. The mutation is a first-class input threaded through
every stage; the payoff metric is a **selectivity margin** (`wt_score − mutant_score`),
not raw affinity.

This folder holds one focused spec per feature that is still **to build** or
**needs extension**. This page is the map: what exists today, where it diverges
from the plan, what to reuse, and the order to build in.

---

## Where we are today

The current codebase is a working **structure-based virtual-screening explorer**
built on a **monomer vs. dimer** axis (an oligomerization question), not the
resistance axis the product is about.

- **Backend (Go / Gin, `:8080`)** — UniProt + AlphaFold fetch, fpocket pocket
  detection on monomer + dimer, interface detection, a monomer/dimer pocket
  **comparison** (`ComparisonResult`: conserved / emergent / interface pockets,
  ΔDGI, druggability distributions), ChEMBL fragment lookup per pocket, and
  AutoDock Vina docking of a single SMILES into a single pocket. Jobs live in an
  **in-memory** store; there is no database and no queue.
- **Compute** — Go shells out directly to `fpocket`, `obabel`, and `vina`. There
  are no Python workers.
- **Molecules** — sourced from a **fixed ChEMBL library** per pocket. Nothing is
  generated.
- **Frontend (React / TS + Mol\*)** — search, a structure viewer (monomer +
  dimer, pLDDT), a pocket list, a docking panel (candidate molecules for the
  selected pocket), a results leaderboard ranked by raw binding affinity, docked
  pose overlay, and full-screen modes.

## The core gaps

1. **Wrong dual axis.** Everything dual today is *monomer vs. dimer*. The product
   is *wild type vs. mutant*. The mutation, mutagenesis, and the WT/mutant
   pocket delta do not exist yet.
2. **No generation.** Molecules come from a fixed library; there is no
   Claude-orchestrated generate → validate → dock → score → feedback loop.
3. **No selectivity.** Ranking uses one raw affinity; there is no dual-pocket
   docking, no `wt_score`/`mutant_score`, and no fitness function.
4. **No durable state.** Runs, molecules, poses, and scores are not persisted;
   there is no job queue, caching, or crash-resumable loop state.

## Reuse map — what already serves the new axis

The dual-track scaffolding is real; it just points at the wrong pair of
structures. Re-orient it from monomer/dimer to WT/mutant rather than rebuilding:

| Existing (monomer/dimer) | Re-serves (WT/mutant) |
|---|---|
| Two structures fetched + rendered side by side | WT structure + mutant structure |
| `services/pocket_compare.go` → `ComparisonResult` (conserved/emergent/delta) | WT↔mutant pocket delta, "what the mutation changed" |
| `services/fpocket.go` + `plddt.go` + `pocket_filter.go` | pocket analysis on both tracks (unchanged) |
| `services/docking.go` + `jobs.go` (Vina, pose PDB) | dock each molecule into **both** pockets |
| Frontend dual viewers, per-structure highlight + pose, leaderboard | WT/mutant comparison, mutated-residue highlight, selectivity board |
| `models.Pocket`, `models.Fragment`, `ResidueConfidence` | reused as-is; add mutation/track fields |

## Status matrix

Legend: **DONE** built · **EXTEND** exists, needs re-orientation · **BUILD** new.

| Stage / capability | Plan | Today | Spec |
|---|---|---|---|
| Run input + mutation as first-class | BUILD | — | [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md) |
| Structure acquisition (prefer experimental holo) | EXTEND | AlphaFold only | [`02-mutagenesis.md`](02-mutagenesis.md) (§ prerequisite) |
| Mutagenesis (WT → mutant) | BUILD | — | [`02-mutagenesis.md`](02-mutagenesis.md) |
| Dual WT/mutant pocket analysis + delta | EXTEND | monomer/dimer compare | [`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md) |
| Claude molecule generation loop | BUILD | — | [`06-generation-loop.md`](06-generation-loop.md) |
| Molecule validation / drug-likeness (RDKit) | BUILD | — | [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md) |
| Dual-track docking + idempotent caching | EXTEND | single-pocket Vina | [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md) |
| Selectivity scoring + ranking | BUILD | affinity-only board | [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md) |
| Persistence + job queue + workers | BUILD | in-memory | [`08-persistence-and-queue.md`](08-persistence-and-queue.md) |
| Frontend resistance UI | EXTEND | monomer/dimer viewer | [`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md) |

## Suggested build order

Ship the loop **closed and single-threaded** before optimizing throughput.

1. **Run lifecycle + mutation input** (`01`) — the spine everything hangs on.
2. **Mutagenesis** (`02`) — unblocks the whole resistance angle; do it early.
3. **Dual pocket analysis + delta** (`03`) — re-point the existing compare.
4. **Dual-track docking + caching** (`04`) — dock into both pockets.
5. **RDKit validation** (`05`) + **generation loop** (`06`) — close Claude →
   validate → dock → score → Claude synchronously first.
6. **Selectivity scoring + ranking** (`07`).
7. **Persistence + queue** (`08`) — only once the loop works end to end.
8. **Frontend resistance UI** (`09`) — the demo surface; last.

## Conventions used across these specs

- **Tracks.** Every structure / pocket / dock exists as a **WT track** and a
  **mutant track**. "Dual-track" means: do it for both, keep them paired.
- **Status tags.** Specs mark work as **BUILD** (new) or **EXTEND** (adapt
  existing code), and call out exactly which current files to touch or reuse.
- **Selectivity margin** = `wt_score − mutant_score`. Positive and large is the
  goal: binds the mutant, spares the WT.
- Each spec is **self-contained** (states its own current-state, design, data /
  API contract, dependencies, and acceptance criteria) so it can be picked up
  independently.
