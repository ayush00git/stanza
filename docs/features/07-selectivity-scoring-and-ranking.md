# Stanza — Selectivity Scoring & Ranking

**DONE** · Turn paired WT/mutant dock scores into a single, comparable
**fitness** per molecule — reward binding the *mutant* pocket, penalise binding
the *wild type* — then rank and select the pool.

Implemented in `scoring/selectivity.go` (`ScoreAndRank`, the `Scores` /
`RankedMolecule` / `Ranking` types, and the z-score / min-max normalisers) and
exposed as `GET /runs/:id/ranking` (`handlers.GetRunRankingHandler`), which joins
each dock to its QED from the run's validated candidates and ranks the run's docked
molecules by composite fitness. Query params: `norm=zscore|minmax`, `top=<int>`,
and `wp`/`ws`/`wq` weight overrides.

Adapted to the shipped, no-persistence design: the pool is the run's full dock set.
There are no generation rounds yet, so the round-scoped pool and the `round` /
`parent_id` lineage in the spec below are omitted (they return with the autonomous
loop and stage-8 persistence). The fitness math, sign conventions, normalisation,
tie-breaks, and JSON shapes below are what shipped.

---

## Goal

The payoff metric of the whole product is a **selectivity margin**, not raw
affinity. A molecule that docks strongly into both pockets is worthless here; the
winner docks *worse* into WT and *better* into mutant. This stage converts each
molecule's two dock scores (plus its drug-likeness) into:

1. a **selectivity margin** with an unambiguous sign convention,
2. a **composite fitness** with tunable weights and pool normalisation so the
   three terms (potency, selectivity, drug-likeness) are comparable, and
