# Stanza — Persistence, Job Queue & Workers

The durable substrate under the whole pipeline: a **Postgres** system of record for
runs/structures/pockets/molecules/docks/scores, a **Redis-backed job queue** that keeps
slow compute off the request path, and a **bounded pool of Python workers** that consume
jobs and write results back. Status: **BUILD** (new — everything is in memory today).

See [`00-overview.md`](00-overview.md) for product framing and build order. This spec is
the infrastructure that stages [`01`](01-run-lifecycle-and-mutation.md)–[`07`](07-selectivity-scoring-and-ranking.md)
persist into and dispatch onto; they are its consumers.

---

## Goal

Replace the process-local, volatile state with durable, restartable infrastructure so a
run survives a crash, heavy compute runs off the HTTP request path, and repeated work is
served from cache instead of recomputed. Concretely:

1. A **Postgres schema** — six tables (`runs`, `structures`, `pockets`, `molecules`,
   `docks`, `scores`) with keys, indexes, and a **UNIQUE constraint that enforces the
   docks cache key**.
2. **Migration** off the two in-memory stores: `services/jobs.go` (`JobStore`) and
   `handlers/pocket_store.go` (`PocketStore`).
3. A **Redis-backed job queue** — one queue per job type
   (`mutagenesis` / `fpocket` / `dock` / `validate`), a job envelope
   `{ type, payload, idempotency_key }`, a worker contract, and result write-back to
   Postgres.
4. A **bounded worker pool** of Python processes consuming those queues; the **queue is
   the only Go↔Python interface** — no in-process FFI, no HTTP between them.
5. **Backpressure + idempotency**: a fixed-size pool bounds concurrency; the round loop
   blocks until that round's docks drain, then scores as a batch; jobs are keyed by
   `(smiles_hash, pocket_id, params_hash)` so redelivery and re-entered rounds hit cache
   instead of recomputing.

The **orchestration** (round loop, continue/stop decision) stays in Go; only the heavy
compute (mutagenesis / fpocket / Vina / RDKit) moves behind the queue. The run **state
machine** and stage semantics are owned by [`01`](01-run-lifecycle-and-mutation.md); this
spec owns *where that state lives* and *how stages are dispatched*.

---

## Current state

Everything is in memory. Two stores hold all mutable state, and Go shells out to CLIs
inline on a goroutine per docking job.

| File | Today | What replaces it |
|---|---|---|
| [`services/jobs.go`](../../services/jobs.go) | `JobStore`: a `map[jobID]DockingResult` guarded by a `sync.RWMutex`, capped at `maxJobs = 100` with FIFO eviction. `Submit` spawns **one goroutine per job** (`runJob`) that shells out to `obabel`/`vina` inline and mutates the map. | Enqueue a `dock` job to Redis; read status/result from the `docks` table. No goroutine, no cap, no eviction. |
| [`handlers/pocket_store.go`](../../handlers/pocket_store.go) | `PocketStore`: a process-wide `map` keyed `"sourceType:pocketID"` (e.g. `"monomer:3"`, `"dimer:1"`). Lost on restart. | The `pockets` table, keyed by a UUID and scoped by `structure_id`. |
| [`models/pocket.go`](../../models/pocket.go) | `Pocket` struct (residues, center, druggability, fragments). | Serialized into `pockets` rows; residue arrays → `key_residues jsonb`, `Center` → the docking-box center. |
| [`models/complex.go`](../../models/complex.go) | `Complex` target metadata, fetched fresh per request. | Not persisted here; a run references a target by `uniprot_id` only (see [`01`](01-run-lifecycle-and-mutation.md)). |

Facts that shape the design:

- Module path is `github.com/ayush00git/stanza`; the **only** external deps today are
  `gin-gonic/gin` and `google/uuid`. This feature adds a Postgres driver and a Redis
  client to `go.mod`, plus a Python worker runtime that is deployed separately.
- Go currently does the CLI shelling (`fpocket`, `obabel`, `vina`) itself. That work
  moves **worker-side**; the Go binary stops needing those CLIs on its host.
- The old store keys encode the pre-existing **monomer/dimer** axis. The product axis is
  **WT/mutant** ([`00`](00-overview.md)); pocket rows are scoped by `structure_id`, and a
  structure carries the `wt`/`mutant` discriminator. Do not carry the monomer/dimer key
  format into the new schema.

