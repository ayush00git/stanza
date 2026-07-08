# Stanza — Dual WT/Mutant Pocket Analysis & Delta

Run pocket detection on **both** the wild-type and mutant structures, match the
pockets across the two tracks, and emit the **delta** — what the mutation changed
about the druggable site. That delta is the context the generation loop conditions
on. **Status: EXTEND** — re-point the existing monomer/dimer comparison at the
WT/mutant axis rather than rebuilding it.

---

## Goal

Produce, for one run, a compact description of the resistance pocket and how the
mutation reshaped it:

- Detect pockets on the **WT structure** and the **mutant structure** (same
  fpocket step, run twice).
- **Match** pockets across tracks by spatial overlap, and single out the pocket
  that contains the **mutated residue** — that is the pocket we design against.
- Compute a **pocket delta**: Δvolume, Δhydrophobicity, Δpolarity, the change in
  the key-residue set at/near the mutated position, and H-bond / contact partner
  changes.
- Serialize the mutant pocket + delta as the small JSON the
  [generation loop](06-generation-loop.md) (and the model it prompts) reads.

This is the hinge between structure and chemistry: everything downstream —
generation, dual-track docking, selectivity — is conditioned on *this pocket* and
*this delta*, not on the whole protein.

## Current state

The dual-track pocket machinery already exists; it just answers the wrong
question (oligomerization, not resistance).

- **`services/fpocket.go`** — `RunFpocket(structureURL)` downloads a structure,
  shells out to `fpocket`, parses `*_info.txt` (druggability score, volume, SASA,
  hydrophobicity, polarity) and each `pocket*_atm.pdb` (residue indices, names,
  chains, geometric center, per-residue B-factor → pLDDT). Returns
  `[]models.Pocket` sorted by druggability. Structure-agnostic already: it takes a
  URL, so it can run on a WT file and a mutant file with no change.
- **`services/plddt.go`** — attaches per-residue pLDDT and average pLDDT to each
  pocket (confidence, reused as-is).
- **`services/pocket_filter.go`** — `FilterInterfacePockets(...)` computes a
  per-residue delta and flags/ranks pockets; caps at `MaxPockets = 5`.
- **`services/pocket_compare.go`** — `ComparePockets(monomerPockets,
  dimerPockets, ...)` is the piece we re-orient. It already:
  - matches pockets across two tracks by 3D center distance
    (`distance3D`, `DistanceThreshold = 6.0` Å);
  - classifies each pocket as **conserved** (matched) vs **emergent** (unmatched);
  - accumulates per-track averages (score, volume, hydrophobicity, polarity) and
    a scalar delta (`DDGI = avgDimer − avgMonomer`);
  - builds `models.ComparisonResult` (`models/comparison.go`): summary metrics,
    pocket mapping counts, per-track druggability distributions, property
    changes, and stabilization stats.

What is missing for resistance: the compare is hardwired to `monomer`/`dimer`
labels; nothing knows about a **mutated residue**; there is no notion of *the*
resistance pocket; and it emits no per-mutation delta the generation loop can
consume. `models.Pocket.SourceType` is a free-form string (`"monomer"|"dimer"`)
— it takes `"wt"|"mutant"` with no schema change.

## Design

Re-orientation, not a rewrite. Three moves: run twice, match across tracks, emit
the delta.

### 1. Run fpocket on both tracks

The run gives us two structure files from
[mutagenesis](02-mutagenesis.md): the WT structure and the mutant structure
(same residue numbering — mutagenesis preserves indices, only the side chain at
the mutated position changes).

- Call `RunFpocket(wtURL)` → `wtPockets`; call `RunFpocket(mutantURL)` →
  `mutantPockets`. Run `plddt`/`pocket_filter` on each track exactly as today.
- Set `SourceType = "wt"` on the first, `"mutant"` on the second. No struct
  change — just new string values in the existing field.
- Because the two structures differ by a single side chain, pocket geometry is
  nearly identical away from the mutation; the interesting change is local. That
  is what makes spatial matching reliable and the delta meaningful.

### 2. Match pockets across WT ↔ mutant

Reuse the existing `distance3D` + `DistanceThreshold` matching from
`pocket_compare.go`, relabeled:

- For each WT pocket, find the nearest unmatched mutant pocket within
  `DistanceThreshold` (6.0 Å between centers). Matched → **conserved** (present in
  both). Unmatched WT pocket → **WT-only** (site the mutation *closed*). Unmatched
  mutant pocket → **emergent** (site the mutation *opened*). This is exactly the
  conserved / monomer-only / emergent logic already in `ComparePockets`, with the
  labels renamed.
- **The resistance pocket.** Independently of the greedy nearest-match, find the
  pocket **containing the mutated residue** on each track: the pocket whose
  `ResidueIndices` (on the target chain) includes the mutated position (fall back
  to the pocket whose center is nearest the mutated residue's Cα if no pocket
  literally lists it). The WT resistance pocket and the mutant resistance pocket
  are then paired directly — this pairing is authoritative and overrides the
  greedy match if they disagree. Everything downstream designs against the
  **mutant** resistance pocket while checking selectivity against the **WT** one.

