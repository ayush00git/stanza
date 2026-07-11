# Installation

Stanza is a Go server that shells out to Python (RDKit, PDBFixer/OpenMM) and to three
scientific binaries (AutoDock Vina, OpenBabel, fpocket), with a React frontend and an
optional Postgres database. This guide sets up all of it.

The versions below are the ones the project is verified against. Newer point releases are
generally fine; the scientific binaries are the ones worth pinning if you hit trouble.

| Component | Verified version | Required? |
|---|---|---|
| Go | 1.25+ | yes (server) |
| Python | 3.11+ (tested on 3.14) | yes (helper scripts) |
| RDKit | 2026.03 | yes |
| OpenMM | 8.5 | yes (mutagenesis) |
| PDBFixer | current | yes (mutagenesis) |
| AutoDock Vina | 1.2.7 | yes (docking) |
| OpenBabel | 3.1.1 | yes (ligand/receptor prep) |
| fpocket | current | yes (pocket detection) |
| Node.js / npm | 22 / 10 | yes (frontend) |
| PostgreSQL | 14+ (tested on 18) | optional (falls back to in-memory) |

## 1. The scientific stack (recommended: conda)

RDKit, OpenMM, PDBFixer, OpenBabel and Vina all live on `conda-forge`, and fpocket on
`bioconda`, so one environment is the least painful way to get a consistent toolchain:

```bash
conda create -n stanza -c conda-forge -c bioconda \
    python=3.12 rdkit openmm pdbfixer openbabel vina fpocket
conda activate stanza
```

If you prefer native installs, each tool builds independently (Vina and fpocket ship
release binaries; OpenBabel is in most package managers). Whatever route you take, verify:

```bash
vina --version          # AutoDock Vina v1.2.7
obabel -V               # Open Babel 3.1.1
fpocket -h              # prints usage
python3 -c "import rdkit, openmm, pdbfixer; print('scientific stack OK')"
```

If you did **not** use the conda environment above, install the Python packages directly:

```bash
pip install -r scripts/requirements.txt      # rdkit, openmm, pdbfixer
```

The synthetic-accessibility score is an optional RDKit Contrib script. If it is not
importable the pipeline simply reports `sa_score = null`; nothing else is affected.

## 2. The Go server

```bash
go build ./...      # or: go run .
```

Go modules are fetched automatically on first build. `go test ./...` runs the unit tests;
the tests that need RDKit or a database skip cleanly when those are absent.

## 3. The frontend

```bash
cd app
npm install
npm run build       # production build, or `npm run dev` for the Vite dev server
```

## 4. Environment

| Variable | Purpose | Required? |
|---|---|---|
| `ANTHROPIC_API_KEY` | molecule generation (Stage 6) | required to generate; docking works without it |
| `DATABASE_URL` | Postgres connection, e.g. `postgres://user:pass@127.0.0.1:5432/stanza?sslmode=disable` | optional |
| `STANZA_TEST_DATABASE_URL` | Postgres for the store integration tests | test-only |

Without `DATABASE_URL` the server runs in-memory: fully functional for a session, but run
history is lost on restart. With it set, the schema is created and migrated automatically on
startup, so you only need an empty database:

```bash
createdb stanza
export DATABASE_URL='postgres://<user>:<pass>@127.0.0.1:5432/stanza?sslmode=disable'
```

## 5. A writable `./tmp`, run from the repo root

fpocket writes into `./tmp` **relative to the server's working directory**, so run the
server from the repository root and make sure `./tmp` is writable. It is created
automatically if missing. The docking stages use the system temp dir and clean up after
themselves.

## 6. Run it

```bash
export ANTHROPIC_API_KEY=sk-ant-...            # for generation
export DATABASE_URL='postgres://...'           # optional
go run .                                        # serves on :8080
# in another shell:
cd app && npm run dev                           # frontend dev server
```

## 7. Verify the toolchain end to end

The reproducible controls exercise the whole scientific stack (fetch, mutate, dock,
covalent assessment) without needing the server, the API key, or a database. If this runs,
your Vina + OpenBabel + RDKit/PDBFixer install is sound:

```bash
scripts/controls/abl_t315i.sh                   # ~8 min; ends in PASS
```