---

## Design

### System of record — Postgres

Six tables, one FK spine rooted at `runs`. The WT/mutant split lives on `structures.kind`
(this is spec [`01`](01-run-lifecycle-and-mutation.md)'s **`track`** discriminator; the
column is named `kind` to match the data-model sketch). Pockets inherit the track
transitively through their `structure_id`.

```
runs ─┬─▶ structures ──▶ pockets ─┐
      │                           ├─▶ docks   (molecule × pocket)
      └─▶ molecules ──────────────┘
                └────────────────────▶ scores (one per molecule)
```

- **`runs`** — the thin header (id, target, mutation, status). Also carries the durable
  loop checkpoint (`round`, `round_state`) so a restart resumes at the last completed
  stage boundary rather than round 0. This is where [`01`](01-run-lifecycle-and-mutation.md)'s
  run state machine is persisted.
- **`structures`** — exactly two rows per run (`kind = wt | mutant`), each with a
  `source` (`alphafold | pdb | refold`) and a `path` to the coordinate file on disk /
  object storage. A `UNIQUE (run_id, kind)` enforces the "one WT + one mutant" invariant.
- **`pockets`** — belong to a structure; `key_residues` and `delta` are `jsonb`
  (residue lists and the WT↔mutant delta payload from [`03`](03-dual-pocket-analysis-and-delta.md)
  are variable-shape). `center` is the docking-box center reused by [`04`](04-dual-track-docking-and-caching.md).
- **`molecules`** — belong to a run, tagged with the `round` that produced them and a
  self-referential `parent_id` for lineage across rounds ([`06`](06-generation-loop.md)).
  `smiles_hash` is the **canonical**-SMILES hash and is the first component of the dock
  cache key. Drug-likeness columns (`qed`, `ro5_pass`, `sa_score`) are filled by the
  `validate` worker ([`05`](05-molecule-validation-rdkit.md)).
- **`docks`** — one row per (chemistry, pocket, params). `pose_path` points at the pose
  file (poses are large; the row stays small). The cache key is enforced by a **UNIQUE
  index** — see below.
- **`scores`** — one row per molecule: `mutant_score`, `wt_score`, `selectivity`
  (`wt_score − mutant_score`), and the ranking `fitness` ([`07`](07-selectivity-scoring-and-ranking.md)).

**The docks cache key.** The data-model shorthand is "smiles_hash + pocket", but the
idempotency key is the full triple `(smiles_hash, pocket_id, params_hash)`:

- `smiles_hash` — hash of the RDKit-canonical SMILES, so two `molecules` rows with the
  same chemistry (e.g. a molecule re-proposed in a later round) share one dock.
- `pocket_id` — the specific pocket geometry (WT or mutant, this run).
- `params_hash` — a hash of the docking parameters that change the result: box center +
  size, exhaustiveness, and the docking-engine version. Bump the engine → new key → no
  stale hit.

`UNIQUE (smiles_hash, pocket_id, params_hash)` makes the table itself the cache: a
`SELECT` on the triple is a cache lookup, and an `INSERT ... ON CONFLICT DO NOTHING`
makes redelivered/duplicate jobs write-back-idempotent.

### Job queue — Redis

Redis holds the **job queue** and the **live loop coordination**; Postgres holds the
**authoritative** results and checkpoint. If Redis is lost, coordination is rebuilt from
Postgres (any `docks` row still `pending`/`running` is re-enqueued).

- **One queue per job type**: `queue:mutagenesis`, `queue:fpocket`, `queue:dock`,
  `queue:validate`. Separate queues let dock (the slow, CPU-bound one) be drained by its
  own bounded pool without head-of-line blocking the cheap jobs.
- **Reliable delivery via a stream with consumer groups** (generic reliable-queue
  pattern): a worker reads-and-claims an entry, processes it, then **acks** it. Entries
  that a dead worker never acked sit in the pending list and are re-claimed after an idle
  timeout by a reclaimer. This gives **at-least-once** delivery; idempotent write-back
  (the UNIQUE cache key) makes it **effectively once**.
