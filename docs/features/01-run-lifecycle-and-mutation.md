# Stanza — Run Lifecycle & Mutation Input

A **run** is the unit of work that turns a target + resistance mutation into a
selectivity campaign; this spec defines the run resource, its state machine, and
how the mutation string becomes the WT/mutant hook every later stage hangs on.
Status: **BUILD** (new — no run or mutation concept exists yet).

See [`00-overview.md`](00-overview.md) for the product framing and build order.

---

## Goal

Introduce the **run** as Stage 0 of the pipeline. A run is created from
`{ uniprot_id, mutation, site_hint? }`. The mutation (e.g. `T790M`) is the
resistance hook: parsing it splits the campaign into a **WT track** and a
**mutant track** that stay paired through every downstream stage — structure,
pocket, molecule, dock, score. The run also owns **loop/round control** as a
persisted state machine so a crashed or paused run resumes deterministically
instead of restarting from scratch.

Concretely, this feature delivers:

1. A `runs` resource with a stable id and a lifecycle status.
2. Robust **mutation-string parsing + validation** (WT residue, 1-based
   position, mutant residue).
3. `site_hint` semantics — an optional pocket/region hint that biases later
   pocket selection without constraining it.
4. Three new endpoints: `POST /runs`, `GET /runs/:id`, `GET /runs`.
5. The `run_id` foreign key that threads into structures, pockets, molecules,
   docks, and scores.

Persistence mechanics are intentionally out of scope here — see
[`08-persistence-and-queue.md`](08-persistence-and-queue.md).

---

## Current state

There is **no run and no mutation concept** in the codebase today. The current
API is a stateless structure-explorer keyed on UniProt/AlphaFold ids:

| File | What it does today | Relevance to runs |
|---|---|---|
| [`main.go`](../../main.go) | Registers all routes on Gin `:8080`: `/health`, `/search`, `/complex/:id`, `/complex/:id/binding-sites`, `/complex/:id/drugs`, `/chembl`, `/dock`, `/dock/status`. | New `/runs*` routes register here. |
| [`handlers/complex.go`](../../handlers/complex.go) | `ComplexDetailHandler`, `ComplexDrugsHandler`, and `normalizeToUniProtID` (strips `AF-…-F1` → accession). | Run creation reuses `normalizeToUniProtID` to normalize the incoming `uniprot_id`. |
| [`handlers/search.go`](../../handlers/search.go) | `buildComplexFromUniProt` — concurrent UniProt + AlphaFold fetch, Swiss-Prot gate, returns `models.Complex`. | Run **validation** step reuses this to confirm the target resolves before a run leaves `draft`. |
| [`models/complex.go`](../../models/complex.go) | `Complex` struct (target metadata) + `SearchResult`. | The run references a target; it does not embed the whole `Complex`. |

Key facts that shape the design:

- Module path is `github.com/ayush00git/stanza`; only external deps are
  `gin-gonic/gin` and `google/uuid`.
- The only "dual track" in the code today is **monomer vs. dimer**
  (an oligomerization axis), which is unrelated to the **WT vs. mutant** axis a
  run introduces. Do not overload it.
- Jobs (docking) currently live in an in-memory store with no durability; the
  run state machine is what makes the pipeline crash-resumable.

---

## Design

### The run resource

A run is a small, durable header record. It does **not** embed structures,
pockets, or molecules — those are separate rows that carry `run_id` as a foreign
key. Keeping the run thin lets it be the single source of truth for *where the
campaign is* without bloating.

```
run
├── id           uuid          stable handle for every downstream artifact
├── uniprot_id   string        normalized accession (post normalizeToUniProtID)
├── mutation     string        canonical form, e.g. "T790M"
├── site_hint    string?       optional pocket/region hint (see below)
├── status       enum          run-level lifecycle state (see state machine)
├── round        int           current generation round, 0-based
└── created_at   timestamp
```

The parsed mutation components (WT residue, position, mutant residue) are
derived and stored alongside so downstream stages never re-parse the string.

### Mutation string parsing

The mutation is given in **1-letter substitution notation**:
`<WT residue><1-based position><mutant residue>`, e.g. `T790M` =
threonine at position 790 → methionine.

