# Stanza — Dual-Track Docking & Caching

Dock every candidate into **both** the mutant and WT pockets, keep the two scores
paired, and never re-dock a molecule we have already seen. **EXTEND** — the
single-pocket Vina path in `services/docking.go` + `services/jobs.go` exists; the
dual-track pairing and the idempotent cache do not.

---

## Goal

Produce, for each candidate molecule, a **paired** result:

```
{ wt_score, wt_pose_pdb, mutant_score, mutant_pose_pdb }
```

by docking the same ligand into the **WT pocket** and the **mutant pocket** with
identical box geometry. These two numbers are the raw material for the
**selectivity margin** (`wt_score − mutant_score`) computed downstream in
[`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md).

Docking is the pipeline bottleneck: the generation loop
([`06-generation-loop.md`](06-generation-loop.md)) runs `R` rounds, each
proposing up to `N` molecules, and every molecule now costs **two** Vina runs.
Naïvely that is `2 × N × R` docks per run. This feature keeps that number
tractable two ways:

1. **Cap `N` per round** and run docks through a bounded worker pool.
2. **Cache by SMILES** so a re-proposed molecule (a very common event once the
   loop starts exploiting a scaffold) is a store lookup, not a Vina run.

**Sign convention.** Vina reports binding affinity in **kcal/mol, always
negative**; more negative = stronger binding. This convention is preserved
end to end — no absolute values, no sign flips. Selectivity math and all
comparisons downstream assume it.

## Current state

The docking path today is **single-pocket, single-molecule, uncached**.

- **`services/docking.go`** — `SMILESTo3D` (`obabel --gen3d`),
  `PrepareReceptor` / `PrepareLigand` (`obabel` → `.pdbqt`, with post-strip of
  records Vina rejects), and `RunVinaDock`, which runs `vina` with the box
  **centered on `pocket.Center`**, **25 Å** cube, **exhaustiveness 16**,
  **`--cpu 4`**, then converts `docked.pdbqt` → `docked.pdb` for Mol\*.
  `parseVinaAffinity` reads **mode 1** (the best pose) from Vina's stdout table.
  `DockResult` carries one `BindingAffinity` + one pose.
- **`services/jobs.go`** — an in-memory `JobStore` (UUID keys, `sync.RWMutex`,
  cap **100** with FIFO eviction). `Submit` spawns one goroutine per job;
  `runJob` does the full 3D → prep → Vina → read-pose sequence for **one SMILES
  into one pocket**. `DockingResult{ JobID, PocketID, Status, BindingAffinity,
  PosePDB, Conformations, Error }` is the reported shape.
- **`handlers/dock_handler.go`** — `POST /dock` takes one `pocket_id` +
  `source_type` (`"monomer"`/`"dimer"` today) + `ligand_smiles`, looks the
  pocket up in `DefaultPocketStore`, and submits one job.

Two gaps this spec closes:

- **No pairing.** A job targets exactly one pocket. There is no notion of a
  WT/mutant pair, so `wt_score` and `mutant_score` cannot both exist for one
  molecule from one request.
- **No caching.** A re-proposed SMILES re-runs the whole obabel + Vina
  pipeline. Nothing is keyed on molecule identity, so the loop pays full price
  for every duplicate.

Everything Vina-level (`RunVinaDock`, the prep/strip helpers, `parseVinaAffinity`)
is reused **unchanged** — this feature wraps it, it does not rewrite it.

## Design

### Dual-track dock (the pairing)

The WT and mutant pockets are supplied by
[`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md):
the same molecule is docked into each. A **dual dock** is the unit of work here —
one SMILES, two pockets, one paired result.

A single dual dock, for one candidate, is:

1. Generate the ligand 3D + PDBQT **once** (`SMILESTo3D` + `PrepareLigand`) — the
   ligand is pocket-independent, so it is prepared a single time and reused for
   both tracks.
2. For each track ∈ {WT, mutant}:
   - Resolve the receptor PDBQT for that track's structure (`PrepareReceptor`,
     memoizable per structure — the receptor does not change between candidates).
   - **Cache lookup** on `(smiles_hash, pocket_id, params_hash)`. On a hit,
     take the stored `score` + `pose_pdb` and skip Vina entirely.
   - On a miss, `RunVinaDock` with the track's `pocket.Center` and the standard
     box (25 Å, exhaustiveness 16, best mode → affinity + pose), then **write the
     result back** into the cache before returning.
