# Stanza — Structure Acquisition & Mutagenesis

Turn a target + a resistance mutation into a **paired pair of structures** —
`wt_structure` and `mutant_structure` — that every downstream stage docks and
compares against. Status: structure acquisition is **EXTEND** (today it is
AlphaFold-only); mutagenesis is **BUILD** (does not exist).

---

## Goal

Given a resolved target (UniProt accession) and a parsed point mutation (e.g.
`S315T`), produce two structures on disk:

- `wt_structure` — the wild-type pocket, preferring an experimental **holo**
  co-crystal when one exists, else the AlphaFold model.
- `mutant_structure` — the same fold with the mutation applied, yielding the
  altered pocket that resistance chemistry must target.

These two paths are the entry point of the **dual-track** convention: everything
after this stage runs once per track (see
[`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md),
[`06-dual-track-docking-and-caching.md`](06-dual-track-docking-and-caching.md)).
The mutation is not metadata — it is the physical difference between the two
inputs, and the selectivity margin (`wt_score − mutant_score`) is only meaningful
if these two structures differ in exactly the mutated site and nothing else.

## Current state

- `services/alphafold.go` acquires structure **only** from AlphaFold. Two paths
  exist: `FetchMonomerPrediction(uniprotID)` hits `/prediction/{id}` and returns
  a single model; `FetchComplexData(uniprotID)` hits the `/search?type=complex`
  endpoint and builds `.cif` model URLs for a **monomer** and a **dimer** plus
  their pLDDT. Both return **remote URLs to predicted models** — there is no
  experimental PDB path, no local structure file, and no notion of a holo
  (ligand-bound) pocket.
- `models/complex.go` (`Complex`) carries `MonomerStructURL` / `ComplexStructURL`
  and pLDDT fields — again, oriented on the monomer/dimer axis, not WT/mutant.
- Compute is done by **shelling out to CLIs**: `services/docking.go` runs
  `obabel` and `vina`; `services/fpocket.go` runs `fpocket`. There is **no
  Python, no database, no mutation code, and no mutagenesis tool** anywhere.
- Consequently there is no `wt_structure`, no `mutant_structure`, and no ΔΔG.

This spec adds (a) a holo-preferring acquisition step in front of the existing
AlphaFold fetch, and (b) a brand-new mutagenesis worker that follows the same
CLI shell-out pattern the codebase already uses.

## Design

### Prerequisite — structure acquisition (EXTEND `services/alphafold.go`)

The mutation is applied to *a* structure; which structure we start from sets the
ceiling on pocket realism. An experimental co-crystal with a bound ligand (a
**holo** structure) already shows the pocket in its druggable, closed
conformation — strictly better than a predicted apo model for docking. So
acquisition becomes a **preference ladder**, not a single fetch:

1. **Experimental holo (preferred).** Query the PDB for structures mapped to the
   UniProt accession that (i) cover the mutated residue position and (ii) contain
   a bound non-solvent ligand (a HETATM ligand that is not water/ion/buffer).
   Rank by resolution, then by ligand relevance, then by coverage of the pocket
   region. If one qualifies, download the PDB/mmCIF and use it as `wt_structure`.
2. **Experimental apo (fallback).** If no holo exists but an experimental
   structure covering the residue does, take the best-resolution apo structure.
3. **AlphaFold model (last resort).** Fall back to the existing
   `FetchMonomerPrediction` path and use the predicted `.cif` as `wt_structure`.

Selection rules and edge cases:

- **Residue-numbering safety.** The mutation position is defined in UniProt
  canonical numbering. Experimental PDB chains frequently use author numbering
  with offsets, gaps, or engineered constructs. Acquisition must resolve the
  mutated residue through the SIFTS UniProt↔PDB residue mapping and **reject any
  candidate where the mutated position is unmodeled** (missing density) or maps
  ambiguously. A holo structure that does not actually resolve the target residue
  is worse than an AlphaFold model that does.
- **Chain / assembly choice.** Pick a single chain that contains the residue and
  the pocket; record which chain was chosen so mutagenesis and pocket detection
  operate on the same chain.
- **Provenance.** Record how `wt_structure` was obtained (`experimental_holo` /
  `experimental_apo` / `alphafold`), the source ID (PDB ID or AlphaFold entry),
  resolution if experimental, and the bound-ligand code if holo. This feeds the
  frontend badge (see [`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md))
  and is a caching key.