- **Enqueue-time dedupe**: `SET idem:<idempotency_key> NX EX <ttl>` before pushing. If
  the key already exists, the job is already queued or in flight — skip the push. This
  stops two concurrent rounds from queuing the same dock twice before its `docks` row
  exists.
- **Loop coordination keys** (ephemeral, rebuildable): a per-round **barrier counter**
  `run:<id>:round:<n>:pending` that Go sets to the number of dock jobs it enqueued and
  workers decrement (or that Go polls against `docks` completion counts). When it reaches
  zero, that round's docks have drained and Go proceeds to batch scoring.

### Worker pool — bounded, Python

A fixed set of Python worker processes (`WORKER_CONCURRENCY`, default = available cores ÷
Vina threads-per-job) each pull **one job at a time** from their queue, run the compute,
write back to Postgres, and ack. The fixed pool size **is** the backpressure: the queue
absorbs bursts, but concurrency never exceeds the pool, so CPU-bound Vina never
oversubscribes the host.

Workers own the heavy dependencies that should not live in the Go binary:

| Job type | Worker does | Writes back to | Consumer spec |
|---|---|---|---|
| `mutagenesis` | Apply the point mutation to the WT structure (e.g. PDBFixer/Modeller) | `structures` (`kind = mutant`) | [`02`](02-mutagenesis.md) |
| `fpocket` | Run `fpocket`, filter pockets, compute WT↔mutant delta | `pockets` | [`03`](03-dual-pocket-analysis-and-delta.md) |
| `dock` | Ligand 3D prep + receptor prep + Vina (the work `services/jobs.go` does inline today) | `docks` | [`04`](04-dual-track-docking-and-caching.md) |
| `validate` | RDKit canonicalize + QED / Lipinski / SA score | `molecules` (`smiles_hash`, `qed`, `ro5_pass`, `sa_score`) | [`05`](05-molecule-validation-rdkit.md) |

### The Go↔Python boundary — the queue is the interface

Go never links Python and never calls a worker over HTTP. The contract between them is
exactly **(a) the job envelope schema** and **(b) the Postgres tables**:

```
   Go (orchestrator)                 Redis                 Python (workers)
   ─────────────────                 ─────                 ────────────────
   round loop decides   ── enqueue ▶ queue:<type> ── read ▶  compute (RDKit/
   what to compute                   (envelope)             fpocket/Vina)
        ▲                                                        │
        │  read results  ◀───────── Postgres ◀── write-back ─────┘
        └── continue / stop            (docks, scores, …)
```

- **Go** resolves targets, drives the round state machine, builds cache keys, enqueues
  jobs, waits on the round barrier, reads results, and decides continue/stop.
- **Python** does nothing but consume a job, compute, and write one row (or set of rows).
  It is stateless between jobs and horizontally scalable.
- Because the interface is a queue + a schema, workers can be added in any language and
  the Go binary stays lean (no RDKit, no fpocket/Vina on its host).

### Backpressure & idempotency

The round loop is deliberately **single-threaded per run** and drains one round's docks
before scoring:

```
enqueue round n docks ──▶ workers drain at pool capacity ──▶ docks rows land
        │                                                          │
        └── set barrier = N            Go blocks on barrier ◀──────┘
                                              │ (barrier → 0)
                                              ▼
                                    batch-score round n  ──▶ decide continue/stop
```

- **Backpressure** = fixed pool + the round barrier. Enqueue always succeeds (the queue
  is the buffer), but Go will not advance the round until every dock is `done`/`error`,
  so an overloaded worker pool slows the run instead of exploding memory or CPU.
- **Idempotency, three layers**:
  1. **Enqueue** — `SET idem:<key> NX` dedupes concurrent enqueues.
  2. **Cache hit** — before enqueuing a dock, `SELECT` the `docks` cache triple; a `done`
     row is reused directly (this is [`04`](04-dual-track-docking-and-caching.md)'s
     caching contract, stored here). A re-entered round after a crash re-hits cache
     instead of re-docking.
  3. **Write-back** — `INSERT ... ON CONFLICT (smiles_hash, pocket_id, params_hash) DO
     NOTHING/UPDATE`, so a redelivered job cannot create a duplicate or corrupt a result.

