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

- **Structures** — collapsible/minimisable section with two Mol* viewers
  (monomer + dimer), loaded straight from remote `.cif` URLs. Representation
  switcher (spheres/cartoon/surface/ball&stick), pLDDT legend.
- **Binding sites** — `BindingSitesPanel` runs fpocket on load and lists pockets
  in two columns (dimer / monomer), **sorted by druggability** with the top
  pocket marked, every fpocket metric labelled.
- **Selection → highlight** — clicking a pocket overpaints its residues in
  **green** and focuses the camera in the matching viewer. Highlight is
  **persistent per structure**: it stays until another pocket in that same
  structure is clicked; each structure keeps its own selection independently.
- **Docking** — the most-recently selected pocket drives a `DockingPanel`
  (fragment list + submit/poll docking jobs), rendered below the analysis.

### Mol* wrapper

- `components/viewer/MolstarViewer.tsx` — one canvas + overlays.
- `components/viewer/useMolstar.ts` — plugin lifecycle, structure load,
  representation, and residue highlight (green overpaint `0x16a34a` +
  `focusLoci`). Assumes fpocket `residue_indices` == mmCIF `auth_seq_id` (1-based).

### API client (`lib/api.ts`)

Typed wrappers over every endpoint: `searchComplexes` (SSE), `getComplex`,
`getBindingSites`, `getChemblFragments`, `submitDock`, `getDockStatus`,
`checkHealth`. Types mirror the Go models. `lib/plddt.ts` holds the shared
pLDDT colour bands.

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
- Docking may shell out to an external tool at runtime.
- **Rebuild from source on deploy** — the checked-in `stanza` binary can be
  stale (predated the `/dock` routes).

### Verification status

- `go build ./...` and `tsc -b && vite build` pass.
- End-to-end smoke test passed: `/health`, `/complex/P00533` (EGFR),
  `/complex/P00533/binding-sites` (fpocket ran, 104 pockets), and
  `/dock/status` all responded correctly.

---

## Known gaps / next steps

- Pockets aren't yet reflected back into the 3D view beyond the selected-pocket
  highlight (e.g. colour-by-pocket overview).
- The Mol* bundle is ~3.3 MB (lazy-loaded); consider code-splitting.
- Docking UX is minimal (list + submit/poll); results aren't visualised in 3D.
- Residue-index → `auth_seq_id` mapping assumes 1-based PDB numbering; revisit
  if any fpocket output uses 0-based indices.
