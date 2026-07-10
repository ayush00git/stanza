#!/usr/bin/env python3
"""Measure how far a proposed molecule sits from the published prior art.

Part of the Stanza pipeline. The generator is steered with a prompt that names
the clinical KRAS switch-II inhibitors and tells the model not to re-derive
them. This script checks whether it listened -- it is the audit, not the filter,
and it never drops anything.

Three independent axes, because a molecule can be new on one and stale on another:

  1. scaffold    Bemis-Murcko framework: strip every side chain, keep the ring
                 systems and the linkers between them. Two molecules with the
                 same framework are the same design decorated differently. We
                 report both the exact framework and the *generic* one (every
                 atom -> carbon, every bond -> single), which sees through an
                 N-for-C swap that leaves the topology untouched.

  2. similarity  Max Tanimoto over ECFP4 (Morgan radius 2, 2048 bits) against
                 every reference. This is the medicinal chemist's rule of thumb:
                 >= 0.70 and you are looking at an analogue, not a new series.
                 Below ~0.40 the compounds share little beyond gross features.

  3. identity    InChIKey skeleton (first block, connectivity only). An exact
                 hit means the model reproduced a known drug outright, possibly
                 with different stereochemistry.

None of this measures whether the molecule is any *good*. Novelty and
feasibility are independent: a compound can be unlike anything published and
still be unable to reach the cysteine. Read this next to the covalent
feasibility, never instead of it.

Usage:
    echo '{"query": [{"id": "m1", "smiles": "..."}]}' | python3 novelty.py

Input is a single JSON object on stdin:
    {
      "query": [{"id": "...", "smiles": "..."}, ...],
      "reference": [{"name": "...", "smiles": "..."}, ...],  # optional
      "analogue_cutoff": 0.70                                # optional
    }

The reference set defaults to data/prior_art_kras_g12c.json, whose structures
come from PubChem by CID. Do not pass hand-typed SMILES: a plausible-looking
string for "sotorasib" that is not sotorasib makes every number below a lie.

On success a single JSON line is printed to stdout ({"molecules": [...]}) with
one record per query molecule, in input order, and the process exits 0.
Errors go to stderr with a non-zero exit code.
"""

import json
import os
import sys

from rdkit import Chem, RDLogger
from rdkit.Chem import rdFingerprintGenerator
from rdkit.Chem.Scaffolds import MurckoScaffold

RDLogger.DisableLog("rdApp.*")

# ECFP4. The literature's default for this comparison, and the basis for the
# 0.70 analogue cutoff -- change one and the other stops meaning anything.
FP_RADIUS = 2
FP_BITS = 2048

# Tanimoto at or above this against any reference: an analogue of that reference.
ANALOGUE_CUTOFF = 0.70

_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_REFERENCE = os.path.join(_ROOT, "data", "prior_art_kras_g12c.json")

_GEN = rdFingerprintGenerator.GetMorganGenerator(radius=FP_RADIUS, fpSize=FP_BITS)


def skeleton(mol):
    """InChIKey connectivity block -- identity ignoring stereo and salt form."""
    return Chem.MolToInchiKey(mol).split("-")[0]


def scaffolds(mol):
    """(exact Murcko framework, generic framework) as canonical SMILES.

    A molecule with no ring system has an empty framework; that is reported as
    "" rather than faked, since an acyclic compound shares a scaffold with
    nothing.
    """
    core = MurckoScaffold.GetScaffoldForMol(mol)
    if core is None or core.GetNumAtoms() == 0:
        return "", ""
    exact = Chem.MolToSmiles(core)
    try:
        generic = Chem.MolToSmiles(MurckoScaffold.MakeScaffoldGeneric(core))
    except Exception:  # noqa: BLE001 - a pathological core is not fatal here
        generic = ""
    return exact, generic


def load_reference(spec):
    """Reference set as [{name, mol, fp, skeleton, scaffold, generic}].

    `spec` is either an explicit list of {name, smiles} or None, in which case
    the curated PubChem-sourced file is read. A reference that will not parse is
    a bug in the data file, not a survivable condition -- the whole point of the
    file is that its structures are correct.
    """
    if spec is None:
        with open(DEFAULT_REFERENCE) as fh:
            spec = json.load(fh)["compounds"]

    refs = []
    for entry in spec:
        name = entry["name"]
        mol = Chem.MolFromSmiles(entry["smiles"])
        if mol is None:
            raise ValueError("reference {!r} has an unparseable SMILES".format(name))
        exact, generic = scaffolds(mol)
        refs.append(
            {
                "name": name,
                "fp": _GEN.GetFingerprint(mol),
                "skeleton": skeleton(mol),
                "scaffold": exact,
                "generic": generic,
            }
        )
    return refs


def assess(entry, refs, cutoff):
    from rdkit import DataStructs

    rec = {"id": entry.get("id", ""), "smiles": entry.get("smiles", "")}
    mol = Chem.MolFromSmiles(rec["smiles"])
    if mol is None:
        rec["verdict"] = "invalid_smiles"
        return rec

    exact, generic = scaffolds(mol)
    rec["scaffold"] = exact
    rec["scaffold_generic"] = generic

    sims = [DataStructs.TanimotoSimilarity(_GEN.GetFingerprint(mol), r["fp"]) for r in refs]
    best = max(range(len(refs)), key=lambda i: sims[i])
    rec["max_tanimoto"] = round(sims[best], 3)
    rec["nearest"] = refs[best]["name"]

    # Which references, if any, this molecule shares a framework with. The exact
    # test is the strong claim; the generic one catches a heteroatom swap that
    # leaves the ring topology identical.
    rec["scaffold_shared_with"] = [r["name"] for r in refs if exact and r["scaffold"] == exact]
    rec["topology_shared_with"] = [r["name"] for r in refs if generic and r["generic"] == generic]

    known = skeleton(mol)
    rec["known_compound"] = next((r["name"] for r in refs if r["skeleton"] == known), None)

    # Ordered most to least damning: being a known drug outranks being its
    # analogue, which outranks merely sharing its skeleton.
    if rec["known_compound"]:
        rec["verdict"] = "known_compound"
    elif rec["max_tanimoto"] >= cutoff:
        rec["verdict"] = "analogue"
    elif rec["scaffold_shared_with"]:
        rec["verdict"] = "known_scaffold"
    elif rec["topology_shared_with"]:
        rec["verdict"] = "known_topology"
    else:
        rec["verdict"] = "novel_scaffold"
    return rec


def main():
    data = json.load(sys.stdin)
    query = data.get("query") or []
    cutoff = data.get("analogue_cutoff", ANALOGUE_CUTOFF)
    refs = load_reference(data.get("reference"))

    molecules = [assess(q, refs, cutoff) for q in query]
    json.dump({"reference": [r["name"] for r in refs], "molecules": molecules}, sys.stdout)
    sys.stdout.write("\n")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 - top-level guard for a CLI tool
        print("error: {}".format(exc), file=sys.stderr)
        sys.exit(1)
