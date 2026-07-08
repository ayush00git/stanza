# Stanza — Frontend Resistance UI

Re-orient the existing dual Mol\* explorer from the **monomer/dimer** axis to the
**wild-type/mutant** axis: render the chosen pose inside the *mutant* pocket,
spotlight the *mutated residue*, rank molecules by **selectivity margin**
(`wt_score − mutant_score`), and drive a run's generation loop from the browser.
**Status: EXTEND** — the two-structure viewer, per-structure highlight, pose
overlay, and full-screen machinery already exist and are reused wholesale.

See [`00-overview.md`](00-overview.md) for the product framing and build order.

---

## Goal

Turn the current structure-explorer page into the **resistance demo surface**:

1. **WT vs. mutant viewers.** The two side-by-side Mol\* canvases render
   `wt_structure` and `mutant_structure` instead of monomer/dimer. The mutant is
   the pocket we design *against*; the WT is the selectivity check.
2. **Mutated-residue emphasis.** On both structures, the residue at the mutation
   position is highlighted *distinctly* from the green pocket overlay, so the eye
   lands on the difference the mutation makes (e.g. `Thr790` → `Met790`).
3. **Selectivity board.** The affinity-only leaderboard becomes a board ranked by
   fitness / selectivity margin, showing `wt_score`, `mutant_score`, `margin`, and
   `QED` per molecule (from [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md)).
4. **Run / loop UI.** Start a run from `{ uniprot_id, mutation }`
   (`POST /runs`, [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md)),
   watch rounds advance, and browse molecule **lineage** (round / parent) with the
   selected molecule's pose shown in the mutant pocket.

This spec is UI-only. It consumes contracts owned elsewhere ([`01`](01-run-lifecycle-and-mutation.md),
[`02`](02-mutagenesis.md), [`03`](03-dual-pocket-analysis-and-delta.md),
[`07`](07-selectivity-scoring-and-ranking.md)); where it needs a shape those specs
have not frozen yet, it states the shape it needs and flags the ownership.

---

## Current state

The dual-track frontend is real; it points at the wrong pair of structures.