**Ordering dependency (canonicalization).** `smiles_hash` must be the hash of the
*canonical* SMILES, which only the RDKit worker can produce. So the loop enqueues
`validate` first; the worker writes `molecules.smiles_hash`; only then does Go build the
dock cache key and enqueue `dock`. Generation → validate → dock ordering guarantees the
key exists before it is needed.

---

## Contracts

### Postgres DDL

Requires Postgres 13+ for built-in `gen_random_uuid()` (else `CREATE EXTENSION pgcrypto`).

```sql
-- enums --------------------------------------------------------------------
CREATE TYPE run_status       AS ENUM ('draft','validated','running','done','failed');
CREATE TYPE structure_kind   AS ENUM ('wt','mutant');       -- spec 01's `track`
CREATE TYPE structure_source AS ENUM ('alphafold','pdb','refold');
CREATE TYPE job_status       AS ENUM ('pending','running','done','error');

-- runs ---------------------------------------------------------------------
CREATE TABLE runs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    uniprot_id  TEXT        NOT NULL,
    mutation    TEXT        NOT NULL,                 -- canonical, e.g. 'T790M'
    status      run_status  NOT NULL DEFAULT 'draft',
    round       INTEGER     NOT NULL DEFAULT 0,       -- loop checkpoint (01)
    round_state TEXT,                                 -- per-round stage while running
    site_hint   TEXT,
    error       TEXT,                                 -- set when status = 'failed'
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runs_status     ON runs (status);
CREATE INDEX idx_runs_created_at ON runs (created_at DESC);   -- list newest-first

-- structures ---------------------------------------------------------------
CREATE TABLE structures (
    id         UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id     UUID             NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    kind       structure_kind   NOT NULL,             -- wt | mutant
    source     structure_source NOT NULL,             -- alphafold | pdb | refold
    path       TEXT             NOT NULL,             -- coord file (disk / object store)
    created_at TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_structures_run_kind ON structures (run_id, kind);  -- one WT + one mutant

-- pockets ------------------------------------------------------------------
CREATE TABLE pockets (
    id           UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    structure_id UUID             NOT NULL REFERENCES structures(id) ON DELETE CASCADE,
    key_residues JSONB            NOT NULL,            -- lining residues (idx/name/chain)
    volume       DOUBLE PRECISION,
    druggability DOUBLE PRECISION,
    center       DOUBLE PRECISION[3],                 -- docking-box center (04)
    delta        JSONB,                               -- WT↔mutant delta payload (03)
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE INDEX idx_pockets_structure ON pockets (structure_id);

-- molecules ----------------------------------------------------------------
CREATE TABLE molecules (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id      UUID        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    round       INTEGER     NOT NULL,
    parent_id   UUID        REFERENCES molecules(id) ON DELETE SET NULL,  -- lineage (06)
    smiles      TEXT        NOT NULL,
    smiles_hash TEXT        NOT NULL,                 -- hash of canonical SMILES (cache key)
    qed         DOUBLE PRECISION,                     -- filled by validate worker (05)
    ro5_pass    BOOLEAN,
    sa_score    DOUBLE PRECISION,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_molecules_run_round    ON molecules (run_id, round);
CREATE INDEX idx_molecules_smiles_hash  ON molecules (smiles_hash);

-- docks --------------------------------------------------------------------
CREATE TABLE docks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    molecule_id UUID        NOT NULL REFERENCES molecules(id) ON DELETE CASCADE,
    pocket_id   UUID        NOT NULL REFERENCES pockets(id)   ON DELETE CASCADE,
    smiles_hash TEXT        NOT NULL,                 -- denormalized cache-key component
    params_hash TEXT        NOT NULL,                 -- box + exhaustiveness + engine ver
    score       DOUBLE PRECISION,                     -- best affinity (kcal/mol)
    pose_path   TEXT,                                 -- path to docked pose file
    status      job_status  NOT NULL DEFAULT 'pending',
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- THE cache key: one dock result per (chemistry, pocket, params).
CREATE UNIQUE INDEX uq_docks_cache ON docks (smiles_hash, pocket_id, params_hash);
CREATE INDEX idx_docks_molecule ON docks (molecule_id);

-- scores -------------------------------------------------------------------
CREATE TABLE scores (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    molecule_id  UUID        NOT NULL REFERENCES molecules(id) ON DELETE CASCADE,
    mutant_score DOUBLE PRECISION,
    wt_score     DOUBLE PRECISION,
    selectivity  DOUBLE PRECISION,                    -- wt_score - mutant_score
    fitness      DOUBLE PRECISION,                    -- ranking objective (07)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_scores_molecule ON scores (molecule_id);        -- one score / molecule
CREATE INDEX        idx_scores_fitness ON scores (fitness DESC);       -- leaderboard order
```