3. Assemble the paired result `{ wt_score, wt_pose_pdb, mutant_score,
   mutant_pose_pdb }`.

The two tracks are **independent** and run **concurrently** within a dual dock —
they share only the prepared ligand. Each side keeps the existing behaviour:
box centered on that pocket's `Center`, 25 Å cube, exhaustiveness 16, mode 1 →
affinity + pose PDB. Nothing about Vina's invocation changes.

Either track can be a cache hit while the other is a miss; a dual dock reports
its result the same way regardless.

### Cache & idempotency

Two related but distinct ideas:

- **Idempotent jobs** — a submitted dual-dock **job** is keyed by
  `(smiles_hash, pocket_pair, params_hash)`. Re-submitting an identical job
  returns the existing job/result instead of starting new work. This dedupes at
  the *request* level (e.g. the loop re-proposes the same molecule in the same
  run).
- **Dock cache** — an individual **dock** (one SMILES into one pocket) is keyed
  by `smiles_hash + pocket_id + params_hash`. A hit **skips Vina entirely** and
  returns the stored `score` + `pose_pdb`. This dedupes at the *compute* level,
  and — crucially — is shared **across tracks, jobs, rounds, and (later) runs**:
  a molecule docked into the mutant pocket in round 2 is free if it was already
  docked there in round 1.

**Canonicalisation.** The cache key must be **molecule identity**, not raw
string. The SMILES is canonicalised first (the same normalization RDKit performs
in [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md)) so that
two spellings of the same molecule collide in the cache. `smiles_hash =
hash(canonical_smiles)`.

**Params hash.** `params_hash = hash(box_size, center, exhaustiveness,
[vina_version])`. Including the docking parameters means a cached result is only
reused when it was produced under **identical geometry and settings** — change
the box to 30 Å or bump exhaustiveness and the cache correctly misses rather
than serving a stale, incomparable score. `pocket_id` is included separately so
the same molecule's WT and mutant docks never alias to one entry.

> The center is part of `params_hash`, and each pocket has its own center, so
> `pocket_id` + `params_hash` together fully pin the box. `pocket_id` is kept as
> an explicit key component for readability and cheap indexing.

**Store backends.** The cache is an **interface**, not a concrete store. Today
it is backed by an **in-memory map** (mirroring the current `JobStore`). Per
[`08-persistence-and-queue.md`](08-persistence-and-queue.md) it is later backed
by a **Postgres `docks` table** (durable, survives restarts, shared across runs)
fronted by **Redis** (hot lookups). Swapping backends must not touch the
docking or job code — only the interface implementation.

### Budget & parallelism

- **Cap `N` per round.** The generation loop caps how many *new* molecules enter
  a round; this feature enforces a hard ceiling on **concurrent Vina processes**
  so `2 × N` docks do not oversubscribe the box. Each Vina run already uses
  `--cpu 4`, so the worker-pool size is `min(N-cap, floor(cores / 4))`-ish, not
  unbounded goroutines.
- **Worker pool.** Replace the current "one goroutine per job, no limit" model
  with a **bounded pool** of dock workers pulling from a queue. A dual dock
  enqueues up to two dock tasks (fewer on cache hits). This is the seam that
  [`08-persistence-and-queue.md`](08-persistence-and-queue.md) later replaces
  with a real queue + separate worker processes; the interface should not assume
  in-process goroutines.
- **Cache-first.** Every dock task checks the cache before it ever touches the
  worker pool, so duplicates never consume a worker slot. In a converged loop
  the effective dock count is **far below** `2 × N × R`.
- **Fail-soft per track.** If one track errors (bad geometry, Vina failure), the
  other still records its score; the paired result marks the failed side rather
  than discarding the whole candidate. Downstream ranking decides how to treat a
  half-scored molecule.

## Contracts

> Illustrative Go — names and shapes to implement, not final signatures.

### Dual-dock request & result