3. a **ranked, selected pool** with **lineage** (round, parent) attached — the
   ranking is what the loop ([`06-generation-loop.md`](06-generation-loop.md))
   reads to steer the next round and what the selectivity board
   ([`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md)) renders.

This **replaces** the raw-affinity leaderboard as the pipeline's ranking
authority.

## Current state

Built as of Stage 7 — see the status note above. The context that shaped it (and
which still describes the separate `/complex` oligomerization dock path):

- Docking (`services/jobs.go`) returns `DockingResult{ BindingAffinity, PosePDB, … }`
  — a **single** Vina affinity against a **single** pocket. There is no WT/mutant
  pairing, no fitness, no drug-likeness.
- The frontend `app/src/components/viewer/DockedResults.tsx` is a leaderboard that
  sorts completed docks by that one raw `binding_affinity` (most negative ranks
  #1). It knows nothing about a partner score.
- `app/src/lib/api.ts` mirrors the single-affinity `DockingResult` /
  `DockedPose`; there is no `Scores` or ranking type.
- Nothing is persisted — jobs live in an in-memory `JobStore`, so there is no
  molecule record, no lineage, and no round/parent to feed back.

So this stage is genuinely new. It consumes the **paired** dock output from
[`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md)
and the **QED** drug-likeness score from
[`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md), and writes
its records through [`08-persistence-and-queue.md`](08-persistence-and-queue.md).

## Design

### Sign conventions (read this before anything else)

Vina reports binding affinity in **kcal/mol as a negative number**: **more
negative = stronger binding**. Every downstream sign flows from that.

| Quantity | Definition | "Good" direction |
|---|---|---|
| `mutant_score` | Vina affinity of the molecule docked into the **mutant** pocket (kcal/mol, negative) | **more negative** — we want it to bind the mutant well |
| `wt_score` | Vina affinity of the same molecule docked into the **WT** pocket (kcal/mol, negative) | **less negative / closer to 0** — we want it to bind WT poorly |
| `selectivity` | `wt_score − mutant_score` | **large and positive** — WT binds worse, mutant binds better |

Why `selectivity = wt_score − mutant_score` has the sign it does:

- **Selective (good).** `mutant_score = −9.2`, `wt_score = −5.1` →
  `selectivity = (−5.1) − (−9.2) = +4.1`. Binds the mutant much harder than WT.
- **Non-selective (bad).** `mutant_score = −8.0`, `wt_score = −8.4` →
  `selectivity = (−8.4) − (−8.0) = −0.4`. Binds WT slightly *better* — the exact
  thing we are designing against; the negative margin flags it.

This matches the project-wide convention in
[`00-overview.md`](00-overview.md): **selectivity margin = `wt_score − mutant_score`,
positive-and-large is the goal.**

### The three fitness terms

Fitness combines three signals. They live on different scales and in different
directions, so each is first turned into a **"bigger = better, unitless"** raw
term, then pool-normalised (below).

1. **Mutant potency** `p = −mutant_score`. Negate so stronger mutant binding
   gives a larger positive number. (Units: kcal/mol magnitude.)
2. **Selectivity margin** `s = wt_score − mutant_score`. Already "bigger =
   better". (Units: kcal/mol.)
3. **Drug-likeness** `q = QED`, the RDKit quantitative drug-likeness score from
   [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md), already
   in `[0, 1]` with bigger = better. (Unitless.)

Note `p` and `s` are **not independent** — both fall as `mutant_score` rises —
which is deliberate: a molecule can win on potency, on selectivity, or on the
balance the weights encode. It cannot game selectivity by simply refusing to
bind anything, because that tanks the potency term (see risks).

### Pool normalisation

The three raw terms are incomparable as-is (a 4 kcal/mol margin vs. a 0.7 QED),
so summing them directly lets whichever has the largest numeric spread dominate.
**Normalise each term across the scored pool for the round (or the cumulative
run) before weighting.** Two supported modes, `zscore` (default) and `minmax`:

- **z-score** (default): `ẑ(x) = (x − μ_pool) / σ_pool`, using the mean and
  population std of that term over the pool. Guard `σ_pool = 0` (all-equal, e.g.
  a pool of one) → contribute `0`. Robust to outliers being few; centres each
  round on its own distribution.
- **min-max**: `x̂ = (x − min_pool) / (max_pool − min_pool)` into `[0, 1]`; guard
  a zero range → `0.5`. Bounded and easy to read, but a single outlier squashes
  the rest.

The **pool** is the set of molecules being ranked together — by default the
**round's** validated + fully-paired molecules, so each round is scored on its
own scale (keeps the loop's top/bottom feedback stable round to round). A
`run`-scoped mode normalises over every molecule seen so far in the run (better
for a final cross-round leaderboard). The scope is a parameter (see open
questions).

### Composite fitness

$$\text{fitness} = w_p\,\hat{z}(p) \;+\; w_s\,\hat{z}(s) \;+\; w_q\,\hat{z}(q)$$

where `ẑ(·)` is the chosen pool normalisation and the weights are tunable and
sum to 1. Defaults lean on selectivity, because it is the product's whole point:

| Weight | Term | Default |
|---|---|---|
| `w_s` | selectivity margin | **0.45** |
| `w_p` | mutant potency | **0.35** |
| `w_q` | drug-likeness (QED) | **0.20** |

Higher fitness = better. Because normalisation is pool-relative, **fitness is
only comparable within the same pool/run** — it is a ranking coordinate, not an
absolute score. Persist the raw `mutant_score`, `wt_score`, `selectivity`, and
`qed` alongside it so a molecule can be re-normalised or re-weighted later
without re-docking.

### Handling incomplete / invalid molecules

A molecule reaches this stage only if it validated (`05`) and both tracks docked
(`04`). Guard the rest:

- **Missing a track** (WT or mutant dock failed/errored) → cannot compute
  selectivity → mark `scores.status = "incomplete"`, set `fitness = null`, and
  **exclude from ranking and from the normalisation pool** (an absent term must
  not shift μ/σ). Surface it separately so the loop knows it was tried.
- **Invalid / no QED** → treat `q` as its pool minimum (worst) rather than
  dropping the molecule, so a strong-but-ugly binder still ranks but is
  penalised; record the substitution.
- **Positive dock scores** (Vina occasionally returns ≥ 0 for a bad fit) are
  passed through unchanged — the sign convention already handles them (a `wt_score`
  of `+0.5` is *great* for selectivity).

### Ranking & selection

1. Compute `Scores` for every complete molecule in the pool.
2. Normalise each term over the pool; compute `fitness`.
3. **Sort by `fitness` descending**; assign 1-based `rank`. Ties broken by
   `selectivity` desc, then `mutant_score` asc (more negative first).
4. **Select** the final pool: top-`N` (default configurable, e.g. 20) and/or
   `fitness ≥ threshold`. Selection marks molecules for carry-forward /
   presentation; it does not delete the rest.
5. Emit the **ranking output** (below). The loop consumes **both ends** — top
   molecules as parents to elaborate, bottom molecules as negative signal —
   which is why the output is the full sorted list, not just the winners.

### Lineage

Every molecule carries provenance so the loop and the UI can trace *where it came
from*:

- `round` — the generation round that produced it (seeds = round 0).
- `parent_id` — the molecule it was derived from (`""`/null for de-novo seeds).

Lineage is written with the molecule record (persistence lives in
[`08-persistence-and-queue.md`](08-persistence-and-queue.md)); this stage
**reads** it to group pools by round and **stamps** it onto ranking rows so
[`06-generation-loop.md`](06-generation-loop.md) can surface "top + bottom by
fitness" with parentage, and the board in
[`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md) can draw lineage.

## Contracts

### `Scores` (Go)

The per-molecule scorecard. Raw scores kept beside the derived ones so nothing
needs re-docking to re-rank.

```go
// Scores is the selectivity scorecard for one molecule in a run.
// Vina affinities are negative kcal/mol; more negative = stronger binding.
type Scores struct {
    MoleculeID  string   `json:"molecule_id"`
    MutantScore float64  `json:"mutant_score"` // kcal/mol into MUTANT pocket (neg; lower = better mutant binding)
    WTScore     float64  `json:"wt_score"`     // kcal/mol into WT pocket    (neg; higher/nearer 0 = better)
    Selectivity float64  `json:"selectivity"`  // wt_score − mutant_score    (large positive = selective for mutant)
    Fitness     *float64 `json:"fitness"`      // composite, pool-normalised; null when status != "scored"
    Status      string   `json:"status"`       // "scored" | "incomplete"
}
```

### Fitness configuration

```go
// FitnessWeights are tunable and expected to sum to 1 (normalised on load).
type FitnessWeights struct {
    Potency      float64 `json:"potency"`       // w_p, default 0.35
    Selectivity  float64 `json:"selectivity"`   // w_s, default 0.45
    DrugLikeness float64 `json:"drug_likeness"` // w_q, default 0.20
}

// NormMode selects pool normalisation; PoolScope selects the pool.
type NormMode  string // "zscore" (default) | "minmax"
type PoolScope string // "round"  (default) | "run"
```

### Ranking output (Go)

```go
type Ranking struct {
    RunID   string           `json:"run_id"`
    Round   int              `json:"round"`         // the round scored; -1 = whole-run pool
    Weights FitnessWeights   `json:"weights"`
    Norm    NormMode         `json:"normalization"`
    Scope   PoolScope        `json:"pool_scope"`
    Ranked  []RankedMolecule `json:"ranked"`        // sorted by fitness desc; rank 1 = best
    Excluded []Scores        `json:"excluded"`      // status = "incomplete", not ranked
}

type RankedMolecule struct {
    Rank     int     `json:"rank"`           // 1-based
    Selected bool    `json:"selected"`       // in the carried-forward / final pool
    SMILES   string  `json:"smiles"`
    QED      float64 `json:"qed"`            // drug-likeness from 05
    Round    int     `json:"round"`          // lineage
    ParentID string  `json:"parent_id"`      // lineage; "" for seeds
    Scores   Scores  `json:"scores"`
}
```

### JSON shape (what the loop / board receive)

```json
{
  "run_id": "run_9f3a",
  "round": 2,
  "normalization": "zscore",
  "pool_scope": "round",
  "weights": { "potency": 0.35, "selectivity": 0.45, "drug_likeness": 0.20 },
  "ranked": [
    {
      "rank": 1,
      "selected": true,
      "smiles": "COc1ccc(CN2CCN(...)CC2)cc1",
      "qed": 0.74,
      "round": 2,
      "parent_id": "mol_0b12",
      "scores": {
        "molecule_id": "mol_1a44",
        "mutant_score": -9.2,
        "wt_score": -5.1,
        "selectivity": 4.1,
        "fitness": 1.83,
        "status": "scored"
      }
    }
  ],
  "excluded": [
    {
      "molecule_id": "mol_1c07",
      "mutant_score": -8.6,
      "wt_score": 0,
      "selectivity": 0,
      "fitness": null,
      "status": "incomplete"
    }
  ]
}
```

The `Scores` records are the persisted rows `(molecule_id, mutant_score,
wt_score, selectivity, fitness)`; `Ranking` is the computed, ordered view
returned to the loop and the frontend. HTTP surface (e.g.
`GET /run/:id/ranking?round=&scope=`) is owned by
[`08-persistence-and-queue.md`](08-persistence-and-queue.md); this spec fixes the
**shapes**, not the routes.

## Dependencies & touch points

**Inputs**

- [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md)
  — paired `{ wt_score, mutant_score, wt_pose, mutant_pose }` per molecule. This
  stage assumes both tracks are present; it does not dock.
- [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md) — `QED`
  drug-likeness per molecule (and the validity gate that keeps junk out of the
  pool).

**Outputs / consumers**

- [`06-generation-loop.md`](06-generation-loop.md) — reads the `Ranking` (top +
  bottom by fitness, with lineage) to pick parents and steer the next round.
- [`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md) — the
  selectivity board renders `Ranking`; this **replaces** `DockedResults.tsx` as
  the ranking surface.
- [`08-persistence-and-queue.md`](08-persistence-and-queue.md) — persists the
  molecule + lineage + `Scores` + poses so ranking survives restarts and can be
  re-weighted without re-docking.

**Code to change / retire**

- `services/jobs.go` — its single-affinity `DockingResult` is per-dock; scoring
  sits **above** it, joining the WT and mutant results for one molecule into one
  `Scores`. No change to the docking mechanics themselves here.
- `app/src/lib/api.ts` — add `Scores`, `RankedMolecule`, `Ranking` types
  (mirroring the Go structs) alongside the existing `DockingResult`.
- `app/src/components/viewer/DockedResults.tsx` — retired as the ranking
  authority; its single-affinity sort and `affinityTone` are superseded by
  fitness ordering. (New component specced in `09`.)

## Acceptance criteria

- [ ] Given a molecule's `wt_score` and `mutant_score`, `selectivity` computes
      as exactly `wt_score − mutant_score`; the worked examples above
      (`+4.1` selective, `−0.4` non-selective) reproduce.
- [ ] `fitness = w_p·ẑ(−mutant_score) + w_s·ẑ(selectivity) + w_q·ẑ(qed)` with
      weights summing to 1; changing weights re-orders the pool deterministically.
- [ ] Normalisation is computed **over the pool** (default: the round). A pool of
      one, or an all-equal term, yields a finite fitness (no NaN/Inf from
      `σ = 0` or a zero range).
- [ ] Both `zscore` and `minmax` modes produce identical **ordering** when a term
      is monotonic, and both are selectable.
- [ ] Ranking sorts by `fitness` desc with the specified tie-breakers; `rank` is
      1-based and dense.
- [ ] A molecule missing a WT or mutant score is `status:"incomplete"`, has
      `fitness:null`, is **excluded from the normalisation pool** and from
      `ranked`, and appears under `excluded`.
- [ ] Every ranked row carries `round` and `parent_id`; seeds have `parent_id`
      empty and `round = 0`.
- [ ] `Scores` rows persist `(molecule_id, mutant_score, wt_score, selectivity,
      fitness)` and can be re-ranked from storage without re-docking.
- [ ] The `Ranking` JSON validates against the shape above and is consumable by
      both the loop and the frontend board.

## Open questions / risks

- **Selectivity gaming.** Maximising `wt_score − mutant_score` alone rewards a
  molecule that binds *nothing* (both scores near 0, or WT positive). The potency
  weight `w_p` is the guardrail, but consider a hard floor (e.g. require
  `mutant_score ≤ −6` to be `selected`) rather than trusting the weighted sum.
- **Pool scope for feedback.** Round-scoped normalisation keeps each round's
  top/bottom stable but makes cross-round fitness incomparable; run-scoped fixes
  the final leaderboard but drifts as the pool grows. Default `round` for the
  loop, expose `run` for the final board — is that the right split?
- **Small-pool instability.** Early rounds may have too few molecules for a
  meaningful μ/σ (z-scores explode toward ±1 with `n=2`). Possibly fall back to
  `minmax`, or a fixed reference scale, below some pool size.
- **Vina noise vs. margin significance.** A `selectivity` of `+0.4` may be within
  docking noise. Should the board/loop treat a margin below some ε as "no
  selectivity", and should scoring average multiple Vina runs before ranking?
- **QED as the sole drug-likeness term.** QED rolls several properties into one
  `[0,1]` number; some campaigns care about specific violations (PAINS, MW,
  Lipinski) not captured by QED alone. Keep `w_q` low and revisit whether extra
  terms belong here or in the `05` gate.
- **Weight calibration.** The `0.45 / 0.35 / 0.20` split is a starting guess;
  weights should be run-configurable and, ideally, sanity-checked against a
  known selective vs. non-selective pair before trusting the ranking.