### Job envelope

The core is the required triple `{ type, payload, idempotency_key }`; envelope metadata
(`run_id`, `attempt`, `enqueued_at`) rides alongside for tracing and dead-lettering.

```json
{
  "type": "dock",
  "idempotency_key": "sha256(<smiles_hash>|<pocket_id>|<params_hash>)",
  "run_id": "b1f0…",
  "attempt": 1,
  "enqueued_at": "2026-07-08T10:00:00Z",
  "payload": {
    "molecule_id": "…",
    "pocket_id":   "…",
    "smiles":      "CC(=O)Oc1ccccc1C(=O)O",
    "smiles_hash": "…",
    "params_hash": "…",
    "box":  { "center": [12.3, 4.5, -8.1], "size": [20, 20, 20] },
    "exhaustiveness": 8
  }
}
```

Payload by type (idempotency key in parentheses):

| Type | Payload keys | Idempotency key |
|---|---|---|
| `mutagenesis` | `run_id`, `wt_structure_id`, `mutation{wt,pos,mut}` | `hash(run_id, mutation)` |
| `fpocket` | `run_id`, `structure_id` | `hash(structure_id, params_hash)` |
| `dock` | `molecule_id`, `pocket_id`, `smiles`, `smiles_hash`, `params_hash`, `box`, `exhaustiveness` | `hash(smiles_hash, pocket_id, params_hash)` |
| `validate` | `run_id`, `molecule_id`, `smiles` | `hash(molecule_id)` |

### Worker contract

Every worker follows the same loop; only the compute step differs.

```python
# workers/<type>.py — the generic worker loop (pseudocode)
def run(queue_name, group, consumer):
    while True:
        entry = read_and_claim(queue_name, group, consumer)   # blocking read
        job   = decode(entry)                                 # {type,payload,idempotency_key,…}

        # 1. idempotency / cache guard (dock shown; other types check their own rows)
        if job.type == "dock" and dock_exists_done(job.smiles_hash,
                                                    job.pocket_id, job.params_hash):
            ack(entry); continue                              # cache hit — nothing to do

        try:
            result = compute(job)                             # RDKit / fpocket / Vina
            write_back(job, result)                           # INSERT ... ON CONFLICT
            ack(entry)                                        # at-least-once → done
        except Exception as e:
            if job.attempt >= MAX_ATTEMPTS:
                dead_letter(entry, e)                         # poison job → DLQ
                mark_error(job, e); ack(entry)
            # else: do NOT ack → reclaimer redelivers after idle timeout
```

Write-back is a single transaction that upserts the result row and, where relevant,
advances status:

```sql
-- dock write-back: idempotent on the cache key
INSERT INTO docks (id, molecule_id, pocket_id, smiles_hash, params_hash,
                   score, pose_path, status, updated_at)
VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, 'done', now())
ON CONFLICT (smiles_hash, pocket_id, params_hash)
DO UPDATE SET score = EXCLUDED.score, pose_path = EXCLUDED.pose_path,
              status = 'done', updated_at = now();
```

### Go-side interfaces (replace the in-memory stores)

```go
// queue/queue.go (new) — the only thing Go says to workers.
type Enqueuer interface {
    // Enqueue pushes an envelope after an NX idempotency guard; returns whether
    // it was newly enqueued (false = already queued/in-flight, deduped).
    Enqueue(ctx context.Context, env Envelope) (enqueued bool, err error)
}

// store/docks.go (new) — replaces services/jobs.go JobStore.
type DockStore interface {
    Get(ctx context.Context, id string) (Dock, bool, error)          // was JobStore.Get
    Lookup(ctx context.Context, smilesHash, pocketID, paramsHash string) (Dock, bool, error) // cache hit
    CountRemaining(ctx context.Context, runID string, round int) (int, error) // round barrier
}

// store/pockets.go (new) — replaces handlers/pocket_store.go PocketStore.
type PocketStore interface {
    Insert(ctx context.Context, p Pocket) (id string, err error)     // was Put
    ByStructure(ctx context.Context, structureID string) ([]Pocket, error)
    Get(ctx context.Context, id string) (Pocket, bool, error)
}
```

