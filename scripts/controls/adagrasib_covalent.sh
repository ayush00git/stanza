#!/usr/bin/env bash
#
# adagrasib covalent control (see docs/features/12-adagrasib-covalent-control.md).
#
# The covalent counterpart to the ABL T315I steric control. adagrasib (MRTX849) is an
# approved covalent KRAS G12C inhibitor: its alpha-fluoroacrylamide bonds Cys12 in the
# clinic (kinact/KI ~ 35,000 M^-1 s^-1). Docked FREELY into the switch-II pocket, does
# Stanza's geometry gate recognise that the warhead can attack, and does the honesty
# backstop behave when the free dock finds that pose only some of the time?
#
# Everything mirrors services/dual_dock.go: exhaustiveness 16, --cpu 2, seeds {42,1337,7},
# 20 modes, and scripts/covalent.py for the geometry. The receptor is built from 6OIM the
# same way the pipeline builds it (mutate.py, --strip-het), and the box is derived from the
# co-crystal sotorasib, so nothing is hand-entered.
#
# Usage:  scripts/controls/adagrasib_covalent.sh [workdir]   (default: ./tmp/adagrasib_control)
# Needs:  curl, obabel, vina, python3 + RDKit/PDBFixer (scripts/requirements.txt)
# Runtime: ~10-15 min -- adagrasib has 16 rotatable bonds, so each dock is slow.

set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
D="${1:-$REPO/tmp/adagrasib_control}"
mkdir -p "$D"

# 6OIM: KRAS G12C + sotorasib (MOV) covalently bound to Cys12, 1.65 A. Residue 12 is already
# CYS, so no mutation is needed; running mutate.py --to CYS rebuilds a clean, unmodified
# cysteine side chain (the crystal's SG carries the sotorasib adduct) and strips the ligand.
if [ ! -s "$D/6oim.pdb" ]; then
  echo "==> fetching 6OIM"
  curl -sf --max-time 60 -o "$D/6oim.pdb" https://files.rcsb.org/download/6OIM.pdb || {
    echo "failed to fetch 6OIM" >&2; exit 1; }
fi

# adagrasib (MRTX849), PubChem CID 138611145. SMILES fetched by CID, not hand-typed.
ADAGRASIB='CN1CCC[C@H]1COC2=NC3=C(CCN(C3)C4=CC=CC5=C4C(=CC=C5)Cl)C(=N2)N6CCN([C@H](C6)CC#N)C(=O)C(=C)F'

echo "==> building the Cys12 receptor from 6OIM"
python3 "$REPO/scripts/mutate.py" --input "$D/6oim.pdb" --chain A --resnum 12 \
  --to CYS --out "$D/mut.pdb" --keep-chain A --strip-het >/dev/null

echo "==> deriving the box from the co-crystal sotorasib (MOV)"
read -r CX CY CZ SZ < <(python3 - "$D/6oim.pdb" <<'PY'
import sys, statistics as st
pts = []
for l in open(sys.argv[1]):
    if l.startswith('HETATM') and l[17:20] == 'MOV' and l[21] == 'A' and l[76:78].strip() != 'H':
        pts.append((float(l[30:38]), float(l[38:46]), float(l[46:54])))
xs, ys, zs = zip(*pts)
span = max(max(xs)-min(xs), max(ys)-min(ys), max(zs)-min(zs))
size = min(max(span+8.0, 20.0), 26.0)
print(f"{st.mean(xs):.3f} {st.mean(ys):.3f} {st.mean(zs):.3f} {size:.3f}")
PY
)
echo "    center ($CX, $CY, $CZ)  edge ${SZ} A"

echo "==> preparing the receptor and adagrasib"
obabel "$D/mut.pdb" -O "$D/mut_receptor.pdbqt" -xr >/dev/null 2>&1
grep -E '^(ATOM|HETATM|REMARK|TER|END|MODEL|ENDMDL)' "$D/mut_receptor.pdbqt" > "$D/rec.tmp"
mv "$D/rec.tmp" "$D/mut_receptor.pdbqt"
python3 "$REPO/scripts/ligprep.py" --smiles "$ADAGRASIB" --out "$D/adag.sdf" --seed 42 >/dev/null
obabel "$D/adag.sdf" -O "$D/adag.pdbqt" >/dev/null 2>&1

echo "==> docking + covalent assessment over 3 seeds (adagrasib is floppy; this is slow)"
started=$(date +%s)
for seed in 42 1337 7; do
  out="$D/pose_${seed}.pdbqt"
  if [ ! -s "$out" ]; then
    vina --receptor "$D/mut_receptor.pdbqt" --ligand "$D/adag.pdbqt" \
      --center_x "$CX" --center_y "$CY" --center_z "$CZ" \
      --size_x "$SZ" --size_y "$SZ" --size_z "$SZ" \
      --exhaustiveness 16 --cpu 2 --seed "$seed" --num_modes 20 \
      --out "$out" > "$D/vina_${seed}.log" 2>&1
  fi
  python3 "$REPO/scripts/covalent.py" assess --smiles "$ADAGRASIB" --pose "$out" \
    --receptor "$D/mut.pdb" --chain A --resnum 12 > "$D/covalent_${seed}.json" 2>/dev/null
  echo "    seed $seed: feasibility $(python3 -c "import json;print(json.load(open('$D/covalent_${seed}.json')).get('feasibility'))")"
done
echo "    done in $(( $(date +%s) - started ))s"

echo
python3 "$REPO/scripts/controls/adagrasib_covalent_analyse.py" "$D"
