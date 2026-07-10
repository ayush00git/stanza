#!/usr/bin/env python3
"""Analyse the ABL T315I positive control. See docs/features/11-abl-t315i-positive-control.md.

Three checks, in the order that matters -- each one is worthless if the previous fails:

  0. Matched pair   Do the WT and mutant receptors share one backbone frame and differ only
                    at residue 315? If not, any selectivity is measuring the wrong thing.
  1. Redocking      Does the WT dock reproduce imatinib's crystallographic pose in 1IEP?
                    If not, nothing downstream means anything.
  2. Selectivity    Imatinib should lose against T315I; ponatinib should not.

selectivity = wt_score - mutant_score, both negative, so NEGATIVE selectivity means the
drug prefers wild-type -- i.e. the mutation confers resistance to it.

Usage: python3 scripts/controls/abl_t315i_analyse.py <workdir>
"""

import math
import os
import re
import statistics as st
import subprocess
import sys
import tempfile

from rdkit import Chem, RDLogger
from rdkit.Chem import AllChem

RDLogger.DisableLog("rdApp.*")

SEEDS = (42, 1337, 7)
DRUGS = ("imatinib", "ponatinib")
IMATINIB = "CC1=C(C=C(C=C1)NC(=O)C2=CC=C(C=C2)CN3CCN(CC3)C)NC4=NC=CC(=N4)C5=CN=CC=C5"

# Conventional redocking success line.
RMSD_PASS = 2.0


def best_affinity(path):
    """Mode-1 affinity from a Vina stdout log."""
    with open(path) as fh:
        hits = re.findall(r"^\s+1\s+(-?\d+\.\d+)", fh.read(), re.M)
    return float(hits[0]) if hits else None


def backbone(path):
    out = {}
    for line in open(path):
        if line.startswith("ATOM") and line[12:16].strip() in ("N", "CA", "C", "O"):
            out[(int(line[22:26]), line[12:16].strip())] = (
                float(line[30:38]), float(line[38:46]), float(line[46:54]))
    return out


def check_pair(d):
    w, m = backbone(os.path.join(d, "wt.pdb")), backbone(os.path.join(d, "mut.pdb"))
    shared = set(w) & set(m)
    dev = max(math.dist(w[k], m[k]) for k in shared)
    res = lambda p: next(l[17:20] for l in open(p)
                         if l.startswith("ATOM") and l[22:26].strip() == "315")
    a, b = res(os.path.join(d, "wt.pdb")), res(os.path.join(d, "mut.pdb"))
    ok = dev < 1e-3 and a == "THR" and b == "ILE"
    print("=== 0. Matched pair ===")
    print(f"  shared backbone atoms {len(shared)}, max deviation {dev:.4f} A, residue 315 {a} -> {b}")
    print(f"  {'OK - one frame, one difference' if ok else 'FAIL - the pair is not matched'}")
    return ok


def load(path, template, pdb=False):
    """Read coordinates and impose the template's chemistry on them.

    The pose file carries geometry, never bond orders worth trusting -- OpenBabel's
    perception of a fused heteroaromatic is not reliable. Sanitize nothing; take the
    graph from the SMILES.
    """
    m = (Chem.MolFromPDBFile(path, removeHs=True, sanitize=False) if pdb
         else next(Chem.SDMolSupplier(path, sanitize=False, removeHs=True)))
    m.UpdatePropertyCache(strict=False)
    Chem.FastFindRings(m)
    return AllChem.AssignBondOrdersFromTemplate(template, m)


def symm_rmsd(pose, xtal, template):
    """In-place RMSD (no superposition), minimised over the template's automorphisms.

    A nearest-neighbour distance is NOT this: it is a lower bound that flattered these
    poses by ~1.8x (0.60 A vs the true 1.07 A). Do not substitute it.
    """
    mx = xtal.GetSubstructMatches(template, uniquify=False, useChirality=False)
    mp = pose.GetSubstructMatches(template, uniquify=False, useChirality=False)
    if not mx or not mp:
        return None
    cx, cp = xtal.GetConformer(), pose.GetConformer()
    best = None
    for a in mx:
        pa = [cx.GetAtomPosition(i) for i in a]
        for b in mp:
            pb = [cp.GetAtomPosition(i) for i in b]
            s = sum((pa[k].x - pb[k].x) ** 2 + (pa[k].y - pb[k].y) ** 2 + (pa[k].z - pb[k].z) ** 2
                    for k in range(len(pa)))
            r = math.sqrt(s / len(pa))
            best = r if best is None or r < best else best
    return best


