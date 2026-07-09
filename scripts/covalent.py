#!/usr/bin/env python3
"""Covalent-docking helper for the Stanza mutant track.

AutoDock Vina scores non-covalently: it cannot see the covalent bond a warhead
forms to a cysteine thiol, which is the entire selectivity mechanism of covalent
inhibitors (sotorasib/adagrasib bond KRAS Cys12; wild-type Gly12 has no thiol, so
the drug physically cannot attach). This script supplies the geometry Vina is
blind to:

  detect  — does a SMILES carry a cysteine-reactive warhead, and of what class.
  assess  — given a Vina multi-mode docked pose and the target cysteine, find the
            docked mode whose warhead electrophilic carbon comes closest to the Cys
            SG (scanned across ALL modes, since the covalently-competent orientation
            is often not the top-scoring non-covalent pose), and optionally write
            the tethered covalent-complex pose (warhead bonded to SG).

The Go side turns the reported reach distance into a covalent credit and applies
it to the mutant score; this script does only chemistry and geometry.

Both subcommands print a single JSON line to stdout and exit 0 on success. Every
assess result carries a `status` so the caller can tell "no warhead" apart from
"warhead out of reach" apart from "the pose could not be read" — collapsing those
into a single silent nil is how a broken measurement masquerades as a negative
result. On a handled failure they print JSON with an "error" field and exit
non-zero.

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

# Ideal thioether S–C bond length (Å) for the formed adduct, and the tolerance the
# minimised tether must land within to be reported as a real bond. A pose whose
# "bond" is 2.4 Å is not an adduct — it is a docked pose with a line drawn on it.
SC_BOND = 1.81
SC_BOND_TOL = 0.25

# Forming the bond genuinely moves the ligand: the non-covalent docked pose is not
# the covalent-adduct pose. These let the scaffold relax far enough to close the
# bond while still being pulled toward the docked geometry.
TETHER_MAX_DISPL = 2.0  # Å a heavy atom may drift from its docked position
TETHER_FORCE = 5.0      # kcal/mol/Å² restraint toward the docked position

# A tethered ligand heavy atom closer than this to a receptor heavy atom (other than
# the cysteine it bonds) is a steric clash, not a pose.
CLASH_CUTOFF = 2.0

# Cysteine-reactive warheads, most specific pattern first so the reported class is
# the informative one. For EVERY pattern, match index 0 is the electrophilic carbon
# that forms the new C–S bond, and index 1 is the α-carbon (Michael acceptors, which
# gains a hydrogen on addition) or the leaving-group halogen (SN2).
#
# For an α,β-unsaturated carbonyl the electrophile is the carbon NOT bonded to the
# electron-withdrawing group, so each Michael SMARTS is written C(β)=C(α)–EWG and the
# β carbon falls out at index 0. Substituted β-carbons are deliberately allowed:
# afatinib-style dimethylaminocrotonamides and β-aryl cyanoacrylamides are real
# warheads, and a pattern demanding a terminal [CX3H2] silently misses them.
WARHEADS = [
    ("acrylamide",          "[CX3H2]=[CX3H1][CX3](=O)[#7,#8]",            "michael"),
    ("cyanoacrylamide",     "[CX3]=[CX3]([CX2]#[NX1])[CX3](=O)[#7]",      "michael"),
    ("vinyl_sulfonamide",   "[CX3]=[CX3][SX4](=O)(=O)[#7]",               "michael"),
    ("propiolamide",        "[CX2]#[CX2][CX3](=O)[#7]",                   "michael_yne"),
    ("haloacetamide",       "[CX4]([F,Cl,Br,I])[CX3](=O)[#7]",            "sn2"),
    # Generic catch-all: any Michael-accepting α,β-unsaturated amide/ester.
    ("unsaturated_amide",   "[CX3]=[CX3][CX3](=O)[#7,#8]",                "michael"),
]
_COMPILED = [(name, Chem.MolFromSmarts(sm), mech) for name, sm, mech in WARHEADS]

# assess status values.
ST_NO_WARHEAD = "no_warhead"        # ligand carries no cysteine-reactive group
ST_NO_THIOL = "no_thiol"            # target residue has no SG (not a cysteine)
ST_UNREADABLE = "unreadable_pose"   # no docked mode could be mapped to the ligand
ST_MEASURED = "measured"            # reach_distance is valid; caller decides credit


def detect_warhead(mol):
    """Return (name, mech, (electrophile_idx, second_idx)) for the first matching
    warhead, or (None, None, None). The second index is the α-carbon for Michael
    acceptors and the leaving-group halogen for SN2."""
    for name, patt, mech in _COMPILED:
        ms = mol.GetSubstructMatches(patt)
        if ms:
            return name, mech, (ms[0][0], ms[0][1])
    return None, None, None


def warhead_pattern(name):
    """The compiled SMARTS for a warhead class, for mapping onto a docked pose."""
    for n, patt, _ in _COMPILED:
        if n == name:
            return patt
    return None


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


def receptor_heavy_atoms(pdb_path, skip_chain, skip_resnum):
    """Heavy-atom coordinates of the receptor, excluding the target residue (whose
    SG we bond to and whose CB necessarily sits within bonding distance)."""
    out = []
    with open(pdb_path) as fh:
        for line in fh:
            if not line.startswith(("ATOM", "HETATM")):
                continue
            element = line[76:78].strip() or line[12:16].strip()[:1]
            if element.upper() == "H":
                continue
            try:
                resnum = int(line[22:26])
            except ValueError:
                continue
            if line[21] == skip_chain and resnum == skip_resnum:
                continue
            out.append((float(line[30:38]), float(line[38:46]), float(line[46:54])))
    return out


def pose_modes(pose_path):
    """Read a Vina multi-mode docked PDBQT into a list of RDKit molecules, one per
    mode, via an OpenBabel SDF conversion.

    Why SDF rather than PDB: a PDB carries no bond orders, so RDKit must re-perceive
    them from geometry, which produces over-valent atoms on perfectly good poses (a
    five-bonded piperazine nitrogen) and makes the whole assessment fail silently.
    OpenBabel writes explicit bond blocks into SDF, and Vina preserves the ligand's
    atom order across every mode, so one conversion yields directly usable molecules.
    """
    workdir = tempfile.mkdtemp(prefix="covpose-")
    sdf = os.path.join(workdir, "modes.sdf")
    subprocess.run(["obabel", pose_path, "-osdf", "-O", sdf],
                   capture_output=True, text=True)
    if not os.path.exists(sdf):
        return []
    return [m for m in Chem.SDMolSupplier(sdf, removeHs=False) if m is not None]


def electrophile_indices(mol, template, e_template_idx):
    """Indices of the warhead electrophilic carbon in a docked-pose molecule, mapped
    through the ligand template. Symmetric molecules admit several matches, so every
    candidate is returned and the caller takes the closest."""
    out = []
    for match in mol.GetSubstructMatches(template):
        if e_template_idx < len(match):
            out.append(match[e_template_idx])
    return out


def scan_reach(template, e_template_idx, mols, sg):
    """Across all docked modes, return (min electrophile→SG distance, best mode mol),
    or (None, None) when no mode could be mapped onto the template."""
    best_d, best_mol = None, None
    for m in mols:
        conf = m.GetConformer()
        for idx in electrophile_indices(m, template, e_template_idx):
            p = conf.GetAtomPosition(idx)
            d = math.dist((p.x, p.y, p.z), sg)
            if best_d is None or d < best_d:
                best_d, best_mol = d, m
    return best_d, best_mol


def build_tether(template, tidx, mech, docked, cys, receptor_atoms, out_pdb):
    """Form the covalent bond from the best docked pose: bond the warhead
    electrophilic carbon to Cys SG and minimise with CA/CB/SG frozen and the ligand
    restrained toward its docked coordinates, so the bond closes while the pocket
    pose is largely preserved.

    Returns a dict with the achieved S–C distance, the heavy-atom RMSD from the
    docked pose, and the closest receptor contact. Raises ValueError when the bond
    could not be closed or the resulting pose clashes with the receptor — a tether
    that reports success with a 2.4 Å "bond" is worse than no tether at all.
    """
    dm = Chem.AddHs(docked, addCoords=True)
    match = dm.GetSubstructMatch(template)
    if not match:
        raise ValueError("docked pose does not match the ligand template")
    e_d, second_d = match[tidx[0]], match[tidx[1]]

    ref = dm.GetConformer()
    heavy_ref = {i: tuple(ref.GetAtomPosition(i)) for i in range(dm.GetNumAtoms())
                 if dm.GetAtomWithIdx(i).GetAtomicNum() > 1}

    # A CCS stub pinned at the cysteine's CA/CB/SG carries the real thiol geometry
    # into the minimisation without needing the whole protein.
    stub = Chem.AddHs(Chem.MolFromSmiles("CCS"))
    AllChem.EmbedMolecule(stub, randomSeed=1)
    sconf = stub.GetConformer()
    for i, key in ((0, "CA"), (1, "CB"), (2, "SG")):
        sconf.SetAtomPosition(i, Point3D(*cys[key]))

    rw = Chem.RWMol(Chem.CombineMols(stub, dm))
    off = stub.GetNumAtoms()
    ca_i, cb_i, sg_i = 0, 1, 2
    e_c = e_d + off
    h_del = [nb.GetIdx() for nb in rw.GetAtomWithIdx(sg_i).GetNeighbors()
             if nb.GetAtomicNum() == 1][:1]

    if mech in ("michael", "michael_yne"):
        alpha_c = second_d + off
        bond = rw.GetBondBetweenAtoms(e_c, alpha_c)
        bond.SetBondType(Chem.BondType.SINGLE if mech == "michael" else Chem.BondType.DOUBLE)
        rw.AddBond(sg_i, e_c, Chem.BondType.SINGLE)
        for hi in sorted(h_del, reverse=True):
            rw.RemoveAtom(hi)
        shift = lambda i: i - sum(1 for r in h_del if r < i)
        ca_i, cb_i, sg_i, e_c, alpha_c = (shift(ca_i), shift(cb_i), shift(sg_i),
                                          shift(e_c), shift(alpha_c))
        new_h = rw.AddAtom(Chem.Atom(1))
        rw.AddBond(alpha_c, new_h, Chem.BondType.SINGLE)
    elif mech == "sn2":
        halogen = second_d + off
        rw.AddBond(sg_i, e_c, Chem.BondType.SINGLE)
        drop = sorted(set(h_del) | {halogen}, reverse=True)
        for hi in drop:
            rw.RemoveAtom(hi)
        shift = lambda i: i - sum(1 for r in drop if r < i)
        ca_i, cb_i, sg_i, e_c = shift(ca_i), shift(cb_i), shift(sg_i), shift(e_c)
    else:
        raise ValueError(f"unknown warhead mechanism {mech!r}")

    m = rw.GetMol()
    Chem.SanitizeMol(m)

    props = AllChem.MMFFGetMoleculeProperties(m)
    if props is None:
        raise ValueError("MMFF typing failed for the tethered complex")
    ff = AllChem.MMFFGetMoleculeForceField(m, props)
    for idx in (ca_i, cb_i, sg_i):
        ff.AddFixedPoint(idx)
    for i in range(m.GetNumAtoms()):
        if i in (ca_i, cb_i, sg_i) or m.GetAtomWithIdx(i).GetAtomicNum() == 1:
            continue
        ff.MMFFAddPositionConstraint(i, TETHER_MAX_DISPL, TETHER_FORCE)
    ff.Minimize(maxIts=2000)

    conf = m.GetConformer()
    sc = math.dist(list(conf.GetAtomPosition(sg_i)), list(conf.GetAtomPosition(e_c)))
    if abs(sc - SC_BOND) > SC_BOND_TOL:
        raise ValueError(f"S–C bond did not close: {sc:.2f} Å")

    # Drift from the docked pose, over the ligand's heavy atoms.
    sq, n = 0.0, 0
    for i_d, ref_xyz in heavy_ref.items():
        p = conf.GetAtomPosition(shift(i_d + off))
        sq += (p.x - ref_xyz[0]) ** 2 + (p.y - ref_xyz[1]) ** 2 + (p.z - ref_xyz[2]) ** 2
        n += 1
    rmsd = math.sqrt(sq / n) if n else 0.0

    # Steric sanity against the receptor.
    lig_idx = [shift(i + off) for i in range(dm.GetNumAtoms())
               if dm.GetAtomWithIdx(i).GetAtomicNum() > 1]
    min_contact = float("inf")
    for i in lig_idx:
        p = conf.GetAtomPosition(i)
        for r in receptor_atoms:
            d = math.dist((p.x, p.y, p.z), r)
            if d < min_contact:
                min_contact = d
    if min_contact < CLASH_CUTOFF:
        raise ValueError(f"tethered pose clashes with the receptor: {min_contact:.2f} Å")

    # Emit the ligand alone: the stub's CA/CB/SG belong to the receptor.
    drop = {ca_i, cb_i, sg_i}
    for a in m.GetAtoms():
        if a.GetAtomicNum() == 1 and a.GetNeighbors()[0].GetIdx() in (ca_i, cb_i):
            drop.add(a.GetIdx())
    em = Chem.RWMol(m)
    for idx in sorted(drop, reverse=True):
        em.RemoveAtom(idx)
    with open(out_pdb, "w") as fh:
        fh.write(Chem.MolToPDBBlock(em.GetMol()))

    return {"bond_distance": round(sc, 3), "tether_rmsd": round(rmsd, 3),
            "min_contact": round(min_contact, 3)}


def cmd_detect(args):
    mol = Chem.MolFromSmiles(args.smiles)
    if mol is None:
        print(json.dumps({"error": "invalid SMILES"}))
        return 1
    name, _, _ = detect_warhead(mol)
    print(json.dumps({"has_warhead": name is not None, "warhead_type": name or ""}))
    return 0


def cmd_assess(args):
    template = Chem.MolFromSmiles(args.smiles)
    if template is None:
        print(json.dumps({"error": "invalid SMILES"}))
        return 1

    name, mech, tidx = detect_warhead(template)
    result = {"has_warhead": name is not None, "warhead_type": name or "",
              "status": ST_NO_WARHEAD, "reach_distance": None,
              "bond_distance": 0.0, "tether_written": False}
    if name is None:
        print(json.dumps(result))
        return 0

    cys = cys_atoms(args.receptor, args.chain, args.resnum)
    if cys["SG"] is None:
        result["status"] = ST_NO_THIOL
        print(json.dumps(result))
        return 0

    mols = pose_modes(args.pose)
    reach, best = scan_reach(template, tidx[0], mols, cys["SG"])
    if reach is None:
        # The pose exists but nothing in it could be mapped to the ligand. This is a
        # broken measurement, not a molecule that fails to reach — say so.
        result["status"] = ST_UNREADABLE
        result["modes_read"] = len(mols)
        print(json.dumps(result))
        return 0

    result["status"] = ST_MEASURED
    result["reach_distance"] = round(reach, 3)
    result["modes_read"] = len(mols)

    # Only build the tether for a warhead that actually reaches; forcing a bond onto
    # a pose 7 Å away yields a distorted pose that the bond/clash checks would reject
    # anyway.
    want_tether = args.tether_out and cys["CA"] and cys["CB"]
    if want_tether and args.tether_max_reach > 0 and reach > args.tether_max_reach:
        want_tether = False
    if want_tether:
        try:
            rec = receptor_heavy_atoms(args.receptor, args.chain, args.resnum)
            info = build_tether(template, tidx, mech, best, cys, rec, args.tether_out)
            result.update(info)
            result["tether_written"] = True
        except Exception as exc:  # noqa: BLE001 - the tether pose is best-effort
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
    a.add_argument("--tether-max-reach", type=float, default=0.0,
                   help="skip the tether when reach exceeds this (Å); 0 disables")
    a.set_defaults(func=cmd_assess)

    args = ap.parse_args()
    try:
        sys.exit(args.func(args))
    except Exception as exc:  # noqa: BLE001 - top-level CLI guard
        print(json.dumps({"error": str(exc)[:300]}))
        sys.exit(1)


if __name__ == "__main__":
    main()