- **Keep AlphaFold code intact.** The existing monomer/dimer fetch stays as the
  fallback branch; this stage wraps it rather than replacing it. The
  monomer/dimer distinction is orthogonal to WT/mutant and is not what we select
  on here.

Output of the prerequisite: a resolved `wt_structure` **local file path** (holo
structures must be downloaded, not left as URLs, because mutagenesis and
`fpocket` both need a file on disk) plus its provenance record.

### Mutagenesis (BUILD) — WT → mutant

Input is `wt_structure` (from above) and the **parsed mutation** produced by
[`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md)
(`{wildtype_aa, position, mutant_aa}`, already validated against the target
sequence). Mutagenesis writes `mutant_structure` — the same coordinates as WT
except the mutated sidechain (and, on the refold path, any local backbone
relaxation the mutation induces).

Two paths, chosen by a fidelity/speed trade-off:

#### Path A — fast sidechain mutagenesis (default)

Swap only the mutated residue's sidechain in place, keeping the backbone fixed,
via one CLI call. Two interchangeable engines:

- **PyMOL** `wizard mutagenesis` (headless, `pymol -cq` driving a short script):
  replaces the residue and picks the best-scoring rotamer. No stability number.
- **FoldX** `BuildModel` with an `individual_list` mutation file: repairs and
  rebuilds the sidechain **and returns a stability ΔΔG for free** (mutant − WT
  folding energy) in its output `.fxout`. Prefer FoldX when the ΔΔG is wanted.

Trade-offs: **fast** (seconds to a minute) and deterministic; keeps the pocket
backbone identical to WT so the WT↔mutant delta is a clean single-sidechain
change — ideal for the tight generation loop. But it **cannot model
backbone/loop rearrangement** that a bulky or charge-flipping substitution may
cause, so the pocket geometry can be optimistic for large mutations.

#### Path B — refold the mutant sequence (higher fidelity)

Apply the mutation to the **sequence**, then re-predict the full structure with a
structure predictor (e.g. Boltz-2 / Chai-1) — optionally co-folding the known
ligand to get a **holo-like** mutant pocket. This lets the backbone and nearby
loops relax around the substitution.

Trade-offs: **much higher pocket fidelity** for mutations that reshape the
pocket, and can produce a holo-like bound conformation; but **minutes to tens of
minutes** per run, needs a GPU, is non-deterministic across seeds, and — because
it re-predicts the *whole* fold — can introduce differences **away** from the
mutated site that muddy the WT↔mutant delta. Mitigate by aligning the refolded
mutant back onto `wt_structure` and confirming only the pocket region changed.

**Recommended default:** Path A (FoldX `BuildModel`, for the free ΔΔG) for the
interactive loop; expose Path B as an opt-in "high-fidelity" mode for a final
pass or when the mutation is known to be pocket-reshaping. The chosen path is
recorded on the mutant structure's provenance so a delta can be interpreted
correctly downstream.

#### Optional ΔΔG output

When the engine is FoldX, capture the folding **ΔΔG** (kcal/mol; positive =
destabilizing). It is a cheap, useful signal: a strongly destabilizing mutation
may indicate the residue is structurally load-bearing, and the number surfaces in
the UI and can inform ranking. ΔΔG is **optional** — the PyMOL path and the
refold path do not produce it, and downstream stages must treat it as nullable.

### Where this runs

Mutagenesis is a **compute job**, not an inline HTTP handler. It follows the
existing shell-out pattern (`services/docking.go`, `services/fpocket.go`): a Go
service function assembles inputs, `exec.Command`s the CLI, parses the output
file, and returns a struct. When the job queue lands, this becomes a queued
worker; see [`08-persistence-and-queue.md`](08-persistence-and-queue.md) for
where workers live and how jobs are enqueued/cached. Until then it can be called
synchronously in the run lifecycle, exactly like docking is today.

## Contracts

### Structure acquisition service

```go
// services/acquire.go (new) — wraps the existing alphafold.go fetch.
type StructureSource string

const (
    SourceExperimentalHolo StructureSource = "experimental_holo"
    SourceExperimentalApo  StructureSource = "experimental_apo"
    SourceAlphaFold        StructureSource = "alphafold"
)

type AcquiredStructure struct {
    Path       string          // local file path (.pdb/.cif) on disk
    Source     StructureSource // provenance
    SourceID   string          // PDB ID or AlphaFold entry ID
    Chain      string          // chain containing the mutated residue + pocket
    Resolution float64         // Å, 0 if predicted
    BoundLigand string         // HETATM code if holo, else ""
    PLDDT      float64         // 0 if experimental
}

// AcquireWTStructure resolves the best available WT structure for a target,
// guaranteeing the mutated position is modeled. Downloads to outDir.
func AcquireWTStructure(uniprotID string, mutationPos int, outDir string) (AcquiredStructure, error)
```

### Mutagenesis job (compute contract)

The core contract requested: **input `{wt_structure_path, mutation}` → output
`{mutant_structure_path, ddg?}`**.

```go
// services/mutagenesis.go (new)
type MutagenesisEngine string

const (
    EngineFoldX  MutagenesisEngine = "foldx"  // Path A, yields ΔΔG
    EnginePyMOL  MutagenesisEngine = "pymol"  // Path A, no ΔΔG
    EngineRefold MutagenesisEngine = "refold" // Path B, high fidelity
)

// Mutation mirrors the parsed struct from 01-run-lifecycle-and-mutation.md.
type Mutation struct {
    WildtypeAA byte // 'S'
    Position   int  // 315 (UniProt canonical numbering)
    MutantAA   byte // 'T'
    Chain      string
}

type MutagenesisJob struct {
    WTStructurePath string
    Mutation        Mutation
    Engine          MutagenesisEngine
    OutDir          string
}

type MutagenesisResult struct {
    MutantStructurePath string   // written mutant .pdb
    DDG                 *float64 // kcal/mol; nil unless engine == foldx
    Engine              MutagenesisEngine
    Status              string   // "done" | "failed"
    Error               string
}

func RunMutagenesis(job MutagenesisJob) (MutagenesisResult, error)
```

### CLI invocations (shell-out, same pattern as docking/fpocket)

```bash
# Path A — PyMOL sidechain mutagenesis (headless, scripted)
pymol -cq mutate.pml -- <wt_structure> <chain> <position> <new_resn> <out.pdb>
#   mutate.pml: cmd.wizard("mutagenesis"); refresh_wizard();
#               cmd.get_wizard().set_mode(new_resn); ... apply(); save(out)

# Path A — FoldX BuildModel (yields Dif_<pdb>.fxout with ΔΔG)
foldx --command=BuildModel \
      --pdb=<wt_basename>.pdb --pdb-dir=<dir> \
      --mutant-file=individual_list.txt --output-dir=<outDir>
#   individual_list.txt: "SA315T;"  (wildtype, chain, position, mutant)
#   ΔΔG parsed from the "total energy" column of Dif_*.fxout

# Path B — refold mutant sequence with a structure predictor (GPU)
#   1. derive mutant FASTA from target sequence with the substitution applied
#   2. <predictor> predict --fasta mutant.fasta [--ligand <smiles>] --out <dir>
#   3. superpose onto wt_structure; confirm only the pocket region changed
```

### Persisted / threaded fields

The two structure paths and their provenance thread through the run. When the run
model is defined in [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md),
it should carry (at minimum):

```go
WTStructurePath     string  // from AcquireWTStructure
WTSource            StructureSource
MutantStructurePath string  // from RunMutagenesis
MutagenesisEngine   MutagenesisEngine
DDG                 *float64 // nullable
```

## Dependencies & touch points

- **Extends:** `services/alphafold.go` — its fetch becomes the fallback branch of
  `AcquireWTStructure`; keep `FetchMonomerPrediction` / `FetchComplexData`.
- **New services:** `services/acquire.go`, `services/mutagenesis.go` — both follow
  the `exec.Command` shell-out pattern of `services/docking.go` /
  `services/fpocket.go`.
- **New CLI/runtime deps:** a PDB/SIFTS lookup for holo selection (HTTP, like the
  existing AlphaFold client); **PyMOL** and/or **FoldX** binaries for Path A; a
  structure predictor (Boltz-2 / Chai-1) + GPU for Path B. These are the first
  non-`obabel`/`vina`/`fpocket` compute tools — note the environment/install
  cost.
- **Consumes:** the parsed, sequence-validated mutation from
  [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md).
- **Feeds:** [`03-dual-pocket-analysis-and-delta.md`](03-dual-pocket-analysis-and-delta.md)
  (runs `fpocket` on both paths), [`06-dual-track-docking-and-caching.md`](06-dual-track-docking-and-caching.md)
  (docks into both), and the WT/mutant + ΔΔG surfaces in
  [`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md).