```go
// DualDockRequest is one candidate to dock into both tracks' pockets.
type DualDockRequest struct {
    RunID        string        // owning run (threaded from 01-run-lifecycle)
    SMILES       string        // raw; canonicalised before hashing
    WTPocket     models.Pocket // WT track pocket  (from 03-dual-pocket-*)
    MutantPocket models.Pocket // mutant track pocket
    WTPDBPath    string        // WT receptor structure (path or URL)
    MutantPDBPath string       // mutant receptor structure
    Params       DockParams    // box/exhaustiveness — feeds params_hash
}

// DockParams are the Vina settings that make a score comparable/cacheable.
type DockParams struct {
    BoxSize        float64 // 25.0 today
    Exhaustiveness int     // 16 today
    // Center is per-pocket (pocket.Center); folded into params_hash per track.
}

// DualDockResult is the paired output — the unit 07-selectivity consumes.
type DualDockResult struct {
    JobID          string  `json:"job_id"`
    Status         string  `json:"status"` // pending|running|done|error|partial
    CanonicalSMILES string `json:"canonical_smiles"`

    WTScore        float64 `json:"wt_score"`        // kcal/mol, negative
    WTPosePDB      string  `json:"wt_pose_pdb"`     // Mol*-ready
    WTFromCache    bool    `json:"wt_from_cache"`

    MutantScore    float64 `json:"mutant_score"`    // kcal/mol, negative
    MutantPosePDB  string  `json:"mutant_pose_pdb"`
    MutantFromCache bool   `json:"mutant_from_cache"`

    // SelectivityMargin is NOT computed here — see 07. Left to the scorer so
    // this layer stays a pure docking/caching concern.
    Error          string  `json:"error,omitempty"`
}
```

`Status = "partial"` when exactly one track produced a score (fail-soft).

### Cache key & entry

```go
// DockCacheKey uniquely identifies one dock (one molecule, one pocket, one geometry).
type DockCacheKey struct {
    SMILESHash string // hash(canonical_smiles)
    PocketID   int    // pocket.PocketID (WT or mutant)
    ParamsHash string // hash(box_size, center, exhaustiveness[, vina_version])
}

func (k DockCacheKey) String() string // stable "smiles:pocket:params" for map / Redis key

// DockCacheEntry is one cached dock result.
type DockCacheEntry struct {
    Score   float64 // kcal/mol, negative
    PosePDB string
    // provenance for the later Postgres `docks` table: created_at, vina_version…
}
```

### Cache-store interface

```go
// DockCache abstracts the dock result store. In-memory now; Postgres `docks`
// table + Redis later (08) — implementations swap without touching dock code.
type DockCache interface {
    Get(key DockCacheKey) (DockCacheEntry, bool)
    Put(key DockCacheKey, entry DockCacheEntry) error
}
```

### Docking functions

```go
// DualDock runs (or serves from cache) both tracks for one candidate. The
// per-track dock is cache-first: Get → on miss RunVinaDock → Put. Reuses the
// existing SMILESTo3D / PrepareReceptor / PrepareLigand / RunVinaDock verbatim.
func DualDock(ctx context.Context, req DualDockRequest, cache DockCache) (DualDockResult, error)

// canonicalSMILES normalises a SMILES to molecule identity before hashing
// (shared with 05-molecule-validation).
func canonicalSMILES(smiles string) (string, error)

func dockParamsHash(p DockParams, center [3]float64) string
func smilesHash(canonical string) string
```

### Job store (extend)

`JobStore` (or its successor) tracks `DualDockResult` keyed by job UUID **and**
by the idempotency key `(smiles_hash, pocket_pair, params_hash)` so a duplicate
submission returns the in-flight/finished job rather than starting new work.
`RunVinaDock` and the prep/strip helpers in `services/docking.go` are **not**
modified.

## Dependencies & touch points

- **Reuses (unchanged):** `services/docking.go` — `RunVinaDock`, `SMILESTo3D`,
  `PrepareReceptor`, `PrepareLigand`, `parseVinaAffinity`, and all PDBQT-strip
  helpers. `models.Pocket.Center` is still the box center.
- **Extends:** `services/jobs.go` — `JobStore` gains dual-track results, an
  idempotency index, and a bounded worker pool in place of unbounded per-job
  goroutines.
