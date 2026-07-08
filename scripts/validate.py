#!/usr/bin/env python3
"""Validate and score a batch of SMILES with RDKit (Stanza Stage 5).

Part of the Stanza pipeline. A Go service shells out to this script once per
batch of proposed molecules: the generation loop hands over the raw SMILES
Claude produced, and this filter parses, canonicalizes, dedupes, and scores each
one, returning only the molecules worth spending the (expensive) docking budget
on. Go has no cheminformatics library, so this logic lives in Python + RDKit.

The filter is fast and deterministic -- no 3D embedding, no network, no docking
-- so the same batch always yields the same verdicts.

Usage:
    echo '{"run_id": "...", "smiles": ["CCO", ...]}' | python3 validate.py

Input is a single JSON object on stdin:
    {
      "run_id": "run_9f3c1a2b",
      "smiles": ["<SMILES>", ...],
      "seen_inchikeys": ["<InChIKey>", ...],   # optional, run-scoped dedupe
      "thresholds": { "qed_min": 0.30, ... }   # optional per-run overrides
    }

On success a single JSON line is printed to stdout ({"run_id", "molecules": [...]})
with one verdict per input molecule, in input order, and the process exits 0.
Errors go to stderr with a non-zero exit code.
"""

import json
import os
import sys

from rdkit import Chem, RDLogger
from rdkit.Chem import QED, Crippen, Descriptors, Lipinski

# Silence RDKit's parse/sanitize chatter -- an unparseable SMILES is an expected
# input here (invalid_smiles verdict), not something to spam stderr about.
RDLogger.DisableLog("rdApp.*")

# The SA (synthetic-accessibility) scorer is a Contrib script bundled with RDKit;
# it needs a fragment-contribution data file that is not guaranteed on every
# install. Treat it as optional: if it can't be imported, emit sa_score = null and
# skip the SA threshold rather than failing the whole batch.
_sascorer = None
try:
    from rdkit.Chem import RDConfig

    sys.path.append(os.path.join(RDConfig.RDContribDir, "SA_Score"))
    import sascorer as _sascorer  # type: ignore
except Exception:  # noqa: BLE001 - optional dependency; degrade gracefully
    _sascorer = None

# Defaults are deliberately permissive: this is a pre-filter, not final selection
# (that is selectivity ranking downstream). Overridable per run via "thresholds".
DEFAULTS = {
    "mw_min": 150.0,
    "mw_max": 500.0,
    "qed_min": 0.30,
    "ro5_max_violations": 1,
    "sa_max": 6.0,
}


def ro5_violations(mw, logp, hbd, hba):
    """Count Lipinski Rule-of-Five violations (MW>500, LogP>5, HBD>5, HBA>10)."""
    return sum((mw > 500.0, logp > 5.0, hbd > 5, hba > 10))


def validate_one(raw, thresholds, seen):
    """Return one verdict dict for a single raw SMILES string.

    `seen` is a mutable set of InChIKeys already observed this run; the first
    occurrence of a molecule is added to it, later ones are dropped as duplicates.
    Drop reasons follow a fixed priority so the output is deterministic:
    invalid_smiles -> duplicate -> mw_out_of_range -> ro5_fail -> low_qed ->
    hard_to_synthesize.
    """
    smi = (raw or "").strip()
    mol = Chem.MolFromSmiles(smi) if smi else None
    if mol is None:
        return {
            "smiles": smi,
            "inchikey": None,
            "valid": False,
            "kept": False,
            "qed": None,
            "ro5_pass": None,
            "sa_score": None,
            "mol_weight": None,
            "logp": None,
            "drop_reason": "invalid_smiles",
        }

    canonical = Chem.MolToSmiles(mol)
    inchikey = Chem.MolToInchiKey(mol)
    mw = Descriptors.MolWt(mol)
    logp = Crippen.MolLogP(mol)
    hbd = Lipinski.NumHDonors(mol)
    hba = Lipinski.NumHAcceptors(mol)
    qed = QED.qed(mol)
    ro5_pass = ro5_violations(mw, logp, hbd, hba) <= thresholds["ro5_max_violations"]

    sa = None
    if _sascorer is not None:
        try:
            sa = _sascorer.calculateScore(mol)
        except Exception:  # noqa: BLE001 - never let SA scoring sink a molecule
            sa = None

    rec = {
        "smiles": canonical,  # canonical form is what downstream stages key on
        "inchikey": inchikey,
        "valid": True,
        "qed": round(qed, 4),
        "ro5_pass": ro5_pass,
        "sa_score": (round(sa, 3) if sa is not None else None),
        "mol_weight": round(mw, 2),
        "logp": round(logp, 3),
    }

    # Drop reasons, in fixed priority order. A dropped molecule is still valid;
    # `kept` is what gates docking.
    if inchikey in seen:
        rec["kept"], rec["drop_reason"] = False, "duplicate"
        return rec
    seen.add(inchikey)

    if mw < thresholds["mw_min"] or mw > thresholds["mw_max"]:
        rec["kept"], rec["drop_reason"] = False, "mw_out_of_range"
    elif not ro5_pass:
        rec["kept"], rec["drop_reason"] = False, "ro5_fail"
    elif qed < thresholds["qed_min"]:
        rec["kept"], rec["drop_reason"] = False, "low_qed"
    elif sa is not None and sa > thresholds["sa_max"]:
        rec["kept"], rec["drop_reason"] = False, "hard_to_synthesize"
    else:
        rec["kept"], rec["drop_reason"] = True, None
    return rec


def main():
    data = json.load(sys.stdin)
    smiles = data.get("smiles") or []
    run_id = data.get("run_id", "")
    seen = set(data.get("seen_inchikeys") or [])
    thresholds = dict(DEFAULTS)
    thresholds.update(data.get("thresholds") or {})

    molecules = [validate_one(s, thresholds, seen) for s in smiles]
    json.dump({"run_id": run_id, "molecules": molecules}, sys.stdout)
    sys.stdout.write("\n")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 - top-level guard for a CLI tool
        print("error: {}".format(exc), file=sys.stderr)
        sys.exit(1)
