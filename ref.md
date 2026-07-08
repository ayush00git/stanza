# Stanza — Reference

Structure-guided drug discovery pipeline. Given a protein target, Stanza pulls
its sequence (UniProt) and predicted structures (AlphaFold monomer + dimer),
detects druggable pockets (fpocket), and supports fragment lookup (ChEMBL) and
docking. A React frontend presents search, a 3D structure viewer, and the
pocket analysis.

- **Backend:** Go + Gin, serves on `:8080`.
- **Frontend:** React 19 + TypeScript + Vite + Tailwind CSS v4, in `app/`.
- **Module:** `github.com/ayush00git/stanza`.

---

## Backend

### HTTP API (`main.go`)

| Method | Path | Handler | Purpose |
|--------|------|---------|---------|
| GET | `/health` | inline | Liveness — `{"status":"ok"}`. |
| GET | `/search?q=` | `SearchHandler` | **SSE stream** of `Complex` results. Events: `result` (one Complex), `done` (`{source:"live"\|"fallback"}`), `error`. Reviewed (Swiss-Prot) hits only; each enriched with AlphaFold confidence, streamed as it resolves. |
| GET | `/complex/:id` | `ComplexDetailHandler` | One `Complex` (id = UniProt accession or AlphaFold ID), including ChEMBL drug coverage. |
| GET | `/complex/:id/binding-sites` | `BindingSiteHandler` | Runs **fpocket** on monomer + dimer, cross-references pLDDT, flags interface pockets, returns `BindingSiteResult` with a monomer/dimer comparison. 1h in-memory cache. |
| GET | `/chembl?pocket_id=` | `ChemblHandler` | Candidate ChEMBL fragments for a pocket. Query params: `pocket_id` (required), optional `source_type`, `volume`, `hydrophobicity`, `polarity`. Returns `Fragment[]`. |
| POST | `/dock` | `DockSubmitHandler` | Submit an async docking job (JSON body: `pocket_id`, `source_type`, `ligand_smiles`, `protein_pdb_path`/`protein_pdb_id`). Returns **202** `{job_id}`. |
| GET | `/dock/status?id=` | `DockStatusHandler` | Poll a docking job (job id via **query** `?id=`). Returns `DockingResult` (status: pending → running → done/error). |

### Layout

- `handlers/` — `search.go`, `complex.go`, `bindingsites.go`, `pocket_store.go`, `chembl_handler.go`, `dock_handler.go`.
- `services/` — external data + compute:
  - `uniprot.go` (search + entry fetch), `alphafold.go` (structure/pLDDT lookup), `drug_coverage.go` (ChEMBL drug counts).
  - `fpocket.go` (runs fpocket, parses pockets), `plddt.go` (per-residue pLDDT), `pocket_filter.go` (interface detection), `pocket_compare.go` (monomer vs dimer).
  - `chembl.go` (fragment search), `docking.go` (docking run), `jobs.go` (async job store, UUID-keyed).
- `models/` — `complex.go` (`Complex`), `pocket.go` (`Pocket`, `Fragment`, `BindingSiteResult`, `ResidueConfidence`), `comparison.go` (`ComparisonResult`).
- `scoring/` — `who_pathogen.go` (WHO priority-pathogen classification).

### Key JSON shapes

- **`Complex`**: `uniprot_id`, `alphafold_id`, `protein_name`, `gene_name`, `organism`, `is_who_pathogen`, `disease_associations[]`, `monomer_plddt_avg`, `dimer_plddt_avg`, `disorder_delta`, `drug_count` (−1 = not fetched), `known_drug_names[]`, `monomer_structure_url`, `complex_structure_url`, `category`, `review_status`.
- **`Pocket`**: `pocket_id`, `druggability_score`, `volume`, `surface_area`, `depth`, `hydrophobicity`, `polarity`, `source_type` (`monomer`/`dimer`), `is_interface_pocket`, `is_conserved?`, `is_emergent?`, `avg_plddt`, `residue_indices[]`, `residue_chains[]`, `chains[]`, `center[3]`, `residue_confidences[]`.
- **`BindingSiteResult`**: `total_pockets`, `interface_pocket_count`, `pockets[]`, `monomer_total_pockets`, `monomer_pockets[]`, `comparison?`.

