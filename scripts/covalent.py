#!/usr/bin/env python3
"""Covalent-docking helper for the Stanza mutant track.

AutoDock Vina scores non-covalently: it cannot see the covalent bond a warhead
forms to a cysteine thiol, which is the entire selectivity mechanism of covalent
inhibitors (sotorasib/adagrasib bond KRAS Cys12; wild-type Gly12 has no thiol, so
the drug physically cannot attach). This script supplies the geometry Vina is
blind to:

  detect  — does a SMILES carry a cysteine-reactive warhead, and of what class.
  assess  — given a Vina multi-mode docked pose and the target cysteine, find the
            docked mode whose warhead reactive carbon comes closest to the Cys SG
            (scanned across ALL modes, since the covalently-competent orientation
            is often not the top-scoring non-covalent pose), and optionally write
            the tethered covalent-complex pose (warhead bonded to SG).

The Go side turns the reported reach distance into a covalent credit and applies
it to the mutant score; this script does only chemistry and geometry.

Both subcommands print a single JSON line to stdout and exit 0 on success. On a
handled failure they print JSON with an "error" field and exit non-zero.

Usage:
    python3 covalent.py detect --smiles "C=CC(=O)N1CCN(CC1)c1ncccn1"
    python3 covalent.py assess --smiles "<smi>" --pose docked.pdbqt \
        --receptor mutant.pdb --chain A --resnum 12 [--tether-out pose.pdb]
"""
import argparse
import json
import math
import os
import subprocess
import sys
import tempfile

from rdkit import Chem
from rdkit.Chem import AllChem
from rdkit.Geometry import Point3D
from rdkit import RDLogger

RDLogger.DisableLog("rdApp.*")

# Ideal thioether S–C bond length (Å) for the formed adduct.
SC_BOND = 1.81

# Cysteine-reactive warheads. For every pattern, match index 0 is the electrophilic
# carbon that forms the new C–S bond to the thiol; for Michael acceptors index 1 is
# the α-carbon (which gains a hydrogen on addition). "mech" drives tether building:
# "michael" turns the C=C/C#C into a single/double bond and adds an α-H; "sn2"
# removes the leaving-group halogen (index 1 in that pattern is the halogen).
WARHEADS = [
    ("acrylamide",      "[CX3H2]=[CX3H1][CX3](=O)[#7,#8]",              "michael"),
    ("vinyl_sulfonamide", "[CX3H2]=[CX3H1][SX4](=O)(=O)[#7]",          "michael"),
    ("cyanoacrylamide", "[CX3H2]=[CX3]([CX2]#[NX1])[CX3](=O)[#7]",     "michael"),
    ("propiolamide",    "[CX2H1]#[CX2][CX3](=O)[#7]",                  "michael_yne"),
    ("haloacetamide",   "[CX4H2]([F,Cl,Br,I])[CX3](=O)[#7]",           "sn2"),
]
_COMPILED = [(name, Chem.MolFromSmarts(sm), mech) for name, sm, mech in WARHEADS]


def detect_warhead(mol):
    """Return (name, mech, (beta_idx, alpha_idx)) for the first matching warhead, or
    (None, None, None). For sn2 the second index is the leaving-group atom."""
    for name, patt, mech in _COMPILED:
        ms = mol.GetSubstructMatches(patt)
        if ms:
            return name, mech, (ms[0][0], ms[0][1])
    return None, None, None


def cys_atoms(pdb_path, chain, resnum):
    """Extract CA/CB/SG coordinates of (chain, resnum) from a PDB file."""
    want = {"CA": None, "CB": None, "SG": None}
    with open(pdb_path) as fh:
        for line in fh:
            if not line.startswith(("ATOM", "HETATM")):
                continue
            if line[21] != chain:
                continue
            try:
                if int(line[22:26]) != resnum:
                    continue
            except ValueError:
                continue
            name = line[12:16].strip()
            if name in want:
                want[name] = (float(line[30:38]), float(line[38:46]), float(line[46:54]))
    return want


def split_models(pose_path):
    """Split a Vina multi-model PDBQT into per-mode PDB files via OpenBabel; return
    their paths."""
    workdir = tempfile.mkdtemp(prefix="covpose-")
    base = os.path.join(workdir, "m.pdb")
    subprocess.run(["obabel", pose_path, "-O", base, "-m"],
                   capture_output=True, text=True)
    out, i = [], 1
    while True:
        p = os.path.join(workdir, f"m{i}.pdb")
        if not os.path.exists(p):
            break
        out.append(p)
        i += 1
    return out


