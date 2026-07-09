#!/usr/bin/env python3
"""Generate a deterministic 3D conformer for a ligand SMILES.

Part of the Stanza docking stage. This replaces `obabel --gen3d`, whose conformer
search is unseeded: three invocations on the same SMILES return three different
structures, with per-atom displacements of up to 17 A. Vina's --seed pins the docking
SEARCH but not the ligand it starts from, so the same molecule re-docked against the
same receptor gave different answers. That does not matter for the binding affinity,
which Vina re-optimises over torsions anyway, but it matters a great deal for the
covalent reach distance: the whole selectivity margin is a function of how close the
warhead lands to the thiol.

RDKit's ETKDG accepts a random seed, so the same SMILES yields the same conformer
forever. Output is SDF rather than PDB so that the bond orders travel with the
coordinates and OpenBabel never has to guess them when it writes the PDBQT.

Prints a single JSON line to stdout and exits 0 on success; on failure prints JSON
with an "error" field and exits 1.

Usage:
    python3 ligprep.py --smiles "C=CC(=O)N1CCNCC1" --out ligand_3D.sdf [--seed 42]
"""
import argparse
import json
import sys

from rdkit import Chem
from rdkit.Chem import AllChem
from rdkit import RDLogger

RDLogger.DisableLog("rdApp.*")

# Iterations for the MMFF cleanup of the embedded conformer. Deterministic: a plain
# gradient minimisation from a fixed starting point.
MMFF_ITERS = 500


def embed(mol, seed):
    """Embed a single conformer with ETKDG under a fixed seed, falling back to random
    starting coordinates for the cages and macrocycles ETKDG occasionally refuses."""
    params = AllChem.ETKDGv3()
    params.randomSeed = seed
    if AllChem.EmbedMolecule(mol, params) == 0:
        return True
    params.useRandomCoords = True
    return AllChem.EmbedMolecule(mol, params) == 0


def main():
    ap = argparse.ArgumentParser(description="Deterministic 3D ligand conformer")
    ap.add_argument("--smiles", required=True)
    ap.add_argument("--out", required=True, help="Output SDF path")
    ap.add_argument("--seed", type=int, default=42)
    args = ap.parse_args()

    mol = Chem.MolFromSmiles(args.smiles)
    if mol is None:
        print(json.dumps({"error": "invalid SMILES"}))
        return 1

    mol = Chem.AddHs(mol)
    if not embed(mol, args.seed):
        print(json.dumps({"error": "conformer embedding failed"}))
        return 1
    AllChem.MMFFOptimizeMolecule(mol, maxIters=MMFF_ITERS)

    writer = Chem.SDWriter(args.out)
    writer.write(mol)
    writer.close()

    print(json.dumps({"smiles": args.smiles, "seed": args.seed,
                      "atoms": mol.GetNumAtoms(), "output": args.out}))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # noqa: BLE001 - top-level CLI guard
        print(json.dumps({"error": str(exc)[:300]}))
        sys.exit(1)