---

## Frontend (`app/`)

React 19 + TypeScript, Vite, Tailwind v4 (`@tailwindcss/vite`, tokens in
`src/index.css` under `@theme`). Fonts: Fraunces (display), Inter (UI), IBM Plex
Mono (data). Light editorial theme; palette = paper/ink + assay-blue accent +
the AlphaFold pLDDT confidence scale.

### Routing (`App.tsx`, react-router-dom)

- `/` → `pages/Home.tsx`
- `/structure/:id` → `pages/ComplexViewerPage.tsx` (lazy-loaded; pulls in Mol*)

Search state lives in `lib/searchStore.tsx` (a provider above the router) so
results survive navigating to a structure page and back.

### Home page

`Navbar` · `Hero` (target card with residue sequence + pLDDT strip) ·
`TargetSearch` (live SSE search → `ComplexCard` grid) · `Pipeline` ·
`Integrations` · `Footer`. `ComplexCard` links to `/structure/:id`.

### Structure viewer page (`/structure/:id`)

- **Independent loading** — the fast `getComplex` metadata and the slow fpocket
  `getBindingSites` analysis are fetched in parallel with independent state and
  error handling. The Mol* viewers render the moment the metadata resolves; the
  binding-site analysis never blocks the structures and streams in on its own
  status below.
- **Header** — target identity (gene/protein name, UniProt id) plus a WHO-pathogen
  badge when flagged, and a metadata strip beneath it (organism, known-drug
  count/"Undrugged", category) rendered only for the fields that are present.
- **Structures** — a prominent, collapsible (default-open) card with two Mol*
  viewers (monomer + dimer), loaded straight from remote `.cif` URLs.
  Representation switcher (spheres/cartoon/surface/ball&stick), pLDDT legend.
- **Binding-site analysis** — `BindingSitesPanel` lists pockets in two columns
  (dimer / monomer), **sorted by druggability** with the top pocket marked,
  every fpocket metric labelled.
- **Selection → highlight** — clicking a pocket overpaints its residues in
  **green** and focuses the camera in the matching viewer. Highlight is
  **persistent per structure**: it stays until another pocket in that same
  structure is clicked; each structure keeps its own selection independently.
- **Docking (inline in the pocket card)** — selecting a pocket expands its card
  to reveal a compact `DockingPanel` inline (no separate bottom section). It
  lists candidate ChEMBL fragments and submits/polls docking jobs per fragment.
- **Docked pose in 3D** — when a dock finishes, its `pose_pdb` (from
  `DockingResult`) is lifted up via an `onPose` callback
  (`DockingPanel → BindingSitesPanel → ComplexViewerPage`) and rendered in the
  matching structure's Mol* viewer as a **magenta ball-and-stick** ligand
  overlay, camera-framed. Pose state is kept per structure; a `PoseCaption`
  under the viewer shows the pocket/affinity/ChEMBL id and a "Clear" button that
  removes only the pose, leaving the protein and green highlight intact.

### Mol* wrapper

- `components/viewer/MolstarViewer.tsx` — one canvas + overlays. Takes a `pose`
  prop (raw PDB text of a docked ligand) threaded straight through to the hook.
- `components/viewer/useMolstar.ts` — plugin lifecycle, structure load,
  representation, and residue highlight (green overpaint `0x16a34a` +
  `focusLoci`). Assumes fpocket `residue_indices` == mmCIF `auth_seq_id` (1-based).
  Also handles the **docked-pose overlay**: the raw PDB is parsed via Mol*'s
  raw-data path (no network fetch) and drawn as ball-and-stick in a uniform
  magenta (`0xd946ef`), camera-framed. The pose subtree is tracked by cell ref
  so it can be removed on its own (or re-applied after a representation swap /
  structure reload) without disturbing the protein or the green highlight.