- **`handlers/dock_handler.go`** — a dual-dock submit path that takes a WT +
  mutant pocket pair (per run) instead of a single `pocket_id` +
  `source_type`; status returns the paired `DualDockResult`.
- **Upstream inputs:**
  [`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md)
  supplies the WT & mutant pockets;
  [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md) supplies
  the canonical SMILES used for the cache key;
  [`06-generation-loop.md`](06-generation-loop.md) drives candidates in and sets
  the per-round cap `N`.
- **Downstream consumer:**
  [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md)
  reads `{ wt_score, mutant_score }` and computes the margin + ranking.
- **Later backing:**
  [`08-persistence-and-queue.md`](08-persistence-and-queue.md) provides the
  Postgres `docks` table + Redis for the `DockCache` interface and the real job
  queue/workers that replace the in-process pool.
- **Run context:** `RunID` threads through from
  [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md).

## Acceptance criteria

- One candidate SMILES produces a single **paired** result
  `{ wt_score, wt_pose_pdb, mutant_score, mutant_pose_pdb }`, both scores in
  **negative kcal/mol**, both poses Mol\*-renderable.
- Each track's score/pose is byte-for-byte what the existing single-pocket path
  produces for the same pocket + box (25 Å, exhaustiveness 16, mode 1) — the
  wrapper does not change Vina behaviour.
- **Cache correctness:** docking the same canonical SMILES into the same pocket
  with the same params a second time returns the stored result and **runs no
  Vina process** (observable: `*_from_cache = true`, no new `vina` invocation).
- **Canonical collision:** two different SMILES spellings of the same molecule
  hit the same cache entry.
- **Params sensitivity:** changing box size, center/pocket, or exhaustiveness
  produces a cache **miss** (no stale reuse across incomparable geometry).
- **Idempotent submit:** submitting an identical dual-dock job twice does not
  start a second compute; the second returns the first job's result.
- **Budget respected:** no more than the configured number of concurrent Vina
  processes run at once, regardless of `N`; duplicates never occupy a worker.
- **Fail-soft:** one track failing yields `status = "partial"` with the other
  track's score intact, not a total failure.
- **Backend swap:** replacing the in-memory `DockCache` with another
  implementation requires no change to `DualDock` or the job store.

## Open questions / risks

- **Cross-structure receptor identity.** The cache keys on `pocket_id`, but the
  WT and mutant receptors are different structures. `pocket_id` must be unique
  per (structure, track) — or the key must include a structure/track
  discriminator — so a WT pocket and a mutant pocket can never collide. Confirm
  with [`03`](03-dual-pocket-analysis-and-delta.md)'s pocket ID scheme.
- **Vina non-determinism.** Vina uses a random seed; two runs of the same input
  can differ slightly. Caching pins the *first* result, which is what we want for
  comparability — but note that a cached score and a hypothetical re-dock are not
  guaranteed identical. Consider pinning `--seed` for reproducibility, and fold
  `vina_version` into `params_hash` so an engine upgrade invalidates the cache.
- **Pose storage size.** `pose_pdb` strings are large; an in-memory cache of many
  molecules × 2 tracks can grow fast. Decide an eviction/size policy for the
  in-memory backend (the current `JobStore` cap is 100) before
  [`08`](08-persistence-and-queue.md) moves poses to durable storage.
- **Receptor prep memoization.** `PrepareReceptor` is currently per-job; for
  dual-track it should be memoized per structure so N candidates don't re-prep
  the same two receptors. Where does that cache live (separate from `DockCache`)?
- **Ligand prep determinism.** `SMILESTo3D`/`--gen3d` conformer generation may
  vary run to run; since the cache short-circuits before ligand prep on a hit,
  this only matters for cache misses, but worth noting for reproducibility.
- **Partial results downstream.** How should [`07`](07-selectivity-scoring-and-ranking.md)
  rank a `"partial"` candidate with only one score? Defer the policy there, but
  flag the shape here.
- **Box adequacy per track.** A mutation can shift a pocket's geometry; a 25 Å
  box centered on each pocket's own `Center` should still cover both, but verify
  the mutant pocket isn't clipped when its `Center` drifts.
