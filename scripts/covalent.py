#!/usr/bin/env python3
"""Covalent-docking helper for the Stanza mutant track.

AutoDock Vina scores non-covalently: it cannot see the covalent bond a warhead
forms to a cysteine thiol, which is the entire selectivity mechanism of covalent
inhibitors (sotorasib/adagrasib bond KRAS Cys12; wild-type Gly12 has no thiol, so
the drug physically cannot attach). This script supplies the geometry Vina is
blind to:

  detect  — does a SMILES carry a cysteine-reactive warhead, and of what class.
  assess  — given a Vina multi-mode docked pose and the target cysteine, score how
            feasibly the warhead's electrophilic carbon can attack the Cys SG. Every
            docked mode the receptor actually binds (within an energy window of the
            best mode) is scanned — the covalently-competent orientation is often not
            the top-scoring non-covalent pose — and the geometry is scored as a
            feasibility in [0,1] from BOTH the S···C reach and the attack angle. The
            most feasible pose wins; optionally the tethered covalent-complex pose
            (warhead bonded to SG) is written.

Feasibility, not distance, is the output: the minimum of a distance over stochastic
samples is a downward-biased estimator that rewards conformational promiscuity, and a
distance with no trajectory says nothing about whether attack can occur. The Go side
applies no threshold — it consumes the feasibility and the geometry that produced it.
This script owns every chemistry and geometry decision.

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

# Covalent-competence geometry. Every chemistry/geometry decision lives here now; the Go
# side applies no threshold and only consumes the feasibility we return.
#
# REACH_IDEAL is the Bondi S···C van der Waals contact distance (C 1.70 + S 1.80 =
# 3.50 Å). In a NON-covalent dock the warhead carbon and SG are non-bonded spheres that
# physically cannot approach closer, so this is the correct full-credit anchor for a
# docked pose — not an arbitrary cutoff.
REACH_IDEAL = 3.5
# Beyond REACH_MAX the warhead is not covalently competent. Published covalent-docking
# practice draws the line at < 4.0 Å S···electrophile; approved covalent drugs sit at
# 2.98–3.78 Å in their co-crystals. The former 5.0 Å paid 25–50% credit at 4.0–5.0 Å,
# a tail outside anything the literature calls competent.
REACH_MAX = 4.0

# A near-attack conformation needs a trajectory, not just a distance: a warhead can sit
# 3.4 Å from SG with an orientation that makes attack geometrically impossible. Attack
# on a trigonal (sp2) Michael acceptor follows the Bürgi–Dunitz angle (~105°); SN2
# backside attack on a haloacetamide is collinear (~180°).
ANGLE_IDEAL_SP2 = 105.0
ANGLE_IDEAL_SN2 = 180.0
# Within ANGLE_TOL_FULL of the ideal the trajectory is as good as ideal; past
# ANGLE_TOL_ZERO the transition state is out of reach. Linear between the two.
ANGLE_TOL_FULL = 15.0
ANGLE_TOL_ZERO = 40.0

# A docked mode whose affinity is worse than the best mode's by more than this is not a
# pose the receptor binds, so its geometry must not earn covalent credit. This gate is
# what stops an unguarded minimum over 20 modes × 5 seeds from rewarding conformational
# promiscuity: a floppy ligand cannot buy reach with a pose the receptor never holds.
MODE_ENERGY_WINDOW = 2.0

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


def pose_query(template):
    """A substructure query that identifies the ligand by its ATOMS AND TOPOLOGY, with
    bond orders left unconstrained.

    A docked pose is coordinates. To read chemistry back out of it, OpenBabel must
    guess bonds from interatomic distances, and on drug-like scaffolds it guesses
    wrong: fused heteroaromatics come back with bad hydrogen counts, which leaves
    stray radicals and destroys the aromatic perception. RDKit then rejects the
    molecule outright (every SDF record sanitizes to None) or accepts a mangled one
    whose bonds no longer match the template. Either way the warhead is never located
    and a covalent molecule is silently scored as non-covalent.

    So never ask the pose what the molecule is. The elements and the bond graph are
    enough to pin every atom down uniquely, and both survive a bad bond-order guess.
    """
    params = Chem.AdjustQueryParameters.NoAdjustments()
    params.makeBondsGeneric = True
    return Chem.AdjustQueryProperties(Chem.Mol(template), params)


def pose_modes(pose_path):
    """Read a Vina multi-mode docked PDBQT into one RDKit molecule per mode, WITHOUT
    sanitizing: these molecules supply coordinates only, and sanitization would reject
    them for a bad valence that OpenBabel invented and that we are about to ignore.
    Ring info is still needed for substructure matching, hence FastFindRings.
    """
    workdir = tempfile.mkdtemp(prefix="covpose-")
    sdf = os.path.join(workdir, "modes.sdf")
    subprocess.run(["obabel", pose_path, "-osdf", "-O", sdf],
                   capture_output=True, text=True)
    if not os.path.exists(sdf):
        return []
    out = []
    for m in Chem.SDMolSupplier(sdf, removeHs=False, sanitize=False):
        if m is None:
            continue
        m.UpdatePropertyCache(strict=False)
        Chem.FastFindRings(m)
        out.append(m)
    return out


def mode_affinities(pose_path):
    """Per-mode Vina affinities (kcal/mol) in MODEL order, one per docked pose. Vina
    writes them into the PDBQT as `REMARK VINA RESULT: <affinity> <rmsd_lb> <rmsd_ub>`,
    one line per MODEL, so file order is MODEL order.

    These gate out poses the receptor does not actually bind. The caller must NOT assume
    this list aligns 1:1 with pose_modes() output — OpenBabel can silently drop a MODEL
    on conversion — so it checks the counts before trusting the alignment.
    """
    out = []
    with open(pose_path) as fh:
        for line in fh:
            if line.startswith("REMARK VINA RESULT:"):
                fields = line.split(":", 1)[1].split()
                if fields:
                    try:
                        out.append(float(fields[0]))
                    except ValueError:
                        pass
    return out


def angle_deg(a, b, c):
    """Angle at vertex b (degrees) between the rays b→a and b→c. Returns 0.0 for a
    degenerate (zero-length) ray rather than raising."""
    v1 = (a[0] - b[0], a[1] - b[1], a[2] - b[2])
    v2 = (c[0] - b[0], c[1] - b[1], c[2] - b[2])
    n1 = math.sqrt(v1[0] ** 2 + v1[1] ** 2 + v1[2] ** 2)
    n2 = math.sqrt(v2[0] ** 2 + v2[1] ** 2 + v2[2] ** 2)
    if n1 == 0.0 or n2 == 0.0:
        return 0.0
    cos = (v1[0] * v2[0] + v1[1] * v2[1] + v1[2] * v2[2]) / (n1 * n2)
    return math.degrees(math.acos(max(-1.0, min(1.0, cos))))


def distance_score(reach):
    """Reach → [0,1]: full credit at the Bondi vdW contact (≤ REACH_IDEAL), linearly
    down to 0 at REACH_MAX, 0 beyond. A closer-than-contact pose cannot do better than
    contact, so the score saturates rather than rewarding overlap."""
    if reach <= REACH_IDEAL:
        return 1.0
    if reach >= REACH_MAX:
        return 0.0
    return (REACH_MAX - reach) / (REACH_MAX - REACH_IDEAL)


def angle_score(angle, ideal):
    """Attack angle → [0,1]: full credit within ANGLE_TOL_FULL of the ideal trajectory,
    linearly down to 0 at ANGLE_TOL_ZERO, 0 beyond. Deviation is symmetric about the
    ideal, so a too-shallow and a too-steep approach are penalised alike."""
    dev = abs(angle - ideal)
    if dev <= ANGLE_TOL_FULL:
        return 1.0
    if dev >= ANGLE_TOL_ZERO:
        return 0.0
    return (ANGLE_TOL_ZERO - dev) / (ANGLE_TOL_ZERO - ANGLE_TOL_FULL)


def scan_geometry(query, tidx, mech, mols, sg, affinities):
    """Score the covalent near-attack geometry across the docked modes and return the
    SINGLE most feasible pose as a dict {reach, angle, feasibility, mode_rank,
    mode_affinity, mol, match}, plus a note string, or (None, note) when no mode could
    be mapped to the ligand.

    Only modes the receptor actually binds contribute geometry: with `affinities` given,
    a mode more than MODE_ENERGY_WINDOW above the best affinity is dropped, so reach
    cannot be bought with a high-energy pose. When `affinities` is None (the count could
    not be aligned to the molecules) the gate is skipped and mode_affinity is unknown.

    Feasibility = distance_score × angle_score, and the pose with the HIGHEST
    feasibility wins — not the smallest distance. Minimum-over-samples is the
    downward-biased estimator we are removing; feasibility gates on both reach and
    trajectory, so it cannot be won by a floppy ligand sampling many orientations.

    `tidx[0]` is the electrophilic carbon (attack vertex); `tidx[1]` is the α-carbon
    (michael/michael_yne) or leaving halogen (sn2). Symmetric molecules admit several
    atom mappings, so every one is scored.
    """
    note = ""
    if affinities is not None:
        cutoff = min(affinities) + MODE_ENERGY_WINDOW
        candidates = [i for i, a in enumerate(affinities) if a <= cutoff]
        if not candidates:
            # The window admits the best mode by construction, so an empty set can only
            # come from degenerate affinities — fall back to the best-affinity mode.
            candidates = [min(range(len(affinities)), key=lambda i: affinities[i])]
            note = "energy window admitted no mode; fell back to best-affinity mode"
    else:
        candidates = list(range(len(mols)))

    ideal = ANGLE_IDEAL_SN2 if mech == "sn2" else ANGLE_IDEAL_SP2
    best = None
    for i in candidates:
        m = mols[i]
        conf = m.GetConformer()
        for match in m.GetSubstructMatches(query):
            e = conf.GetAtomPosition(match[tidx[0]])
            s = conf.GetAtomPosition(match[tidx[1]])
            e_xyz = (e.x, e.y, e.z)
            reach = math.dist(e_xyz, sg)
            angle = angle_deg(sg, e_xyz, (s.x, s.y, s.z))
            feas = distance_score(reach) * angle_score(angle, ideal)
            if best is None or feas > best["feasibility"]:
                best = {"reach": reach, "angle": angle, "feasibility": feas,
                        "mode_rank": i + 1,
                        "mode_affinity": affinities[i] if affinities is not None else 0.0,
                        "mol": m, "match": match}
    return best, note


def template_on_pose(template, mol, match):
    """The template molecule — correct chemistry, from the SMILES — carrying the docked
    pose's coordinates. This is what the tether is built from, so a wrong bond order
    guessed off the pose can never reach the covalent complex."""
    t = Chem.Mol(template)
    t.RemoveAllConformers()
    src = mol.GetConformer()
    conf = Chem.Conformer(t.GetNumAtoms())
    for ti in range(t.GetNumAtoms()):
        p = src.GetAtomPosition(match[ti])
        conf.SetAtomPosition(ti, Point3D(p.x, p.y, p.z))
    t.AddConformer(conf, assignId=True)
    return t


def build_tether(posed, tidx, mech, cys, receptor_atoms, out_pdb):
    """Form the covalent bond from the best docked pose: bond the warhead
    electrophilic carbon to Cys SG and minimise with CA/CB/SG frozen and the ligand
    restrained toward its docked coordinates, so the bond closes while the pocket
    pose is largely preserved.

    `posed` is the TEMPLATE molecule carrying the docked coordinates, so its bond
    orders and hydrogen counts are the SMILES' and not OpenBabel's guess — opening the
    warhead's C=C is only meaningful if that bond really is a double bond.

    Returns a dict with the achieved S–C distance, the heavy-atom RMSD from the
    docked pose, and the closest receptor contact. Raises ValueError when the bond
    could not be closed or the resulting pose clashes with the receptor — a tether
    that reports success with a 2.4 Å "bond" is worse than no tether at all.
    """
    # AddHs appends hydrogens after the heavy atoms, so template indices still hold.
    dm = Chem.AddHs(posed, addCoords=True)
    e_d, second_d = tidx[0], tidx[1]

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
    # Every emission carries the full contract at its default so the Go side never has to
    # distinguish "field absent" from "value unknown". feasibility/reach stay null until
    # a real measurement; the rest default to the "unknown" sentinels the struct expects.
    result = {"has_warhead": name is not None, "warhead_type": name or "",
              "status": ST_NO_WARHEAD, "feasibility": None, "reach_distance": None,
              "attack_angle": 0.0, "mode_rank": 0, "mode_affinity": 0.0,
              "modes_read": 0, "bond_distance": 0.0, "tether_rmsd": 0.0,
              "min_contact": 0.0, "tether_written": False}
    if name is None:
        print(json.dumps(result))
        return 0

    cys = cys_atoms(args.receptor, args.chain, args.resnum)
    if cys["SG"] is None:
        result["status"] = ST_NO_THIOL
        print(json.dumps(result))
        return 0

    mols = pose_modes(args.pose)
    result["modes_read"] = len(mols)

    # The energy gate keeps geometry from off-energy poses out of the score. It needs a
    # 1:1 alignment between the parsed affinities and the molecules, but OpenBabel can
    # drop a MODEL on conversion; if the counts disagree we refuse to guess which
    # affinity belongs to which pose and run ungated rather than credit the wrong energy.
    notes = []
    affinities = mode_affinities(args.pose)
    if mols and len(affinities) == len(mols):
        result["energy_gate"] = True
        gate_affs = affinities
    else:
        result["energy_gate"] = False
        gate_affs = None
        if mols:
            notes.append(f"affinity/mol count mismatch "
                         f"({len(affinities)} vs {len(mols)}); energy gate skipped")

    geom, gnote = scan_geometry(pose_query(template), tidx, mech, mols,
                                cys["SG"], gate_affs)
    if gnote:
        notes.append(gnote)
    if notes:
        result["note"] = "; ".join(notes)

    if geom is None:
        # The pose exists but nothing in it could be mapped to the ligand. This is a
        # broken measurement, not a molecule that fails to reach — say so.
        result["status"] = ST_UNREADABLE
        print(json.dumps(result))
        return 0

    result["status"] = ST_MEASURED
    result["feasibility"] = round(geom["feasibility"], 3)
    result["reach_distance"] = round(geom["reach"], 3)
    result["attack_angle"] = round(geom["angle"], 1)
    result["mode_rank"] = geom["mode_rank"]
    result["mode_affinity"] = round(geom["mode_affinity"], 3)

    # The script — not the caller — decides when to tether: build it ONLY when the
    # warhead can actually attack (feasibility > 0). Forcing a bond onto an infeasible
    # pose yields a distorted structure the bond/clash checks would reject anyway.
    if args.tether_out and geom["feasibility"] > 0 and cys["CA"] and cys["CB"]:
        try:
            rec = receptor_heavy_atoms(args.receptor, args.chain, args.resnum)
            posed = template_on_pose(template, geom["mol"], geom["match"])
            info = build_tether(posed, tidx, mech, cys, rec, args.tether_out)
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
    a.set_defaults(func=cmd_assess)

    args = ap.parse_args()
    try:
        sys.exit(args.func(args))
    except Exception as exc:  # noqa: BLE001 - top-level CLI guard
        print(json.dumps({"error": str(exc)[:300]}))
        sys.exit(1)


if __name__ == "__main__":
    main()