def reactive_c_coord(template, pose_pdb, beta_template_idx):
    """Coordinate of the warhead reactive carbon in a docked-pose PDB, mapped via the
    ligand template, or None if the pose cannot be matched."""
    docked = Chem.MolFromPDBFile(pose_pdb, removeHs=False, sanitize=False)
    if docked is None:
        return None
    try:
        docked = AllChem.AssignBondOrdersFromTemplate(template, docked)
    except Exception:
        return None
    for _, patt, _ in _COMPILED:
        ms = docked.GetSubstructMatches(patt)
        if ms:
            p = docked.GetConformer().GetAtomPosition(ms[0][0])
            return (p.x, p.y, p.z)
    return None


def scan_reach(smiles, pose_path, sg):
    """Across all docked modes, return (min reactive-C→SG distance, best-mode PDB path)."""
    template = Chem.MolFromSmiles(smiles)
    _, _, idx = detect_warhead(template)
    if idx is None:
        return None, None
    best_d, best_pose = None, None
    for pose_pdb in split_models(pose_path):
        rc = reactive_c_coord(template, pose_pdb, idx[0])
        if rc is None:
            continue
        d = math.dist(rc, sg)
        if best_d is None or d < best_d:
            best_d, best_pose = d, pose_pdb
    return best_d, best_pose


def build_tether(smiles, mech, best_pose_pdb, cys, out_pdb):
    """Form the covalent bond from the best docked pose: bond the warhead reactive
    carbon to Cys SG and constrained-minimize toward the docked coordinates with
    CA/CB/SG frozen, so the good pocket pose is preserved while the bond closes.
    Writes the ligand-only tethered pose to out_pdb. Returns the achieved S–C
    distance, or None if the tether could not be built."""
    template = Chem.MolFromSmiles(smiles)
    _, _, tidx = detect_warhead(template)
    docked = Chem.MolFromPDBFile(best_pose_pdb, removeHs=False, sanitize=False)
    if docked is None:
        return None
    try:
        docked = AllChem.AssignBondOrdersFromTemplate(template, docked)
    except Exception:
        return None
    dm = Chem.AddHs(docked, addCoords=True)
    ms = detect_warhead(dm)[2]
    if ms is None:
        return None
    beta_d, second_d = ms

    stub = Chem.AddHs(Chem.MolFromSmiles("CCS"))
    AllChem.EmbedMolecule(stub, randomSeed=1)
    ca_i, cb_i, sg_i = 0, 1, 2
    sconf = stub.GetConformer()
    sconf.SetAtomPosition(ca_i, Point3D(*cys["CA"]))
    sconf.SetAtomPosition(cb_i, Point3D(*cys["CB"]))
    sconf.SetAtomPosition(sg_i, Point3D(*cys["SG"]))

    rw = Chem.RWMol(Chem.CombineMols(stub, dm))
    off = stub.GetNumAtoms()
    beta_c = beta_d + off
    sg_atom = rw.GetAtomWithIdx(sg_i)
    h_del = [nb.GetIdx() for nb in sg_atom.GetNeighbors() if nb.GetAtomicNum() == 1][:1]

    if mech in ("michael", "michael_yne"):
        alpha_c = second_d + off
        bond = rw.GetBondBetweenAtoms(beta_c, alpha_c)
        bond.SetBondType(Chem.BondType.SINGLE if mech == "michael" else Chem.BondType.DOUBLE)
        rw.AddBond(sg_i, beta_c, Chem.BondType.SINGLE)
        for hi in sorted(h_del, reverse=True):
            rw.RemoveAtom(hi)
        shift = lambda i: i - sum(1 for r in h_del if r < i)
        ca_i, cb_i, sg_i, beta_c, alpha_c = (shift(ca_i), shift(cb_i), shift(sg_i),
                                             shift(beta_c), shift(alpha_c))
        newH = rw.AddAtom(Chem.Atom(1))
        rw.AddBond(alpha_c, newH, Chem.BondType.SINGLE)
    elif mech == "sn2":
        halogen = second_d + off
        rw.AddBond(sg_i, beta_c, Chem.BondType.SINGLE)
        drop = sorted(set(h_del) | {halogen}, reverse=True)
        for hi in drop:
            rw.RemoveAtom(hi)
        shift = lambda i: i - sum(1 for r in drop if r < i)
        ca_i, cb_i, sg_i, beta_c = shift(ca_i), shift(cb_i), shift(sg_i), shift(beta_c)
    else:
        return None

    m = rw.GetMol()
    try:
        Chem.SanitizeMol(m)
    except Exception:
        return None

    props = AllChem.MMFFGetMoleculeProperties(m)
    if props is None:
        return None
    ff = AllChem.MMFFGetMoleculeForceField(m, props)
    for idx in (ca_i, cb_i, sg_i):
        ff.AddFixedPoint(idx)
    for i in range(m.GetNumAtoms()):
        if i in (ca_i, cb_i, sg_i) or m.GetAtomWithIdx(i).GetAtomicNum() == 1:
            continue
        ff.MMFFAddPositionConstraint(i, 0.5, 50.0)
    ff.Minimize(maxIts=1000)

    conf = m.GetConformer()
    sc = math.dist(list(conf.GetAtomPosition(sg_i)), list(conf.GetAtomPosition(beta_c)))

    drop = {ca_i, cb_i, sg_i}
    for a in m.GetAtoms():
        if a.GetAtomicNum() == 1 and a.GetNeighbors()[0].GetIdx() in (ca_i, cb_i):
            drop.add(a.GetIdx())
    em = Chem.RWMol(m)
    for idx in sorted(drop, reverse=True):
        em.RemoveAtom(idx)
    with open(out_pdb, "w") as fh:
        fh.write(Chem.MolToPDBBlock(em.GetMol()))
    return sc


