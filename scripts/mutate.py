#!/usr/bin/env python3
"""Apply a single side-chain point mutation to a protein structure via PDBFixer.

Part of the Stanza pipeline. A Go service shells out to this script once per
track: once for the wild-type residue and once for the mutant residue. Each
invocation applies exactly one point mutation at (chain, resnum) and rebuilds
ONLY the affected side chain -- missing loops are deliberately NOT modelled --
so both tracks emerge in the same normalized PDB format and coordinate frame.

Usage:
    python3 mutate.py --input <path.pdb|.cif> --chain <C> \
        --resnum <int> --to <RES3> --out <path.pdb> \
        [--keep-chain <C>] [--strip-het]

`--to` is a 3-letter residue name (e.g. GLY, CYS). On success a single JSON
line is printed to stdout and the process exits 0. Errors go to stderr with a
non-zero exit code (2 = residue not found, 1 = any other failure).

--keep-chain drops every other chain, and --strip-het removes ligands, ions and
water. Both are needed when the base structure is an experimental co-crystal:
the bound inhibitor otherwise occupies the very pocket we dock into.
"""

import argparse
import json
import sys

from pdbfixer import PDBFixer
from openmm.app import PDBFile


def load_fixer(input_path):
    """Load a structure into PDBFixer, honoring .cif vs .pdb input."""
    # PDBFixer needs an open file handle for mmCIF, but a filename for PDB.
    if input_path.endswith(".cif"):
        return PDBFixer(pdbxfile=open(input_path))
    return PDBFixer(filename=input_path)


def keep_only_chain(fixer, chain_id):
    """Drop every chain but chain_id."""
    drop = [i for i, c in enumerate(fixer.topology.chains()) if c.id != chain_id]
    if drop:
        fixer.removeChains(chainIndices=drop)


def write_structure(fixer, out_path):
    """Write the fixed structure, PRESERVING author residue numbering.

    OpenMM's PDB writer defaults to keepIds=False, which renumbers every residue
    sequentially from 1. On an AlphaFold model, already numbered from 1, that is a
    no-op and goes unnoticed. On an experimental structure it silently shifts the
    whole chain -- 6OIM opens with an expression-tag GLY at author residue 0, so
    Cys12 would land at 13 -- and every downstream lookup of "chain A residue 12",
    from the covalent thiol search to pocket residue matching, then reads the wrong
    residue. PDBFixer itself preserves the ids; only the writer discards them.
    """
    with open(out_path, "w") as handle:
        PDBFile.writeFile(fixer.topology, fixer.positions, handle, keepIds=True)


def find_residue_name(fixer, chain_id, resnum):
    """Return the current 3-letter residue name at (chain_id, resnum).

    residue.id is compared as a string. Returns None if not present.
    """
    resnum_str = str(resnum)
    for chain in fixer.topology.chains():
        if chain.id != chain_id:
            continue
        for residue in chain.residues():
            if str(residue.id) == resnum_str:
                return residue.name
    return None


def rebuild_side_chain(fixer):
    """Rebuild only the mutated side chain; never model missing loops.

    Clearing missingResidues after findMissingResidues() ensures addMissingAtoms
    fills in atoms for existing residues (the new side chain) without inserting
    whole residues for gaps in the chain.
    """
    fixer.findMissingResidues()
    fixer.missingResidues = {}
    fixer.findMissingAtoms()
    fixer.addMissingAtoms()


def main():
    parser = argparse.ArgumentParser(
        description="Apply a single side-chain point mutation via PDBFixer."
    )
    parser.add_argument("--input", required=True, help="Input structure (.pdb or .cif)")
    parser.add_argument("--chain", required=True, help="Chain ID (e.g. A)")
    parser.add_argument("--resnum", required=True, type=int, help="Residue number")
    parser.add_argument("--to", required=True, help="Target residue, 3-letter (e.g. CYS)")
    parser.add_argument("--out", required=True, help="Output PDB path")
    parser.add_argument("--keep-chain", default="", help="Drop every other chain")
    parser.add_argument(
        "--strip-het",
        action="store_true",
        help="Remove ligands, ions and water (needed for co-crystal bases)",
    )
    args = parser.parse_args()

    target = args.to.upper()

    # Load the structure.
    fixer = load_fixer(args.input)

    # Reduce to the biological unit we dock against, before anything else reads
    # residue positions: a co-crystal's bound inhibitor sits in the target pocket.
    if args.keep_chain:
        keep_only_chain(fixer, args.keep_chain)
    if args.strip_het:
        fixer.removeHeterogens(keepWater=False)

    # Determine the current residue name at the requested position.
    current = find_residue_name(fixer, args.chain, args.resnum)
    if current is None:
        print(
            "error: residue not found at chain {} resnum {}".format(
                args.chain, args.resnum
            ),
            file=sys.stderr,
        )
        sys.exit(2)

    # Apply the mutation unless it's a no-op. PDBFixer rejects a same-residue
    # "mutation", so we skip applyMutations but still normalize via the rebuild
    # steps below so both tracks share an identical output format/frame.
    if current != target:
        fixer.applyMutations(
            ["{}-{}-{}".format(current, args.resnum, target)], args.chain
        )

    # Rebuild only the mutated side chain (no loop modelling), then write.
    rebuild_side_chain(fixer)
    write_structure(fixer, args.out)

    # Verify the written output actually carries the target residue.
    verify = PDBFixer(filename=args.out)
    written = find_residue_name(verify, args.chain, args.resnum)
    if written != target:
        print(
            "error: verification failed, residue at chain {} resnum {} is {} "
            "(expected {})".format(args.chain, args.resnum, written, target),
            file=sys.stderr,
        )
        sys.exit(1)

    # Emit a single machine-readable JSON line for the caller.
    print(
        json.dumps(
            {
                "chain": args.chain,
                "resnum": args.resnum,
                "from": current,
                "to": target,
                "output": args.out,
            }
        )
    )
    sys.exit(0)


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        # Preserve explicit sys.exit() codes from main().
        raise
    except Exception as exc:  # noqa: BLE001 - top-level guard for a CLI tool
        print("error: {}".format(exc), file=sys.stderr)
        sys.exit(1)