Migration mapping (behavior preserved where the frontend depends on it):

| Old (in-memory) | New (durable) |
|---|---|
| `JobStore.Submit(...)` spawns a goroutine, returns `jobID` | Insert a `pending` `docks` row + `Enqueue` a `dock` job; return the dock `id` as the job id |
| `JobStore.Get(jobID)` reads the map | `DockStore.Get(id)`; project a `docks` row into the existing `DockingResult` JSON shape so `/dock/status` is unchanged |
| `JobStore` `maxJobs`/eviction | dropped — Postgres is the durable store; no cap |
| `runJob` shells out to `obabel`/`vina` inline | moved into the Python `dock` worker |
| `PocketStore.Put` / `RegisterBindingSitesResult` | `PocketStore.Insert` per pocket into Postgres |
| `PocketStore.Get("monomer:3")` composite key | `PocketStore.Get(uuid)` / `ByStructure(structureID)` (WT/mutant scoping via the structure) |

---

## Dependencies & touch points

| Sibling spec | Relationship |
|---|---|
| [`00-overview.md`](00-overview.md) | Product framing; this is gap #4 ("no durable state") and the "only once the loop works end to end" build step. |
| [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md) | Owns the run state machine; this spec persists `runs.{status,round,round_state}` for crash-resume and provides the loop's dispatch substrate. |
| [`02-mutagenesis.md`](02-mutagenesis.md) | Consumer of the `mutagenesis` job type; writes `structures (kind = mutant)`. |
| [`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md) | Consumer of the `fpocket` job type; writes `pockets` incl. `delta jsonb`. |
| [`06-generation-loop.md`](06-generation-loop.md) | Drives the loop that enqueues jobs and waits on the round barrier; owns the continue/stop decision; writes `molecules` (round + `parent_id` lineage). |
| [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md) | Consumer of the `validate` job type; writes `molecules.{smiles_hash,qed,ro5_pass,sa_score}` — and is the step that produces the canonical `smiles_hash` the dock cache key needs. |
| [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md) | Consumer of the `dock` job type; the `docks` table + `UNIQUE (smiles_hash, pocket_id, params_hash)` **is** its caching contract's storage. |
| [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md) | Batch scoring after a round's docks drain; writes `scores.{mutant_score,wt_score,selectivity,fitness}`. |
| [`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md) | Reads `runs`/`molecules`/`scores` for the leaderboard and round/job status. |

**Code touch points (this feature):**
- `services/jobs.go` — replace `JobStore` with enqueue-to-Redis + read-from-Postgres;
  delete the per-job goroutine, the `maxJobs` cap, and eviction; keep the `DockingResult`
  JSON projection for `/dock/status`.
- `handlers/pocket_store.go` — replace `PocketStore` map with a `pockets`-table repository.
- **new** `store/` — pgx connection pool + repositories (runs, structures, pockets,
  molecules, docks, scores).
- **new** `migrations/` — the DDL above, versioned.
- **new** `queue/` — Redis client, envelope encode/decode, enqueue (with NX guard),
  reliable-read/ack helpers.
- **new** `workers/` — Python worker processes (one entrypoint per job type) sharing the
  generic loop; owns the `obabel`/`vina`/`fpocket`/RDKit dependencies.
- `main.go` — construct the pgx pool + Redis client at startup, inject them into handlers
  and the loop; read config from env.
- `go.mod` — add a Postgres driver (`pgx`) and a Redis client; Python deps live in the
  workers' own environment.
- Config (env): `DATABASE_URL`, `REDIS_URL`, `WORKER_CONCURRENCY`.

---

## Acceptance criteria

- [ ] The six tables exist with the columns, PK/FK, enums, and indexes in the DDL above;
      migrations apply cleanly from empty.