### 3. The pocket delta

For the paired resistance pocket (WT vs mutant), compute the delta the loop
conditions on:

- **Δvolume** = `mutant.Volume − wt.Volume`; likewise **Δhydrophobicity** and
  **Δpolarity** from the existing `Pocket` fields. (Druggability Δ and per-track
  averages already fall out of `ComparisonResult`.)
- **Key-residue set change.** Extract key residues per track (see below), then
  diff the sets: residues **gained** (in mutant, not WT), **lost** (in WT, not
  mutant), and the **substitution at the mutated position** rendered as
  `"Thr790→Met790"` (WT residue name + index → mutant residue name + index, from
  `ResidueNames`/`ResidueIndices`).
- **H-bond / contact partner changes.** The mutated side chain adds or removes
  polar contacts inside the pocket. Derive contacts geometrically from pocket
  residue coordinates (donor/acceptor atoms within an H-bond distance cutoff of
  the mutated residue), and report partners **gained** and **lost**. Kept
  coarse-grained (residue-level pairs, not atom-level energetics) — enough to tell
  the model "lost the H-bond that the WT inhibitor relied on."
- **Effect summary.** A one-line human/model-readable string describing the net
  change (e.g. tighter/bulkier pocket, lost polar anchor). This is the free-text
  `effect` field the loop passes through to the prompt.

### Key-residue extraction

"Key residues" = the residues lining the resistance pocket, ranked so the loop
gets a short, meaningful list rather than the full lining:

- Start from the pocket's `ResidueIndices` / `ResidueNames` / `ResidueChains`
  (already parsed by `parsePocketAtoms`).
- Always include the **mutated residue**. Prioritize residues **near the mutated
  position** (small sequence/spatial neighborhood) and residues on the target
  chain. Rank the remainder by contribution to the pocket (proximity to center /
  contact count) and keep the top N (small, e.g. ~8–12).
- Emit as `RESNAME+INDEX` tokens (e.g. `"Met790"`, `"Leu718"`) so they are stable
  identifiers the model and the frontend both understand.

### Where it plugs in

Add a resistance-oriented entry point (e.g. `ComparePocketsWTMutant` alongside the
existing `ComparePockets`, or a thin wrapper that renames the axis and adds the
mutated-residue handling). It returns the reused `models.ComparisonResult` for the
dual view **plus** the new `MutantPocketContext` (below) that the loop consumes.
The mutated residue (chain + index + WT/mutant names) comes in from the
[run + mutation input](01-run-lifecycle-and-mutation.md) via
[mutagenesis](02-mutagenesis.md).

## Contracts

### Reused as-is

- **`models.Pocket`** (`models/pocket.go`) — unchanged. `SourceType` now also
  takes `"wt"` and `"mutant"`. Its `Volume`, `Hydrophobicity`, `Polarity`,
  `ResidueIndices`, `ResidueNames`, `ResidueChains`, `Center`, `AvgPLDDT` supply
  every delta input.