- **Runs under:** the worker/queue in
  [`08-persistence-and-queue.md`](08-persistence-and-queue.md) once it exists;
  synchronous call in the run lifecycle until then.
- **Models:** `models/complex.go` stays monomer/dimer-oriented; add the WT/mutant
  path + provenance fields to the run model (defined in `01`), not to `Complex`.

## Acceptance criteria

- Given a UniProt accession with a known holo co-crystal, `AcquireWTStructure`
  returns that experimental structure (`Source == experimental_holo`) with the
  mutated position confirmed modeled; given one without, it falls back cleanly to
  apo, then to AlphaFold, and records which.
- `AcquireWTStructure` **rejects** any candidate whose mutated residue is
  unmodeled or maps ambiguously through SIFTS, rather than silently mutating the
  wrong residue.
- `RunMutagenesis` with the FoldX engine produces a `mutant_structure` differing
  from WT **only** at the mutated residue (backbone unchanged) and returns a
  non-nil ΔΔG; with PyMOL it produces the mutant with `DDG == nil`.
- The refold engine produces a mutant pocket, superposed back onto WT, whose
  changes are localized to the pocket region (a sanity check catches gross
  whole-fold divergence).
- Output of the stage is a paired `{wt_structure_path, mutant_structure_path}` on
  disk that `fpocket` (unchanged) can run on for both tracks.