### Docking panel (`components/viewer/DockingPanel.tsx`)

Given a pocket, fetches candidate ChEMBL fragments and docks each one. Supports a
`compact` mode (used inline in the pocket card) and reports finished poses via
its `onPose` callback. Fragments load **progressively in pages of 6**
(`FRAGMENT_PAGE_SIZE`): an `IntersectionObserver` sentinel at the end of the
list auto-reveals the next page, with a "Load more" button as a fallback. Each
"Dock" button submits an async job and polls `/dock/status` every 2 s until it
reaches a terminal state, showing progress per fragment.

### API client (`lib/api.ts`)

Typed wrappers over every endpoint: `searchComplexes` (SSE), `getComplex`,
`getBindingSites`, `getChemblFragments`, `submitDock`, `getDockStatus`,
`checkHealth`. Types mirror the Go models (`DockingResult`, `Conformation`,
etc.). It also defines a UI-only `DockedPose` type (`pdb`, `source_type`,
`pocket_id`, `chembl_id?`, `binding_affinity?`) — the shape lifted out of the
docking panel so the page can overlay a finished pose in the right viewer.
`lib/plddt.ts` holds the shared pLDDT colour bands.

### Dev proxy (`vite.config.ts`)

Proxies `/health`, `/search`, `/complex`, `/chembl`, `/dock` → `http://localhost:8080`
so the browser stays same-origin (no CORS) and SSE streams through untouched.

---

## Running

```bash
# Backend (needs the `fpocket` binary on PATH; rebuild — do not reuse a stale binary)
go run .                     # serves :8080

# Frontend
cd app && npm install && npm run dev
```

### Runtime requirements / notes

- **`fpocket`** must be installed and on `PATH` (verified working with fpocket 4.0);
  the binding-sites endpoint shells out to it and writes scratch to `./tmp`
  (gitignored).
- Live network access to **UniProt** and **AlphaFold** is required for search,
  complex detail, and structure/pocket analysis.
- **Docking** shells out to external tools and requires both on `PATH`:
  - **OpenBabel** (`obabel`) — SMILES→3D ligand generation and PDB↔PDBQT
    conversion (receptor/ligand prep and pose PDBQT→PDB for visualisation).
  - **AutoDock Vina** (`vina`) — the docking run itself.
  Jobs run async in an in-memory `JobStore` (UUID-keyed, capped at 100, oldest
  evicted); each runs in a temp workspace that is removed on completion.
  End-to-end docking therefore requires both `vina` and `obabel` installed.
- **Rebuild from source on deploy** — the checked-in `stanza` binary can be
  stale (predated the `/dock` routes).

### Verification status

- `go build ./...` and `tsc -b && vite build` pass.
- End-to-end smoke test passed: `/health`, `/complex/P00533` (EGFR),
  `/complex/P00533/binding-sites` (fpocket ran), and `/complex` metadata all
  responded correctly.
- **Full docking round-trip verified** against EGFR (P00533): binding-site
  analysis → `POST /dock` of a ligand SMILES into a monomer pocket, using the
  AlphaFold monomer `.cif` as the receptor → OpenBabel receptor/ligand prep →
  Vina → `done` with a real binding affinity and a populated `pose_pdb`. The
  receptor is docked from the same structure fpocket detected the pocket in, so
  the pocket centre and receptor coordinates line up.

---

## Known gaps / next steps

- Pockets aren't yet reflected back into the 3D view beyond the selected-pocket
  highlight and the docked-pose overlay (e.g. a colour-by-pocket overview).
- The Mol* bundle is ~3.3 MB (lazy-loaded); consider code-splitting.
- Residue-index → `auth_seq_id` mapping assumes 1-based PDB numbering; revisit
  if any fpocket output uses 0-based indices.