- **`models.ComparisonResult`** (`models/comparison.go`) — reused for the dual
  pocket view. Read its `conserved`/`emergent` groupings as WT↔mutant
  conserved / emergent, and `DDGI` as the mean druggability shift WT→mutant.
  (Field names keep the `monomer`/`dimer` spelling for now; they carry WT/mutant
  values. Renaming the JSON keys is an [open question](#open-questions--risks).)

### New Go types

```go
// MutantPocketContext is the resistance-pocket payload the generation loop reads.
// Built by the WT/mutant compare; serialized into the run's pocket step.
type MutantPocketContext struct {
    MutantPocket MutantPocket `json:"mutant_pocket"`
    PocketDelta  PocketDelta  `json:"pocket_delta"`
}

// MutantPocket is the pocket we design against (mutant track).
type MutantPocket struct {
    KeyResidues    []string   `json:"key_residues"`   // ["Met790","Leu718",...]
    Volume         float64    `json:"volume"`
    Hydrophobicity float64    `json:"hydrophobicity"`
    Polarity       float64    `json:"polarity,omitempty"`
    Center         [3]float64 `json:"center"`         // docking box seed (track 04)
    PocketID       int        `json:"pocket_id"`
}

// PocketDelta is what the mutation changed, WT → mutant, for the resistance pocket.
type PocketDelta struct {
    Changed          []string `json:"changed"`            // ["Thr790→Met790"]
    ResiduesGained   []string `json:"residues_gained,omitempty"`
    ResiduesLost     []string `json:"residues_lost,omitempty"`
    DVolume          float64  `json:"d_volume"`
    DHydrophobicity  float64  `json:"d_hydrophobicity"`
    DPolarity        float64  `json:"d_polarity"`
    HBondsGained     []string `json:"hbonds_gained,omitempty"` // partner residues
    HBondsLost       []string `json:"hbonds_lost,omitempty"`
    Effect           string   `json:"effect"`             // one-line summary
}
```

### JSON the generation loop consumes

The loop (and the model it prompts) reads exactly this shape:

```json
{
  "mutant_pocket": {
    "key_residues": ["Met790", "Leu718", "Leu844", "Gln791", "Phe856"],
    "volume": 812.4,
    "hydrophobicity": 34.1,
    "center": [22.6, -4.1, 55.9],
    "pocket_id": 2
  },
  "pocket_delta": {
    "changed": ["Thr790→Met790"],
    "residues_gained": [],
    "residues_lost": [],
    "d_volume": -37.5,
    "d_hydrophobicity": 6.2,
    "d_polarity": -3.4,
    "hbonds_lost": ["Thr790 side-chain OH → inhibitor"],
    "hbonds_gained": [],
    "effect": "Gatekeeper T790M: bulkier, more hydrophobic pocket; the polar anchor at 790 is gone. Favor hydrophobic contact at 790; do not rely on an H-bond there."
  }
}
```

Field intent: `mutant_pocket` tells the model *what to bind*; `pocket_delta` tells
it *why the WT drug fails and what to exploit*. `center` seeds the docking box in
[dual-track docking](04-dual-track-docking-and-caching.md); `key_residues` and
`changed` are surfaced in the [resistance UI](09-frontend-resistance-ui.md).

## Dependencies & touch points

- **Input** — [`02-mutagenesis.md`](02-mutagenesis.md) provides the WT + mutant
  structure files (same numbering) and the mutated residue (chain, index, WT name,
  mutant name), threaded from
  [`01-run-lifecycle-and-mutation.md`](01-run-lifecycle-and-mutation.md).
- **Consumer** — [`06-generation-loop.md`](06-generation-loop.md) reads
  `MutantPocketContext` as its conditioning context each iteration.
- **Reuse (unchanged)** — `services/fpocket.go`, `services/plddt.go`,
  `services/pocket_filter.go`, `models/pocket.go`.
- **Extend** — `services/pocket_compare.go` (add the WT/mutant entry point +
  mutated-residue matching + delta); `models/comparison.go` (add
  `MutantPocketContext` / `MutantPocket` / `PocketDelta`; optionally rename
  `monomer`/`dimer` JSON keys).
- **Downstream also touches** — `Center` feeds
  [`04-dual-track-docking-and-caching.md`](04-dual-track-docking-and-caching.md);
  `key_residues` + `changed` feed
  [`09-frontend-resistance-ui.md`](09-frontend-resistance-ui.md) (mutated-residue
  highlight, delta panel).

## Acceptance criteria

- fpocket runs on both the WT and mutant structures for a run; pockets carry
  `SourceType = "wt"` / `"mutant"` respectively.
- Pockets are matched across tracks; each pocket is labeled conserved / WT-only /
  emergent, consistent with the existing distance-threshold logic.
- The **resistance pocket** (the one containing the mutated residue) is identified
  on both tracks and paired, with a documented fallback when no pocket lists the
  residue.
- A `PocketDelta` is produced with: the substitution string (`"Thr790→Met790"`),
  Δvolume / Δhydrophobicity / Δpolarity, residue-set gained/lost, H-bond
  partner gained/lost, and a non-empty `effect` string.
- `key_residues` always includes the mutated residue and is a short ranked list
  (≤ ~12) of `RESNAME+INDEX` tokens.
- The endpoint/step serializes the exact `mutant_pocket` + `pocket_delta` JSON
  above, and the generation loop can consume it without post-processing.
- `models.ComparisonResult` is still emitted for the dual view (no regression to
  the existing compare for callers that want the full breakdown).
- Deterministic output for a fixed (WT, mutant, mutation) input.

## Open questions / risks

- **Matching when the pocket changes a lot.** If the mutation opens/closes a
  pocket, the 6.0 Å center threshold may mis-pair or leave the resistance pocket
  unmatched. The mutated-residue anchor is the safeguard, but pick a principled
  fallback (nearest-center vs Cα distance) and a max radius.
- **fpocket sensitivity to a single side chain.** A one-residue change can shift
  fpocket's pocket boundaries or split/merge a pocket, inflating the apparent
  delta. May need to intersect pocket linings or restrict the delta to a shell
  around the mutated residue to keep Δvolume honest.
- **H-bond inference is heuristic.** Residue-level, geometry-only contact
  detection (no protonation, no ligand) will miss cases. Scope: a hint for the
  prompt, not a quantitative energy. Decide the distance cutoff and whether to
  consider main-chain vs side-chain donors/acceptors.
- **`key_residues` count and ranking.** How many, and ranked by what
  (center proximity, contact count, sequence neighborhood of the mutation)?
  Too many dilutes the prompt; too few drops real contacts.
- **`ComparisonResult` naming.** Keeping `monomer`/`dimer` JSON keys while they
  carry WT/mutant values is a readability trap. Renaming is cleaner but touches
  the frontend — sequence it with [`09`](09-frontend-resistance-ui.md).
- **No mutated residue in any pocket.** If the mutation sits outside every
  detected pocket (allosteric / surface), define what the resistance pocket and
  delta mean — possibly no design target, only a report.
- **Numbering drift.** The whole delta assumes WT and mutant share residue
  indices. If mutagenesis ever renumbers (insertions/deletions), matching by index
  breaks; assert equal numbering upstream.
