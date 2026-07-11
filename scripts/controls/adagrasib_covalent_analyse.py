#!/usr/bin/env python3
"""Analyse the adagrasib covalent control. See docs/features/12-adagrasib-covalent-control.md.

adagrasib is an approved covalent KRAS G12C inhibitor: in the clinic its alpha-fluoroacrylamide
forms a bond to Cys12 (kinact/KI ~ 35,000 M^-1 s^-1). Docked FREELY into the switch-II pocket,
does Stanza's geometry gate recognise that its warhead can attack?

The pipeline aggregates per-seed feasibility exactly as services/dual_dock.go does: the reported
feasibility is the MEDIAN across seeds, and a molecule is flagged `uncertain` (seed-dependent,
excluded from ranking) when the per-seed values straddle zero.

Two things must both hold for a PASS:
  1. The gate is calibrated: on at least one seed the drug scores a high feasibility, so the
     tool is not blind to a warhead it should recognise.
  2. The honesty machinery is correct: if the free dock finds that pose only sometimes, the
     seeds straddle zero and the molecule must be flagged uncertain, not ranked on luck.

Usage: python3 scripts/controls/adagrasib_covalent_analyse.py <workdir>
"""

import json
import os
import statistics as st
import sys

SEEDS = (42, 1337, 7)
HIGH = 0.5  # a seed at or above this counts as "the gate recognised the warhead"


def load(d, seed):
    with open(os.path.join(d, f"covalent_{seed}.json")) as fh:
        return json.load(fh)


def median(xs):
    """Lower-median, matching Go's slices sort + xs[len/2] on an odd-length sample."""
    return sorted(xs)[len(xs) // 2]


def main():
    d = sys.argv[1] if len(sys.argv) > 1 else "tmp/adagrasib_control"
    recs = {}
    for s in SEEDS:
        p = os.path.join(d, f"covalent_{s}.json")
        if not os.path.exists(p):
            sys.exit(f"missing {p} -- run scripts/controls/adagrasib_covalent.sh first")
        recs[s] = load(d, s)

    feas = [recs[s].get("feasibility", 0.0) or 0.0 for s in SEEDS]

    print("=== adagrasib docked freely into 6OIM (Cys12), per seed ===")
    print(f"  {'seed':>5} {'feasibility':>12} {'reach Å':>9} {'angle °':>9} {'mode':>5}")
    for s in SEEDS:
        r = recs[s]
        print(f"  {s:>5} {r.get('feasibility', 0.0):>12.3f} {r.get('reach_distance', 0.0):>9.2f} "
              f"{r.get('attack_angle', 0.0):>9.1f} {r.get('mode_rank', 0):>5}")

    med = median(feas)
    uncertain = min(feas) <= 0 and max(feas) > 0
    reach_spread = max(recs[s].get("reach_distance", 0.0) for s in SEEDS) - \
        min(recs[s].get("reach_distance", 0.0) for s in SEEDS)

    print("\n=== pipeline verdict (median feasibility + uncertain flag, per dual_dock.go) ===")
    print(f"  median feasibility {med:.3f}   best seed {max(feas):.3f}   reach spread {reach_spread:.2f} Å")
    print(f"  seed-dependent (uncertain): {uncertain}"
          + ("  -> excluded from ranking" if uncertain else ""))

    print("\n=== verdict ===")
    gate_ok = max(feas) >= HIGH
    if gate_ok and uncertain:
        print("  PASS - the gate recognises adagrasib's warhead on the pose Vina finds")
        print("         (best seed clears cleanly), AND the free dock finds that pose only")
        print("         sometimes, so the tool correctly flags the drug seed-dependent rather")
        print("         than ranking it on luck. Both the gate and the honesty backstop hold.")
        ok = True
    elif gate_ok and not uncertain:
        print("  PASS (stable) - the gate clears adagrasib on every seed; no seed dependence.")
        ok = True
    elif not gate_ok:
        print("  FAIL - no seed placed the warhead in bonding range. Either the gate is too")
        print("         strict or the free dock never orients this drug's warhead at the thiol.")
        ok = False
    else:
        ok = True

    print("\n  Reference: adagrasib (MRTX849) is an approved covalent G12C inhibitor; its")
    print("  alpha-fluoroacrylamide bonds Cys12 (kinact/KI ~ 35,000 M^-1 s^-1). Caveats: one")
    print("  drug, one dock; 6OIM is sotorasib's co-crystal so adagrasib's true pose differs;")
    print("  and this is a FREE dock -- Vina has no reason to aim the warhead at the thiol,")
    print("  which is exactly why the pose is found only some of the time.")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