- Mutagenesis runs as a shell-out compute step consistent with the existing
  `docking.go` / `fpocket.go` pattern (assemble → `exec.Command` → parse file →
  return struct), with a `Status`/`Error` contract like `DockResult`.

## Open questions / risks

- **Numbering is the sharpest failure mode.** UniProt↔PDB residue mapping (SIFTS)
  offsets, insertion codes, and engineered constructs can silently place the
  mutation on the wrong residue. This must fail loudly, not quietly.
- **Holo selection is a mini ranking problem.** "Best" holo (resolution vs.
  ligand relevance vs. pocket coverage) needs a defined tie-break; a co-crystal
  with an irrelevant crystallization additive in the pocket may mislead.
- **Backbone-fixed optimism.** Path A cannot capture pocket collapse/expansion
  from bulky or charged substitutions; the resulting delta may understate the
  mutation's effect. When to auto-escalate to Path B is an open policy question.
- **Refold pollutes the delta.** Re-predicting the whole fold can change regions
  away from the mutation, inflating the WT↔mutant delta with prediction noise;
  the superpose-and-check mitigation needs a concrete threshold.
- **ΔΔG is FoldX-specific and approximate.** It is a useful heuristic, not ground
  truth; downstream must treat it as nullable and never as a hard gate.
- **Tooling cost.** PyMOL/FoldX licensing and install, plus a GPU for the refold
  path, are a real jump from the current `obabel`/`vina`/`fpocket`-only
  environment. Which engines to require vs. make optional is a deployment
  decision for [`08-persistence-and-queue.md`](08-persistence-and-queue.md).
```