| File | What it does today | Re-serves (WT/mutant) |
|---|---|---|
| [`app/src/pages/ComplexViewerPage.tsx`](../../app/src/pages/ComplexViewerPage.tsx) | Route `/structure/:id`. Two `MolstarViewer`s (monomer + dimer), representation switcher, pLDDT legend, per-viewer full-screen, pocket list, docking section, results leaderboard. Selection kept **separately per structure** (`selectedMonomer`/`selectedDimer`); `source_type` routes each pocket/pose to its own viewer. | Rename the axis to WT/mutant; keep the per-structure selection + pose-routing pattern verbatim. |
| [`app/src/components/viewer/MolstarViewer.tsx`](../../app/src/components/viewer/MolstarViewer.tsx) | One Mol\* canvas. Props: `url`, `label`, `plddt`, `representation`, `highlight: HighlightResidue[]`, `pose: string \| null`. | Add one prop: `mutatedResidue`. |
| [`app/src/components/viewer/useMolstar.ts`](../../app/src/components/viewer/useMolstar.ts) | Mol\* lifecycle. Pocket highlight = **green** selection-manager marking (no auto-zoom, overlay on top of pLDDT). Pose overlay = red spacefill + translucent yellow halo, best model only, tracked in `poseStructRef` and skipped by receptor recoloring/restyling. `buildResidueLoci(structure, HighlightResidue[])` builds a `(chain, auth_seq_id)` Loci. | Add a **second, distinct** emphasis for the mutated residue that reuses `buildResidueLoci`; track + skip it like the pose. |
| [`app/src/components/viewer/DockedResults.tsx`](../../app/src/components/viewer/DockedResults.tsx) | "Recent docks" leaderboard sorted by **raw** `binding_affinity` (most negative = #1). `entryKey(e) = source_type-pocket_id-chembl_id`. Clicking a row loads its pose into the matching viewer. | Evolve into the **selectivity board** (rank by margin/fitness; `wt_score`/`mutant_score`/`margin`/`QED` columns). |
| [`app/src/components/viewer/DockingPanel.tsx`](../../app/src/components/viewer/DockingPanel.tsx) | Fetches ChEMBL candidates for the selected pocket and docks them (`submitDock` → poll `getDockStatus`). | Reused for **manual** docking into the mutant pocket; the loop supplies molecules automatically. |
| [`app/src/lib/api.ts`](../../app/src/lib/api.ts) | Typed client: `getComplex`, `getBindingSites`, `getChemblFragments`, `submitDock`/`getDockStatus`, SSE `searchComplexes`. Types: `Complex`, `Pocket`, `DockedPose`, `DockingResult`, `Conformation`. | Add run + selectivity client fns and types (below). |

The building blocks — two structures, per-structure highlight, per-structure pose
overlay, full-screen, an SSE client pattern — are exactly what the resistance view
needs. Nothing here is rebuilt; it is re-pointed.

---

## Design

### A. WT vs. mutant viewers

A **run-centric page** replaces the id-centric explorer for the resistance view.
Recommended: a new `RunViewerPage` at **`/runs/:id`** built from the *same* parts
(`StructurePanel`, `PoseCaption`, `MolstarViewer`) so the existing monomer/dimer
explorer at `/structure/:id` keeps working. (Alternatively, re-point
`ComplexViewerPage` itself — the diff is mechanical; either way the viewer
plumbing is identical.)

The rename is one-to-one:

| Today (monomer/dimer) | Run view (WT/mutant) |
|---|---|
| `complex.monomer_structure_url` / `complex.complex_structure_url` | `structures.wt.url` / `structures.mutant.url` |
| `selectedMonomer` / `selectedDimer` | `selectedWT` / `selectedMutant` |
| `monomerHighlight` / `dimerHighlight` | `wtHighlight` / `mutantHighlight` |
| `source_type: 'monomer' \| 'dimer'` | `track: 'wt' \| 'mutant'` (`Track` from [`01`](01-run-lifecycle-and-mutation.md)) |
| `fullscreen: 'monomer' \| 'dimer' \| 'both'` | `fullscreen: 'wt' \| 'mutant' \| 'both'` |
| Labels "Monomer · single chain" / "Dimer · complex" | "Wild type" / "Mutant" |

Layout intent: **mutant on the right as the primary design target**, WT on the
left as the selectivity reference. Each viewer carries a provenance sub-label from
[`02-mutagenesis.md`](02-mutagenesis.md): the WT panel shows its source badge
(`experimental_holo` / `experimental_apo` / `alphafold`, with PDB id + resolution
when experimental); the mutant panel shows the mutagenesis engine and, when
present, the folding **ΔΔG** (nullable — hide the chip when `null`).

Pocket highlight and pose routing keep the existing per-structure discipline:
clicking a mutant pocket highlights it in the mutant viewer only; a docked pose
overlays in the viewer whose `track` it belongs to. By default the **mutant
resistance pocket** (from [`03`](03-dual-pocket-analysis-and-delta.md),
`mutant_pocket.pocket_id`) is pre-selected on the mutant viewer so the demo opens
on the right site.

### B. Mutated-residue highlight (distinct emphasis)

The pocket highlight already uses the selection manager's single `selectColor`
(green). The mutated residue needs a **second, visually separable** treatment, so
it cannot share that channel. Reuse the **pose-overlay pattern** instead of the
marking pattern: build the residue Loci with the existing `buildResidueLoci`, then
add a dedicated **component + representation** for it — ball-and-stick in a
distinct color (e.g. magenta `0xdb2777`) with a residue label — layered on top of
the green pocket marking and the pLDDT coloring.

Because it is an added receptor sub-component (like the pose), it must be:

- **Tracked** in a ref (`mutatedStructRef` / component ref), mirroring
  `poseStructRef`.
- **Skipped** by `updateRepresentation` and `applyPlddtColoring` (add its ref to
  the same skip check the pose already uses), so a representation swap or recolor
  doesn't repaint it.
- **Re-applied** after a structure reload and a representation change, exactly as
  `applyHighlight` / `setPose` are re-applied in `loadStructure`.
- **Replaced/removed** when the `mutatedResidue` prop changes, so at most one is in
  the scene.

Both viewers receive the same `{ chain, index }` (mutagenesis preserves numbering
across tracks — see [`02`](02-mutagenesis.md)/[`03`](03-dual-pocket-analysis-and-delta.md)),
so the WT canvas emphasizes the original residue and the mutant canvas emphasizes
the substituted one at the same position. The color layering stays legible: green
= pocket lining, magenta = the mutated residue, red/yellow = the docked pose.

### C. Selectivity board (evolve `DockedResults`)

The leaderboard stops ranking on one raw affinity and starts ranking on
**selectivity**. Each row is a *molecule* (not a per-pocket dock), carrying both
tracks' scores:

- **Columns:** rank · molecule (name/ChEMBL id + a `round · parent` lineage badge)
  · `wt_score` · `mutant_score` · **`margin`** (`wt_score − mutant_score`,
  emphasized) · `QED`.
- **Sort:** by the run's **fitness** when present, else by `margin` descending —
  **large positive margin ranks #1** (binds the mutant, spares the WT). This flips
  the current "most-negative affinity first" rule.
- **Margin tone:** reuse the affinity-tone idea but on margin — large positive =
  accent/green (selective), near-zero = muted, negative = a warning tone
  (`conf-verylow`) since it binds the WT *better*, the failure mode.
- **Click behavior:** selecting a row loads that molecule's **mutant pose** into
  the mutant viewer (inside the mutant pocket) and, when a WT pose exists, the
  **WT pose** into the WT viewer — a side-by-side visual of why the margin is what
  it is. The mutated residue stays highlighted in both.
- **Identity:** `entryKey` becomes the molecule id (`SelectivityEntry.molecule_id`)
  rather than `source_type-pocket_id-chembl_id`, because a molecule is now scored
  once across both tracks rather than once per pocket.

Keep it a self-contained component (`SelectivityBoard`) so both the run view and
any manual-docking view can mount it; a raw-affinity fallback path can remain for
the legacy explorer.

### D. Run / generation-loop UI

Three new pieces wrap the viewers into a run experience:

1. **Launcher** (`RunLauncherForm`). A small form — `uniprot_id`, `mutation`
   (e.g. `T790M`), optional `site_hint` — that calls `createRun` (`POST /runs`).
   On `201` it routes to `/runs/:id`. It surfaces the `400` syntactic-parse error
   verbatim (e.g. *"invalid mutation \"T790T\": wild-type and mutant residue are
   identical"*) inline, and after creation shows the async `draft → validated`
   outcome (a semantic failure lands the run in `failed` with an `error`, not a
   `400` — see [`01`](01-run-lifecycle-and-mutation.md)).

2. **Round progress** (`RoundStepper`). Reads the run's `status` +
   `round` + `round_state` and renders the outer lifecycle
   (`draft → validated → running → done/failed`) plus the inner per-round stepper
   (`generating → validating_mols → docking → scoring`). It updates live via
   `watchRun` (SSE preferred, polling fallback — see Contracts). The mutation shows
   as a chip rendered from `parsed_mutation` (`T` → `M` at `790`).

3. **Molecule lineage** (`MoleculeLineage`). Groups the run's molecules by
   `round`, drawing parent → child edges via `parent_id`, so you can trace how a
   round-2 candidate descends from a round-0 seed. Selecting a node is the same
   action as selecting a board row: it shows that molecule's pose in the mutant
   pocket. Round 0 = seeds (no parent); later rounds attach under their parent.

The selectivity board (C) is fed by the run's scored molecules and grows as rounds
complete. An optional **`PocketDeltaPanel`** renders the
[`03`](03-dual-pocket-analysis-and-delta.md) `pocket_delta` (the `changed`
substitution, Δvolume / Δhydrophobicity / Δpolarity, H-bonds gained/lost, and the
one-line `effect`) next to the mutant viewer, giving the "why the WT drug fails"
context in plain sight. `DockingPanel` remains available for manual docking of an
ad-hoc SMILES into the mutant pocket.

Page composition (run view):

```
RunViewerPage (/runs/:id)
├── header: target · mutation chip · RoundStepper · provenance badges (WT source, mutant engine/ΔΔG)
├── viewers:  [ WT (StructurePanel) ]  [ Mutant (StructurePanel) ]   ← reused verbatim
│               · pocket highlight (green)   · mutated-residue highlight (magenta)   · pose overlay (red/yellow)
├── PocketDeltaPanel        (03: mutant_pocket + pocket_delta)         [new, optional]
├── SelectivityBoard        (07: ranked by margin/fitness)            [evolved DockedResults]
├── MoleculeLineage         (round / parent tree)                     [new]
└── DockingPanel            (manual dock into mutant pocket)          [reused]
```

---

## Contracts

### `app/src/lib/api.ts` — new types

Mirror the Go types from the sibling specs (JSON tags).

```ts
// ── Run lifecycle (01) ────────────────────────────────────────────────
export type RunStatus = 'draft' | 'validated' | 'running' | 'done' | 'failed'
export type RoundState = 'generating' | 'validating_mols' | 'docking' | 'scoring'
export type Track = 'wt' | 'mutant'

export type ParsedMutation = { wt: string; position: number; mut: string }

export type Run = {
  id: string
  uniprot_id: string
  mutation: string                 // canonical, e.g. "T790M"
  parsed_mutation: ParsedMutation
  site_hint?: string
  status: RunStatus
  round: number
  round_state?: RoundState         // set while status === 'running'
  error?: string                   // set when status === 'failed'
  created_at: string
}

export type RunSummary = Pick<
  Run, 'id' | 'uniprot_id' | 'mutation' | 'status' | 'round' | 'round_state' | 'created_at'
>

// ── Resolved structures + mutated residue (02) ────────────────────────
export type StructureSource = 'experimental_holo' | 'experimental_apo' | 'alphafold'

export type RunStructures = {
  wt: {
    url: string
    source: StructureSource
    source_id: string              // PDB id or AlphaFold entry
    resolution: number             // Å, 0 if predicted
    plddt: number                  // 0 if experimental
    chain: string
  }
  mutant: {
    url: string
    engine: string                 // "foldx" | "pymol" | "refold"
    ddg: number | null             // kcal/mol; null unless engine yields it
  }
  // Same (chain, index) on both tracks — numbering is preserved (02/03).
  mutated_residue: { chain: string; index: number; wt_name: string; mut_name: string }
}

// ── Resistance-pocket context (03) ────────────────────────────────────
export type MutantPocket = {
  key_residues: string[]           // ["Met790","Leu718",...]
  volume: number
  hydrophobicity: number
  polarity?: number
  center: [number, number, number]
  pocket_id: number
}
export type PocketDelta = {
  changed: string[]                // ["Thr790→Met790"]
  residues_gained?: string[]
  residues_lost?: string[]
  d_volume: number
  d_hydrophobicity: number
  d_polarity: number
  hbonds_gained?: string[]
  hbonds_lost?: string[]
  effect: string                   // one-line summary
}
export type MutantPocketContext = { mutant_pocket: MutantPocket; pocket_delta: PocketDelta }

// ── Selectivity results (07) ──────────────────────────────────────────
export type SelectivityEntry = {
  molecule_id: string
  round: number
  parent_id?: string | null        // lineage; null/absent for round-0 seeds
  chembl_id?: string
  name?: string
  smiles: string
  wt_score: number
  mutant_score: number
  margin: number                   // wt_score - mutant_score
  qed?: number
  fitness?: number                 // composite ranking score when present
  wt_pose_pdb?: string             // raw PDB → WT viewer pose overlay
  mutant_pose_pdb?: string         // raw PDB → mutant viewer pose overlay
}
```

### `app/src/lib/api.ts` — new client functions

`POST/GET /runs` are owned by [`01`](01-run-lifecycle-and-mutation.md). The
`/runs/:id/structures`, `/runs/:id/pocket`, and `/runs/:id/results` endpoints are
the read surfaces this UI needs; their exact decomposition is owned with
[`02`](02-mutagenesis.md)/[`03`](03-dual-pocket-analysis-and-delta.md)/[`07`](07-selectivity-scoring-and-ranking.md)
and [`08-persistence-and-queue.md`](08-persistence-and-queue.md) — the shapes above
are the contract.

```ts
// POST /runs — create a run; 400 on syntactic parse failure (surface .error).
export function createRun(
  req: { uniprot_id: string; mutation: string; site_hint?: string },
  signal?: AbortSignal,
): Promise<Run>

// GET /runs/:id — full lifecycle (status + round + round_state + error).
export function getRun(id: string, signal?: AbortSignal): Promise<Run>

// GET /runs — newest first.
export function listRuns(signal?: AbortSignal): Promise<{ count: number; runs: RunSummary[] }>

// GET /runs/:id/structures — resolved WT/mutant URLs, provenance, mutated residue.
export function getRunStructures(id: string, signal?: AbortSignal): Promise<RunStructures>

// GET /runs/:id/pocket — mutant resistance pocket + delta (03).
export function getRunPocket(id: string, signal?: AbortSignal): Promise<MutantPocketContext>

// GET /runs/:id/results — scored molecules across rounds (07).
export function getRunResults(id: string, signal?: AbortSignal): Promise<SelectivityEntry[]>

// Live run progress. Preferred: SSE at GET /runs/:id/events emitting a `status`
// frame on each round_state transition (same EventSource pattern as
// searchComplexes). Fallback for MVP: poll getRun on an interval. Returns a
// cancel fn either way.
export function watchRun(
  id: string,
  cb: { onStatus: (run: Run) => void; onDone: (run: Run) => void; onError: (msg: string) => void },
  opts?: { pollMs?: number },
): () => void
```

`watchRun` sketch: open `EventSource('/runs/:id/events')`; on each `status` frame
`JSON.parse` a `Run` and call `onStatus`; when `status` is `done`/`failed` call
`onDone` and `close()`. If the events endpoint is not available, degrade to
`setInterval(() => getRun(id).then(onStatus), pollMs ?? 2000)` and stop on a
terminal status — this mirrors the existing SSE-with-cancel shape of
`searchComplexes`, and results (`getRunResults`) are re-fetched when
`round_state` advances to/through `scoring`.

### Component props / sketch

| Component | Kind | Props (delta) |
|---|---|---|
| `MolstarViewer` | **change** | add `mutatedResidue?: HighlightResidue \| null` (passed straight to `useMolstar`). |
| `useMolstar` | **change** | add option `mutatedResidue?: HighlightResidue \| null`; new `applyMutatedResidue(plugin, residue)` (component + ball-and-stick in `MUTATED_RESIDUE` color, reusing `buildResidueLoci`); track in a ref, skip in `updateRepresentation`/`applyPlddtColoring`, re-apply in `loadStructure`. |
| `SelectivityBoard` | **evolve** `DockedResults` | `{ entries: SelectivityEntry[]; activeId: string \| null; onSelect: (e: SelectivityEntry) => void; onRemove?: (e) => void; sortBy?: 'fitness' \| 'margin' \| 'mutant_score' }`. `entryKey = molecule_id`. Columns wt/mutant/margin/QED; sort margin-desc by default. |
| `RunLauncherForm` | **new** | `{ onCreated: (run: Run) => void }`. Fields `uniprot_id`, `mutation`, `site_hint?`; renders `400` reason inline. |
| `RoundStepper` | **new** | `{ status: RunStatus; round: number; roundState?: RoundState; error?: string }`. |
| `MoleculeLineage` | **new** | `{ entries: SelectivityEntry[]; activeId: string \| null; onSelect: (e: SelectivityEntry) => void }`. Groups by `round`, edges via `parent_id`. |
| `PocketDeltaPanel` | **new**, optional | `{ context: MutantPocketContext; mutatedResidue: RunStructures['mutated_residue'] }`. |
| `RunViewerPage` | **new** (or re-point `ComplexViewerPage`) | route `/runs/:id`; wires `getRunStructures` + `getRunPocket` + `watchRun` + `getRunResults`; owns `selectedWT`/`selectedMutant`, `activeId`, per-track pose derivation, full-screen `'wt' \| 'mutant' \| 'both'`. |

Wiring notes for `RunViewerPage`:

- `mutatedResidue = { chain, index }` from `structures.mutated_residue`, passed to
  **both** `StructurePanel`s (same position, different structure).
- Pre-select the mutant resistance pocket via `pocket.pocket_id === mutant_pocket.pocket_id`.
- `active = entries.find(e => e.molecule_id === activeId)`;
  `mutantPose = active?.mutant_pose_pdb ?? null`, `wtPose = active?.wt_pose_pdb ?? null` —
  the same per-track routing `ComplexViewerPage` already does with
  `monomerPose`/`dimerPose`.

---

## Dependencies & touch points

| Sibling spec | Relationship |
|---|---|
| [`00-overview.md`](00-overview.md) | Stage 6 / last build step; the demo surface for the WT/mutant re-orientation. |
| [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md) | Consumes `POST /runs`, `GET /runs/:id`, `GET /runs`; renders `Run` lifecycle, `parsed_mutation`, and round state. |
| [`02-mutagenesis.md`](02-mutagenesis.md) | WT/mutant structure URLs, provenance badges (source, engine, ΔΔG), and the mutated residue (chain/index/names) for the highlight — via `getRunStructures`. |
| [`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md) | `mutant_pocket` (which pocket to pre-select/highlight) + `pocket_delta` (`PocketDeltaPanel`) — via `getRunPocket`. |
| [`04-generation-loop.md`](04-generation-loop.md) | Produces per-round molecules with `round`/`parent_id`; drives lineage and board growth. |
| [`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md) | `wt_score`/`mutant_score`/`margin`/`QED`/`fitness` and per-track poses — the board's data via `getRunResults`. |
| [`08-persistence-and-queue.md`](08-persistence-and-queue.md) | Owns the run read-endpoints' backing store and whether progress is SSE or polled. |

**Code touch points (this feature):**
- **New:** `RunViewerPage` (route `/runs/:id`), `RunLauncherForm`, `RoundStepper`,
  `MoleculeLineage`, `PocketDeltaPanel`, and route registration (`/runs`, `/runs/:id`).
- **Change:** `MolstarViewer.tsx` (+`mutatedResidue` prop), `useMolstar.ts`
  (+mutated-residue emphasis, new `MUTATED_RESIDUE` color constant), `lib/api.ts`
  (run + selectivity types and client fns).
- **Evolve:** `DockedResults.tsx` → `SelectivityBoard` (margin/fitness ranking,
  new columns, `molecule_id` identity).
- **Reuse unchanged:** `StructurePanel` + `PoseCaption` (inside the page),
  `DockingPanel.tsx`, `buildResidueLoci`, the full-screen + resize logic, the pose
  overlay and pLDDT coloring.

---

## Acceptance criteria

- [ ] `/runs/:id` renders two Mol\* viewers labeled **Wild type** and **Mutant**,
      loading `structures.wt.url` / `structures.mutant.url`, reusing
      `MolstarViewer`/`useMolstar` with no change to the pose-overlay or pLDDT paths.
- [ ] The WT panel shows a provenance badge (`experimental_holo` / `apo` /
      `alphafold`, with PDB id + resolution when experimental); the mutant panel
      shows the engine and ΔΔG when non-null (chip hidden when `null`).
- [ ] The mutated residue is emphasized on **both** structures in a color clearly
      distinct from the green pocket overlay and the red/yellow pose, at the same
      `(chain, index)` on each track.
- [ ] The mutated-residue emphasis survives a representation switch and a structure
      reload, and is not repainted by pLDDT recoloring (skipped like the pose).
- [ ] Selecting a mutant pocket highlights it in the mutant viewer only; the mutant
      resistance pocket (`mutant_pocket.pocket_id`) is pre-selected on load.
- [ ] The board ranks by `fitness` (fallback `margin`) **descending** — largest
      positive margin is #1 — and shows `wt_score`, `mutant_score`, `margin`, `QED`
      per molecule; negative margin is visually flagged.
- [ ] Clicking a board row (or a lineage node) loads that molecule's mutant pose
      into the mutant viewer, and its WT pose into the WT viewer when present.
- [ ] `RunLauncherForm` posts `{ uniprot_id, mutation, site_hint? }` to `/runs`,
      routes to `/runs/:id` on `201`, and renders the `400` mutation-parse reason
      inline.
- [ ] `RoundStepper` reflects live `status` + `round` + `round_state` via
      `watchRun` (SSE or polling), and shows the `error` on `failed`.
- [ ] `MoleculeLineage` groups molecules by round and draws parent→child edges from
      `parent_id`; round-0 seeds have no parent.
- [ ] The existing monomer/dimer explorer at `/structure/:id` is unaffected (the
      WT/mutant view is additive; no `source_type`/`Track` field is overloaded).

---

## Open questions / risks

- **Read-endpoint decomposition.** This UI needs resolved structures, pocket
  context, and scored results per run. Whether these are three endpoints
  (`/runs/:id/structures|pocket|results`) or one composite `GET /runs/:id/view` is
  a backend call ([`08`](08-persistence-and-queue.md)); the frontend only fixes the
  shapes. Over-fetching a large `results` payload (with pose PDBs inline) may argue
  for a summary list + a lazy per-molecule pose fetch.
- **SSE vs. poll.** [`01`](01-run-lifecycle-and-mutation.md) defines `GET /runs/:id`
  but no events endpoint. Polling is the safe MVP; an SSE `/runs/:id/events` is the
  upgrade (the codebase already uses `EventSource` for `/search`). `watchRun` hides
  the choice behind one interface.
- **Pose payload size.** Carrying `wt_pose_pdb` + `mutant_pose_pdb` for every scored
  molecule across many rounds can bloat `getRunResults`. Consider poses fetched
  on-demand when a row is selected, keyed by `molecule_id` + `track`.
- **`ComparisonResult` field naming.** [`03`](03-dual-pocket-analysis-and-delta.md)
  may keep `monomer`/`dimer` JSON keys while they carry WT/mutant values. If those
  keys are surfaced anywhere in this UI, rename in lockstep to avoid a WT-labeled
  value reading from a `dimer` field.
- **Mutated residue outside every pocket.** If the mutation is allosteric/surface
  and no pocket lists it, `mutant_pocket` may be empty; the highlight should still
  render (it is a residue, not a pocket), but the board/delta panel need an empty
  state.
- **Re-point vs. new page.** Re-pointing `ComplexViewerPage` is less code but
  couples the resistance view to the legacy explorer's state; a dedicated
  `RunViewerPage` keeps concerns separate at the cost of some duplication of the
  page shell. Recommendation: new page, shared leaf components.
- **Numbering trust.** The highlight assumes WT and mutant share residue indices
  (guaranteed by [`02`](02-mutagenesis.md) Path A / index-preserving mutagenesis).
  If a refold path ever renumbers, the single `(chain, index)` for both tracks
  breaks and the contract needs a per-track residue.