Grammar (single-substitution, the only form this stage supports):

```
mutation  := AA POS AA
AA        := one of the 20 standard 1-letter codes [ACDEFGHIKLMNPQRSTVWY]
POS       := integer >= 1, no leading zeros
```

Parsing produces a `ParsedMutation{ WT, Position, Mut }` and must reject:

| Case | Example | Reason |
|---|---|---|
| Wrong shape | `790`, `TM790`, `p.T790M` | not `AA POS AA`. |
| Non-standard residue letter | `B790M`, `T790X` | not one of the 20 codes. |
| Silent / no-op mutation | `T790T` | WT == mutant; nothing to differentiate. |
| Position out of range | `T0M`, `T-5M` | must be `>= 1`. |
| Position beyond sequence | `T5000M` on a 700-aa protein | validated against the resolved sequence length (deferred to the validation step, since it needs the fetched structure). |
| WT residue mismatch | `T790M` where residue 790 is not T | checked in the validation step against the resolved sequence. |

Two-tier validation: **syntactic** checks run synchronously in `POST /runs`
(cheap, reject immediately with `400`); **semantic** checks (position within
sequence, WT letter matches the actual residue) run in the `draft → validated`
transition, because they need the UniProt/AlphaFold fetch. A run that fails
semantic validation moves to `failed` with a reason, not `400`, because the
request itself was well-formed.

Canonicalization: uppercase the residue letters; strip surrounding whitespace.
Store the canonical form so `t790m` and `T790M` map to the same run identity.

### site_hint semantics