def redocking(d):
    print("\n=== 1. Redocking control: WT imatinib vs the 1IEP crystal pose ===")
    tmpl = Chem.MolFromSmiles(IMATINIB)
    sti = os.path.join(d, "sti_xtal.pdb")
    with open(sti, "w") as fh:
        for line in open(os.path.join(d, "1iep.pdb")):
            if line.startswith("HETATM") and line[17:20] == "STI" and line[21] == "A":
                fh.write(line)
        fh.write("END\n")
    xtal = load(sti, tmpl, pdb=True)

    worst = 0.0
    for seed in SEEDS:
        pq = os.path.join(d, "out", f"imatinib_wt_{seed}.pdbqt")
        if not os.path.exists(pq):
            print(f"  seed {seed}: missing")
            return False
        with tempfile.NamedTemporaryFile(suffix=".sdf", delete=False) as tf:
            sdf = tf.name
        subprocess.run(["obabel", pq, "-O", sdf, "-l", "1"], capture_output=True)
        r = symm_rmsd(load(sdf, tmpl), xtal, tmpl)
        os.unlink(sdf)
        worst = max(worst, r or 99)
        print(f"  seed {seed:>4}: symmetry-corrected in-place RMSD {r:5.2f} A")
    ok = worst < RMSD_PASS
    print(f"  {'OK' if ok else 'FAIL'} - success line is < {RMSD_PASS} A")
    return ok


def selectivity(d):
    print("\n=== 2. Selectivity: wt - mut  (negative = prefers wild-type = resistance) ===")
    scores = {}
    for lig in DRUGS:
        for rec in ("wt", "mut"):
            vals = []
            for s in SEEDS:
                p = os.path.join(d, "out", f"{lig}_{rec}_{s}.log")
                if os.path.exists(p):
                    v = best_affinity(p)
                    if v is not None:
                        vals.append(v)
            scores[(lig, rec)] = vals
            if len(vals) < len(SEEDS):
                print(f"  {lig}/{rec}: incomplete ({len(vals)}/{len(SEEDS)} seeds)")
                return False

    res, noise = {}, 0.0
    print(f"  {'drug':10} {'WT (3 seeds)':>24} {'T315I (3 seeds)':>24} {'med wt':>8} {'med mut':>8} {'sel':>7}")
    for lig in DRUGS:
        w, m = scores[(lig, "wt")], scores[(lig, "mut")]
        noise = max(noise, max(w) - min(w), max(m) - min(m))
        res[lig] = st.median(w) - st.median(m)
        f = lambda v: "[" + " ".join(f"{x:6.2f}" for x in v) + "]"
        print(f"  {lig:10} {f(w):>24} {f(m):>24} {st.median(w):8.2f} {st.median(m):8.2f} {res[lig]:+7.2f}")

    si, sp = res["imatinib"], res["ponatinib"]
    sep = abs(si - sp)
    print(f"\n  worst seed-to-seed spread on any track: {noise:.2f} kcal/mol")
    print(f"  separation |imatinib - ponatinib| = {sep:.2f} kcal/mol")

    print("\n=== verdict ===")
    if si >= 0:
        print("  FAIL - wrong sign: the dock does not see T315I as resistance to imatinib.")
        ok = False
    elif si >= sp:
        print("  FAIL - imatinib is not penalised more than ponatinib.")
        ok = False
    elif sep > noise:
        print("  PASS - correct sign, correct ordering, separation exceeds seed noise.")
        ok = True
    else:
        print("  WEAK PASS - correct sign and ordering, but separation is within seed noise.")
        ok = True

    print("\n  Reference: imatinib loses ~1000x (~4 kcal/mol) to T315I; ponatinib retains sub-nM.")
    print("  We recover a fraction of that magnitude. T315I resists partly by steric bulk and")
    print("  partly by destabilising the DFG-out conformation imatinib requires -- a dock into")
    print("  one frozen frame is blind to the second half. Trust the sign, never the number.")
    return ok


def main():
    d = sys.argv[1] if len(sys.argv) > 1 else "tmp/abl_control"
    if not os.path.isdir(os.path.join(d, "out")):
        sys.exit(f"no docking output in {d} -- run scripts/controls/abl_t315i.sh first")
    ok = check_pair(d) and redocking(d) and selectivity(d)
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
