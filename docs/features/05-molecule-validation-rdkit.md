# Stanza — Molecule Validation & Drug-Likeness

**Status: DONE** · A fast RDKit pre-filter that parses, canonicalizes, and
dedupes proposed SMILES, then drops invalid / duplicate / non-drug-like
molecules before they reach the expensive docking stage.

Implemented as `scripts/validate.py` (RDKit; reads a JSON batch on stdin, emits
one verdict per input molecule) behind the Go wrapper `services.ValidateSMILES`
(`services/validation.go`), mirroring the `scripts/mutate.py` shell-out pattern.
Stage 6's `GenerateCandidates` runs every Claude proposal through it and forwards
only the `kept` molecules to `run.Candidates`. The design below is what shipped.

---

## Goal

Every molecule that survives to docking should be **chemically valid**,
**unique across the run**, and **drug-like enough to be worth the dock budget**.
Docking a molecule into both the WT and mutant pockets (see
[`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md))
is the most expensive step in the loop. Spending that budget on unparseable
strings, duplicates, or oversized non-drug-like structures is pure waste.

This feature is the gate that stops that waste: a cheap, deterministic filter
that runs on a raw batch of SMILES and returns only the molecules that are
allowed downstream — each tagged with the drug-likeness numbers
(`qed`, `ro5_pass`, `sa_score`) that later feed selectivity ranking in
[`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md).

## Current state

Built as of Stage 5 — see the status note above. The context that shaped it:

- **No cheminformatics in Go.** The Go backend (Gin, with `uuid`) has no RDKit,
  no QED / Ro5 / SA scoring, and no SMILES sanitization. Go has no practical
  cheminformatics library, so this logic lives in Python + RDKit and Go shells
  out to it — the first Python cheminformatics step in the system.
- **Molecules are library fragments, not generated.** Today a molecule is a
  `models.Fragment{ChemblID, Name, SMILES, MolWeight, LogP, Similarity}` pulled
  from a fixed ChEMBL lookup per pocket. Those SMILES are pre-vetted by their
  source, so there is no validation step. Once
  [`06-generation-loop.md`](06-generation-loop.md) starts producing *novel*
  SMILES, they are untrusted and must be validated.
- **The only async pattern is Go shelling out to CLIs.** `services/jobs.go`
  runs docking by launching `obabel` / `vina` subprocesses against an in-memory
  job store (`JobStore`). There is no Python worker and no persistent queue yet;
  both arrive with [`08-persistence-and-queue.md`](08-persistence-and-queue.md).

So this spec introduces the **first Python component** in the system.

## Design

Validation lives in a small **Python + RDKit** module — the natural home,
since RDKit is the reference cheminformatics toolkit and Go has no equivalent.
It is invoked as a **worker** (see *Dependencies & touch points*) that takes a
batch of raw SMILES for one run and returns a per-molecule verdict.

The filter is intentionally **fast and deterministic**: no 3D embedding, no
network calls, no docking. Given the same input it must produce the same output
so results can be cached and the loop is reproducible.

### Pipeline steps

Process the batch molecule by molecule, but keep a **run-scoped seen-set** so
duplicates are caught across the whole batch (and, once persisted, across the
whole run).

1. **Parse + sanitize.** `Chem.MolFromSmiles(smiles)`. A `None` result, or a
   molecule that fails RDKit sanitization (bad valence, unparseable aromaticity,
   etc.), is rejected immediately with `drop_reason = "invalid_smiles"`. This
   also filters empty strings and obvious junk from the generator.
2. **Canonicalize.** Emit the RDKit canonical SMILES
   (`Chem.MolToSmiles(mol)`) and compute the **InChIKey**
   (`Chem.MolToInchiKey(mol)`). The canonical form is what every downstream
   stage stores and keys on, so two syntactically different spellings of the
   same molecule collapse to one identity.
3. **Dedupe across the run.** If the canonical SMILES (or InChIKey — see
   *Dedupe strategy*) has already been seen in this run, reject with
   `drop_reason = "duplicate"`. Otherwise add it to the seen-set. This is the
   single most valuable filter for dock budget: generators re-propose the same
   scaffolds constantly.
4. **Compute drug-likeness.**
   - **QED** — `rdkit.Chem.QED.qed(mol)`, a 0–1 desirability score.
   - **Lipinski Ro5** — MW, LogP (Crippen), H-bond donors, H-bond acceptors;
     `ro5_pass` is true when **at most one** rule is violated (the standard
     "no more than one violation" reading of the Rule of Five).
   - **SA score** (optional) — synthetic-accessibility 1 (easy) – 10 (hard),
     via the standard Ertl/Schuffenhauer `sascorer` contributed script bundled
     with RDKit. Optional because it needs the fragment-contribution data file;
     if unavailable, emit `sa_score = null` and skip the SA threshold rather
     than failing the molecule.
5. **Apply drug-likeness thresholds.** A molecule that parses and is unique but
   fails the thresholds below is still **dropped** (kept out of docking) with a
   specific `drop_reason`, but it is *valid* — `valid = true`. Downstream code
   distinguishes "not a real molecule" from "a real molecule we chose not to
   dock".
6. **Return verdicts.** Emit one record per input molecule: the kept ones with
   their flags, the rejected ones with a `drop_reason`. Order and count mirror
   the input so the generation loop can reconcile.

### Dedupe strategy

- **Within a batch:** a plain in-memory set keyed on the **canonical SMILES**
  is enough and is exact for a single run.
- **Across the run / persisted:** key on **InChIKey**, which normalizes
  tautomer/protonation representation better than raw canonical SMILES and is a
  fixed-length, index-friendly column. The `molecules` table in
  [`08-persistence-and-queue.md`](08-persistence-and-queue.md) should carry a
  **unique constraint on `(run_id, inchikey)`** so dedupe survives worker
  restarts and is enforced by the database, not just process memory.
- Stereochemistry: keep it (do **not** strip stereo) — enantiomers can dock and
  score differently, so they are legitimately distinct molecules.

### Filters + default thresholds

Defaults are deliberately permissive — this is a *pre-filter*, not the final
selection (that is selectivity ranking in
[`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md)).
All thresholds are **configurable per run**; the values below are the defaults.

| Filter | Default | `drop_reason` when failed | Notes |
|---|---|---|---|
| Parse + sanitize | must pass | `invalid_smiles` | Hard gate; `valid = false`. |
| Duplicate (run-scoped) | must be new | `duplicate` | Canonical SMILES / InChIKey. |
| Molecular weight | 150 – 500 Da | `mw_out_of_range` | Lower bound drops trivial fragments. |
| QED | ≥ 0.30 | `low_qed` | Permissive; tune per target. |
| Lipinski Ro5 | ≤ 1 violation | `ro5_fail` | Standard Ro5 reading. |
| SA score | ≤ 6.0 | `hard_to_synthesize` | Skipped if `sascorer` unavailable. |

If a molecule fails several rules, report the **first** failure in a fixed
priority order (`invalid_smiles` → `duplicate` → `mw_out_of_range` →
`ro5_fail` → `low_qed` → `hard_to_synthesize`) so `drop_reason` is
deterministic and easy to aggregate ("why did we lose molecules this round?").

## Contracts

### Input

A batch of proposed SMILES for one run:

```json
{
  "run_id": "run_9f3c1a2b",
  "smiles": [
    "CC(=O)Oc1ccccc1C(=O)O",
    "c1ccccc1",
    "not_a_molecule",
    "CC(=O)Oc1ccccc1C(=O)O"
  ]
}
```

### Output

One record per input molecule, kept order aligned to the input:

```json
{
  "run_id": "run_9f3c1a2b",
  "molecules": [
    {
      "smiles": "CC(=O)Oc1ccccc1C(=O)O",
      "inchikey": "BSYNRYMUTXBXSQ-UHFFFAOYSA-N",
      "valid": true,
      "kept": true,
      "qed": 0.55,
      "ro5_pass": true,
      "sa_score": 1.8,
      "drop_reason": null
    },
    {
      "smiles": "c1ccccc1",
      "inchikey": "UHOVQNZJYSORNB-UHFFFAOYSA-N",
      "valid": true,
      "kept": false,
      "qed": 0.44,
      "ro5_pass": true,
      "sa_score": 1.0,
      "drop_reason": "mw_out_of_range"
    },
    {
      "smiles": "not_a_molecule",
      "inchikey": null,
      "valid": false,
      "kept": false,
      "qed": null,
      "ro5_pass": null,
      "sa_score": null,
      "drop_reason": "invalid_smiles"
    },
    {
      "smiles": "CC(=O)Oc1ccccc1C(=O)O",
      "inchikey": "BSYNRYMUTXBXSQ-UHFFFAOYSA-N",
      "valid": true,
      "kept": false,
      "qed": 0.55,
      "ro5_pass": true,
      "sa_score": 1.8,
      "drop_reason": "duplicate"
    }
  ]
}
```

Field notes:

- `smiles` is always the **canonical** form when the molecule parsed; for an
  invalid molecule it echoes the raw input so the caller can trace it.
- `valid` — did it parse + sanitize. `kept` — did it survive *every* filter and
  is therefore eligible for docking. `kept` implies `valid`.
- `qed`, `ro5_pass`, `sa_score` are computed for every *valid* molecule (even
  dropped ones), so they can be surfaced or logged; `sa_score` is `null` when
  the SA scorer is unavailable.
- `drop_reason` is `null` for kept molecules, otherwise the single deterministic
  reason from the priority order above.

### Worker signature

Python entry point, framework-agnostic so it can sit behind either a CLI
(matching today's shell-out pattern in `services/jobs.go`) or the queue in
[`08-persistence-and-queue.md`](08-persistence-and-queue.md):

```python
def validate_batch(
    smiles: list[str],
    run_id: str,
    *,
    thresholds: Thresholds | None = None,
    seen_inchikeys: set[str] | None = None,   # run-scoped dedupe carried in
) -> list[MoleculeVerdict]:
    ...
```

- `seen_inchikeys` lets the caller pass identities already persisted for the run
  so dedupe spans batches, not just this call.
- `thresholds` overrides the defaults per run; `None` uses the table above.
- Returns a list of `MoleculeVerdict` in input order (serialized to the JSON
  above).

## Dependencies & touch points

- **[`08-persistence-and-queue.md`](08-persistence-and-queue.md)** — provides
  the worker/queue pattern this module plugs into and the `molecules` table
  where verdicts are written (`smiles`, `inchikey`, `qed`, `ro5_pass`,
  `sa_score`, `valid`, `kept`, `drop_reason`, with the
  `UNIQUE(run_id, inchikey)` constraint that backs cross-batch dedupe). This is
  the first Python worker in the system.
- **[`06-generation-loop.md`](06-generation-loop.md)** — the consumer. The loop
  emits raw candidate SMILES, calls this validator, and only forwards `kept`
  molecules to docking; `drop_reason` aggregates become feedback ("too many
  low_qed — ask for smaller, more drug-like scaffolds next round").
- **[`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md)**
  — the protected stage. This filter runs **before** docking so the WT+mutant
  dock budget is spent only on unique, drug-like molecules.
- **[`07-selectivity-scoring-and-ranking.md`](07-selectivity-scoring-and-ranking.md)**
  — consumes `qed`, `ro5_pass`, and `sa_score` as inputs to the final ranking
  alongside the selectivity margin.
- **Existing Go code** — `services/jobs.go` (`DockingResult` / `JobStore`) is
  the reference for the async job contract this worker mirrors; the
  `models.Fragment{SMILES, MolWeight, LogP, ...}` shape shows the fields
  already carried per molecule that this stage now computes properly instead of
  trusting a library.
- **New dependency** — a Python runtime with `rdkit` available on the compute
  host (alongside the existing `fpocket` / `obabel` / `vina` CLIs). The
  optional `sascorer` script + fragment data ships with the RDKit
  Contrib directory.

## Acceptance criteria

- Given a batch of SMILES and a `run_id`, the worker returns exactly one verdict
  per input molecule, in input order.
- Unparseable / non-sanitizable SMILES are returned with `valid = false`,
  `kept = false`, and `drop_reason = "invalid_smiles"`; they never reach
  docking.
- Two different spellings of the same molecule (e.g. `OCC` vs `CCO`) produce the
  **same** canonical SMILES and InChIKey; the second is `kept = false` with
  `drop_reason = "duplicate"`.
- Every *valid* molecule has numeric `qed` and boolean `ro5_pass`; `sa_score` is
  numeric when the SA scorer is available and `null` otherwise, and its absence
  never fails an otherwise-passing molecule.
- A valid, unique molecule outside a threshold (MW / QED / Ro5 / SA) is
  `valid = true`, `kept = false`, with the correct single `drop_reason` chosen
  by the fixed priority order.
- The step is **deterministic**: the same input batch yields byte-identical
  verdicts across runs.
- Thresholds are overridable per run without code changes; defaults match the
  table above.
- Cross-batch dedupe: identities passed in via `seen_inchikeys` (or persisted
  under `UNIQUE(run_id, inchikey)`) are rejected as duplicates in later batches.

## Open questions / risks

- **Tautomer / protonation canonicalization.** InChIKey helps but is not a
  perfect tautomer canonicalizer. Do we run RDKit's tautomer standardization
  before hashing? It is slower and can be surprising; deferred until dedupe
  false-negatives are observed in practice.
- **Salt / mixture stripping.** Generated SMILES may include counter-ions or
  dot-separated fragments. Should we strip to the largest organic component
  before validating, or reject multi-component SMILES outright? Leaning toward
  strip-largest-fragment, but it changes identity — needs a decision before it
  affects dedupe.
- **Threshold calibration.** The defaults are generic drug-likeness values;
  different targets (e.g. fragment-like vs. lead-like campaigns) want different
  MW/QED bands. Per-run overrides cover this, but good starting presets per
  campaign type are still an open design item.
- **SA scorer availability.** The `sascorer` Contrib script and its data file
  are not guaranteed on every install. Treating SA as optional avoids hard
  failures, but a run silently losing the SA filter should at least be surfaced
  in the run log.
- **Python ↔ Go boundary.** This is the first Python in a Go system. The
  transport (CLI subprocess mirroring `services/jobs.go`, a long-lived worker
  reading the queue, or a small local HTTP service) is decided in
  [`08-persistence-and-queue.md`](08-persistence-and-queue.md); this spec only
  fixes the JSON contract so either transport is a drop-in.
- **Stereochemistry vs. generator noise.** Keeping stereo means a generator that
  emits many stereo variants of one scaffold inflates the kept set. Acceptable
  for now (they dock differently), but worth watching as a dock-budget risk.