`site_hint` is an **optional, advisory** free-form pointer at where in the
protein the campaign should focus. It biases but never overrides automated
pocket detection ([`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md)).
Accepted, best-effort-interpreted forms:

- **Residue / range** — `790`, `790-800`: prefer pockets whose lining residues
  overlap this span. The mutation position is a natural default when omitted.
- **Named region** — `ATP-site`, `allosteric`: a label recorded for display and
  as a soft filter hint; not resolved to coordinates at this stage.

When absent, downstream pocket selection falls back to the **mutation position**
as the region of interest (the mutated residue is almost always the point of
resistance). `site_hint` is stored verbatim plus a parsed structured form when
it matches the residue/range shape.

### Run state machine (run-level lifecycle)

The run status is the **outer** loop. It advances monotonically except for the
round-loop back-edge (`scored → generating`) and terminal failure.

```
              ┌───────────────────────────────────────────────┐
              │                                               (back-edge)
 draft ──▶ validated ──▶ [ round loop ] ──▶ done
   │            │                                  ▲
   │            │                                  │
   └────────────┴──────────▶ failed  ◀─────────────┘
        (bad target / mutation)   (any stage errors out)
```

| Status | Meaning | Entry condition | Exit |
|---|---|---|---|
| `draft` | Created; syntactic parse passed. | `POST /runs` succeeds. | Kick off validation. |
| `validated` | Target resolves; mutation is semantically valid against the sequence; WT + mutant tracks provisioned. | UniProt/AlphaFold fetch + WT-residue match OK. | Enter first round. |
| `running` | At least one round is in progress (see per-round states). | First round starts. | Round completes → `scored`, or errors → `failed`. |
| `done` | Stop condition met (max rounds hit, or a molecule clears the selectivity bar). | Round loop terminates successfully. | terminal. |
| `failed` | Unrecoverable error (bad target, invalid mutation, stage crash without retry budget). | Any transition raises a terminal error. | terminal; carries `error` reason. |

`round` increments each time the loop re-enters generation. The stop condition
(max rounds vs. selectivity threshold) is owned by
[`06-generation-loop.md`](06-generation-loop.md); the run just records the
current round and the terminal outcome.

### Per-round loop states

Within a round, the pipeline walks a fixed sequence of stages. Each stage runs
**dual-track** (WT and mutant) where applicable. These are the states a
`GET /runs/:id` exposes so a UI can show *exactly* where the loop is.

```
generating ──▶ validating_mols ──▶ docking ──▶ scoring ──▶ (decide)
                                                              │
                                    ┌─────────────────────────┤
                              next round                    stop
                        (round++, → generating)        (→ run done)
```

| Round state | Stage | Track behavior | Spec |
|---|---|---|---|
| `generating` | Propose candidate molecules for the current round. | Informed by the WT↔mutant pocket delta. | [`06-generation-loop.md`](06-generation-loop.md) |
| `validating_mols` | Structural/drug-likeness filtering of proposed SMILES. | Track-agnostic (per molecule). | [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md) |
| `docking` | Dock each surviving molecule into **both** pockets. | Dual-track; cached per (molecule, pocket). | [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md) |
| `scoring` | Compute `wt_score`, `mutant_score`, and selectivity margin `wt_score − mutant_score`; rank. | Combines both tracks. | [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md) |

The run persists `{ status, round, round_state }` after each stage so a crash
resumes at the last completed stage boundary rather than the last completed
molecule. Idempotent stage work (dockings keyed by molecule+pocket) means a
resumed round re-enters cleanly without duplicating compute — the caching
contract lives in [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md).

### How run_id threads downstream

`run_id` is the spine. Every artifact a run produces carries it as a foreign
key, and the WT/mutant split is expressed with a `track` discriminator on the
per-structure rows:

```
run (1) ──┬─▶ structure   (2 per run: track = "wt" | "mutant")
          ├─▶ pocket       (N per structure, FK → structure)
          ├─▶ molecule     (N per run, FK → run, round-tagged)
          ├─▶ dock         (molecule × pocket, FK → molecule + pocket; carries track)
          └─▶ score        (per molecule per round: wt_score, mutant_score, margin)
```

- **structures** — exactly two per run (WT + mutant), distinguished by `track`.
  WT comes from the existing AlphaFold fetch path; mutant is produced by
  [`02-mutagenesis.md`](02-mutagenesis.md).
- **pockets** — belong to a structure, so they inherit the track transitively;
  the WT↔mutant pairing/delta is [`03-…`](03-dual-pocket-analysis-and-delta.md).
- **molecules** — belong to the run and are tagged with the `round` that
  produced them.
- **docks / scores** — reference molecule and pocket; a score row rolls up both
  tracks into the selectivity margin.

Table shapes and the store interface are defined in
[`08-persistence-and-queue.md`](08-persistence-and-queue.md); this spec only
fixes the **relationships** and the `track` discriminator convention.

---

## Contracts

### Go types

```go
// models/run.go (new)
package models

// RunStatus is the run-level lifecycle state (outer loop).
type RunStatus string

const (
	RunDraft     RunStatus = "draft"
	RunValidated RunStatus = "validated"
	RunRunning   RunStatus = "running"
	RunDone      RunStatus = "done"
	RunFailed    RunStatus = "failed"
)

// RoundState is the per-round stage the pipeline is currently in.
type RoundState string

const (
	RoundGenerating     RoundState = "generating"
	RoundValidatingMols RoundState = "validating_mols"
	RoundDocking        RoundState = "docking"
	RoundScoring        RoundState = "scoring"
)

// Track distinguishes the wild-type and mutant tracks on per-structure artifacts.
type Track string

const (
	TrackWT     Track = "wt"
	TrackMutant Track = "mutant"
)

// ParsedMutation is the decomposed substitution, derived once at run creation.
type ParsedMutation struct {
	WT       string `json:"wt"`       // 1-letter WT residue, e.g. "T"
	Position int    `json:"position"` // 1-based, e.g. 790
	Mut      string `json:"mut"`      // 1-letter mutant residue, e.g. "M"
}

// Run is the Stage-0 header record. Thin by design; downstream artifacts carry run_id.
type Run struct {
	ID         string         `json:"id"`
	UniprotID  string         `json:"uniprot_id"`
	Mutation   string         `json:"mutation"`          // canonical, e.g. "T790M"
	Parsed     ParsedMutation `json:"parsed_mutation"`
	SiteHint   string         `json:"site_hint,omitempty"`
	Status     RunStatus      `json:"status"`
	RoundState RoundState     `json:"round_state,omitempty"` // set while Status == running
	Round      int            `json:"round"`
	Error      string         `json:"error,omitempty"`       // set when Status == failed
	CreatedAt  time.Time      `json:"created_at"`
}
```

### Parsing signature

```go
// services/mutation.go (new)
// ParseMutation does the SYNTACTIC parse only (shape + standard residues + no-op
// guard). Semantic checks (position within sequence, WT letter matches the
// actual residue) happen later, in the draft→validated transition, because they
// need the fetched sequence.
func ParseMutation(raw string) (models.ParsedMutation, error)
```

### Routes

Registered in [`main.go`](../../main.go) alongside the existing handlers.

| Method | Path | Handler | Purpose |
|---|---|---|---|
| `POST` | `/runs` | `RunCreateHandler` | Create a run; returns id + initial state. |
| `GET` | `/runs/:id` | `RunDetailHandler` | Full status incl. round + per-track state. |
| `GET` | `/runs` | `RunListHandler` | List runs (newest first), summary fields. |

#### `POST /runs`

Request:

```json
{
  "uniprot_id": "P00533",
  "mutation": "T790M",
  "site_hint": "790-800"
}
```

`201 Created` — syntactic parse passed, run persisted in `draft`:

```json
{
  "id": "b1f0…",
  "uniprot_id": "P00533",
  "mutation": "T790M",
  "parsed_mutation": { "wt": "T", "position": 790, "mut": "M" },
  "site_hint": "790-800",
  "status": "draft",
  "round": 0,
  "created_at": "2026-07-08T10:00:00Z"
}
```

`400 Bad Request` — missing `uniprot_id`, or mutation fails **syntactic**
parse:

```json
{ "error": "invalid mutation \"T790T\": wild-type and mutant residue are identical" }
```

Validation flow: `RunCreateHandler` normalizes `uniprot_id` via
`normalizeToUniProtID`, runs `ParseMutation` (reject → `400`), persists the run
in `draft`, then kicks off the async `draft → validated` transition (target
resolution via `buildComplexFromUniProt` + WT-residue match). A semantic failure
lands the run in `failed` with an `error`, retrievable through `GET /runs/:id` —
it is **not** a `400`, because the request was well-formed.

#### `GET /runs/:id`

`200 OK` — a running run mid-loop:

```json
{
  "id": "b1f0…",
  "uniprot_id": "P00533",
  "mutation": "T790M",
  "parsed_mutation": { "wt": "T", "position": 790, "mut": "M" },
  "site_hint": "790-800",
  "status": "running",
  "round": 1,
  "round_state": "docking",
  "created_at": "2026-07-08T10:00:00Z"
}
```

`404 Not Found` — unknown id: `{ "error": "run not found" }`.

#### `GET /runs`

`200 OK` — newest first:

```json
{
  "count": 2,
  "runs": [
    { "id": "b1f0…", "uniprot_id": "P00533", "mutation": "T790M",
      "status": "running", "round": 1, "round_state": "docking",
      "created_at": "2026-07-08T10:00:00Z" },
    { "id": "9ac2…", "uniprot_id": "P00533", "mutation": "L858R",
      "status": "done", "round": 3, "created_at": "2026-07-07T09:00:00Z" }
  ]
}
```

### Handler signatures

```go
// handlers/run.go (new)
func RunCreateHandler(c *gin.Context) // POST /runs
func RunDetailHandler(c *gin.Context) // GET  /runs/:id
func RunListHandler(c *gin.Context)   // GET  /runs
```

---

## Dependencies & touch points

| Sibling spec | Relationship |
|---|---|
| [`00-overview.md`](00-overview.md) | Product framing; run is Stage 0, the "spine everything hangs on". |
| [`02-mutagenesis.md`](02-mutagenesis.md) | Consumes `ParsedMutation` + the resolved WT structure to build the mutant track; the semantic WT-residue check shares its sequence resolution. |
| [`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md) | `site_hint` biases pocket selection; pockets inherit `track` via their structure. |
| [`06-generation-loop.md`](06-generation-loop.md) | Owns the round stop condition; drives `round` increments and the `scored → generating` back-edge. |
| [`05-molecule-validation-rdkit.md`](05-molecule-validation-rdkit.md) | The `validating_mols` round state. |
| [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md) | The `docking` round state; idempotent per-(molecule, pocket) caching is what makes a resumed round safe. |
| [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md) | The `scoring` round state; produces `wt_score`, `mutant_score`, margin. |
| [`08-persistence-and-queue.md`](08-persistence-and-queue.md) | Owns table DDL, the store interface, and how `status`/`round`/`round_state` are persisted for crash-resume. This spec fixes relationships + the `track` convention only. |
| [`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md) | Consumes `POST/GET /runs`; renders lifecycle + per-round state; mutation input form. |

**Code touch points (this feature):**
- `main.go` — register `/runs`, `/runs/:id`, `/runs` routes.
- `models/run.go` — new (`Run`, `RunStatus`, `RoundState`, `Track`, `ParsedMutation`).
- `services/mutation.go` — new (`ParseMutation`).
- `handlers/run.go` — new (the three handlers).
- Reuse `normalizeToUniProtID` (`handlers/complex.go`) and
  `buildComplexFromUniProt` (`handlers/search.go`) for target resolution.

---

## Acceptance criteria

- [ ] `POST /runs` with a valid `{ uniprot_id, mutation }` returns `201` with a
      uuid, canonical mutation, `parsed_mutation`, and `status: "draft"`.
- [ ] `uniprot_id` is normalized with `normalizeToUniProtID` (accepts
      `AF-P00533-F1` and `P00533` identically).
- [ ] `ParseMutation` accepts `T790M`/`t790m` (→ canonical `T790M`) and rejects
      `790`, `TM790`, `p.T790M`, `B790M`, `T790X`, `T790T`, `T0M`, `T-5M` with a
      `400` and a human-readable reason.
- [ ] Semantic validation (position within sequence + WT-residue match) runs on
      the `draft → validated` transition; failure lands the run in `failed` with
      an `error`, not a `400`.
- [ ] A validated run provisions exactly two structure references, `track = wt`
      and `track = mutant`.
- [ ] `site_hint` is optional; when a residue/range shape is given it is stored
      both verbatim and parsed; when absent, the mutation position is the default
      region of interest.
- [ ] `GET /runs/:id` returns run-level `status` plus, while `running`, the
      current `round` and `round_state`.
- [ ] `GET /runs/:id` on an unknown id returns `404`.
- [ ] `GET /runs` lists runs newest-first with summary fields.
- [ ] Run status + round + round_state persist across a process restart so an
      interrupted run resumes at the last completed stage boundary (persistence
      via [`08-persistence-and-queue.md`](08-persistence-and-queue.md)).
- [ ] Every downstream artifact created for a run carries `run_id`; per-structure
      artifacts carry a `track` discriminator.
- [ ] The run's WT/mutant axis is kept distinct from the pre-existing
      monomer/dimer axis (no field or enum reuse between them).

---

## Open questions / risks

- **Isoform / sequence numbering.** The 1-based position assumes the canonical
  UniProt sequence. Isoforms, signal-peptide cleavage, and PDB-vs-UniProt
  numbering can shift the index — a `T790M` that is valid on one numbering is a
  mismatch on another. Decision needed on whether to pin canonical UniProt
  numbering (simplest) and surface a clear mismatch error. Ties into
  [`02-mutagenesis.md`](02-mutagenesis.md).
- **Single-substitution only.** Grammar covers one point substitution. Multi-
  mutants (`T790M/C797S`), insertions, deletions, and frameshifts are out of
  scope for v1; the parser must reject them cleanly rather than mis-parse.
- **Duplicate runs.** Should `(uniprot_id, mutation, site_hint)` be unique, or
  are repeat runs (e.g. re-running with a newer generation model) allowed?
  Leaning allowed, distinguished by `id` + `created_at`; revisit if the run list
  gets noisy.
- **Where does the loop execute.** This spec defines the states, not the driver.
  Whether rounds advance in-process (goroutine) or via a queue/worker is
  [`08-persistence-and-queue.md`](08-persistence-and-queue.md); the state machine
  must be identical either way.
- **Cancellation.** No explicit `cancel`/`pause` transition is specified yet. A
  `DELETE /runs/:id` or a `cancelled` terminal state is a likely near-term add
  once long-running loops exist.
- **site_hint expressiveness.** Free-form now. If named regions (`ATP-site`)
  need to resolve to residues, that logic belongs with pocket analysis
  ([`03-…`](03-dual-pocket-analysis-and-delta.md)), not here — risk of scope
  creep into this stage.