- [ ] `docks` has `UNIQUE (smiles_hash, pocket_id, params_hash)`; a second insert of the
      same triple is rejected / upserted, never duplicated.
- [ ] `structures` enforces `UNIQUE (run_id, kind)` — a run has exactly one `wt` and one
      `mutant` structure.
- [ ] `services/jobs.go` no longer holds a map, spawns no per-job goroutine, and has no
      `maxJobs` cap; `Submit` enqueues a `dock` job and inserts a `pending` `docks` row;
      `/dock/status` still returns the existing `DockingResult` shape, projected from the
      row.
- [ ] `handlers/pocket_store.go` no longer holds a map; pockets round-trip through
      Postgres scoped by `structure_id`; no `"monomer:3"`-style composite keys remain.
- [ ] State survives a process restart: a run interrupted mid-round resumes from
      `runs.{status,round,round_state}` at the last completed stage boundary, not round 0.
- [ ] Four queues exist (`mutagenesis`/`fpocket`/`dock`/`validate`); each job is a
      `{ type, payload, idempotency_key }` envelope.
- [ ] Delivery is at-least-once with idempotent write-back: a redelivered `dock` job
      produces no duplicate row and no corrupted result.
- [ ] An enqueue whose `idempotency_key` is already in flight is deduped (not pushed
      twice).
- [ ] A `dock` whose `(smiles_hash, pocket_id, params_hash)` already has a `done` row is
      served from cache — no worker runs Vina again.
- [ ] The worker pool is bounded by `WORKER_CONCURRENCY`; concurrent dock execution never
      exceeds it regardless of queue depth.
- [ ] The round loop blocks until every dock of the round is `done`/`error`, then scores
      the round as a batch; it does not advance early.
- [ ] Go contains no RDKit/fpocket/Vina invocation; all heavy compute is worker-side,
      reached only through the queue.

---

## Open questions / risks

- **Cross-run cache reuse.** `pocket_id` is a run-scoped UUID, so identical chemistry
  docked into "the mutant pocket" of two different runs will not share a `docks` row even
  if the geometry is identical. Reuse across runs would need a **pocket geometry
  fingerprint** as the cache dimension instead of `pocket_id`. Deferred; the intra-run
  and re-entered-round cases (the ones that matter for the loop) are covered.
- **`params_hash` contents.** Must include everything that changes a dock result — box
  center + size, exhaustiveness, and the **engine version** — so a Vina upgrade busts the
  cache. Under-specifying it risks silently serving stale scores; over-specifying it
  kills the hit rate. Pin the exact input set with [`04`](04-dual-track-docking-and-caching.md).
- **Pose storage.** `pose_path` points at a file to keep rows small, but that couples the
  DB to a filesystem/object store lifecycle (orphaned files, backup skew). Alternatives:
  `bytea` in-row, or object storage with a signed URL. The current code returns pose PDB
  inline (`PosePDB string`); a projection layer must bridge that during migration.
- **Redis durability.** Loop authority is Postgres; Redis is a rebuildable cache/queue.
  On Redis loss, in-flight jobs are lost — the recovery path re-enqueues any `docks` row
  still `pending`/`running`. This must be an explicit startup reconciliation, not an
  assumption.
- **Poison jobs / dead-letter.** `MAX_ATTEMPTS` + a dead-letter queue are sketched but the
  operational surface (alerting, replay, manual ack) is unspecified. A single poison dock
  must not wedge a run's round barrier.
- **Migration cutover.** Hard switch vs. dual-write while the in-memory path is retired.
  The `/dock` + `/dock/status` JSON contract must not change for the frontend during the
  transition; the `DockStore` projection is the seam.
- **Worker packaging & placement.** Python workers need RDKit + `fpocket`/`obabel`/`vina`
  installed and CPU headroom; how they are deployed, scaled, and pinned to
  `WORKER_CONCURRENCY` vs. host cores is an ops decision this spec does not fix.
- **Canonicalization ownership.** `smiles_hash` depends on RDKit-canonical SMILES, which
  only the `validate` worker computes — so `dock` enqueue must always follow a completed
  `validate`. If a future path needs to dock un-validated chemistry, the key derivation
  has to move (or Go needs a canonicalizer), which reopens the Go↔Python boundary.
```