def cmd_detect(args):
    mol = Chem.MolFromSmiles(args.smiles)
    if mol is None:
        print(json.dumps({"error": "invalid SMILES"}))
        return 1
    name, mech, idx = detect_warhead(mol)
    print(json.dumps({"has_warhead": name is not None,
                      "warhead_type": name or ""}))
    return 0


def cmd_assess(args):
    mol = Chem.MolFromSmiles(args.smiles)
    if mol is None:
        print(json.dumps({"error": "invalid SMILES"}))
        return 1
    name, mech, idx = detect_warhead(mol)
    result = {"has_warhead": name is not None, "warhead_type": name or "",
              "reach_distance": None, "bond_distance": 0.0, "tether_written": False}
    if name is None:
        print(json.dumps(result))
        return 0

    cys = cys_atoms(args.receptor, args.chain, args.resnum)
    if cys["SG"] is None:
        result["error"] = f"no SG for {args.chain}{args.resnum} in receptor"
        print(json.dumps(result))
        return 0  # not a hard failure: caller treats missing reach as non-covalent

    reach, best_pose = scan_reach(args.smiles, args.pose, cys["SG"])
    if reach is None:
        print(json.dumps(result))
        return 0
    result["reach_distance"] = round(reach, 3)

    if args.tether_out and cys["CA"] and cys["CB"]:
        try:
            sc = build_tether(args.smiles, mech, best_pose, cys, args.tether_out)
            if sc is not None:
                result["bond_distance"] = round(sc, 3)
                result["tether_written"] = True
        except Exception as exc:  # tether pose is best-effort; never fail the assess
            result["tether_error"] = str(exc)[:200]

    print(json.dumps(result))
    return 0


def main():
    ap = argparse.ArgumentParser(description="Stanza covalent-docking helper")
    sub = ap.add_subparsers(dest="cmd", required=True)

    d = sub.add_parser("detect")
    d.add_argument("--smiles", required=True)
    d.set_defaults(func=cmd_detect)

    a = sub.add_parser("assess")
    a.add_argument("--smiles", required=True)
    a.add_argument("--pose", required=True, help="Vina multi-mode docked PDBQT")
    a.add_argument("--receptor", required=True, help="mutant PDB (for the cysteine)")
    a.add_argument("--chain", required=True)
    a.add_argument("--resnum", required=True, type=int)
    a.add_argument("--tether-out", default="", help="write the tethered pose here")
    a.set_defaults(func=cmd_assess)

    args = ap.parse_args()
    try:
        sys.exit(args.func(args))
    except Exception as exc:  # noqa: BLE001 - top-level CLI guard
        print(json.dumps({"error": str(exc)[:300]}))
        sys.exit(1)


if __name__ == "__main__":
    main()
