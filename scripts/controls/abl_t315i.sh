#!/usr/bin/env bash
#
# ABL T315I positive control (see docs/features/11-abl-t315i-positive-control.md).
#
# Stanza's selectivity = wt_score - mutant_score is structurally ~0 on KRAS G12C, because
# that mutation's advantage is covalent rather than shape-based. A number that is supposed
# to be zero cannot demonstrate the dual-track machinery would be non-zero when it should.
#
# BCR-ABL T315I is the complementary case: a STERIC resistance mutation with two real drugs
# and an answer known in advance. Imatinib loses ~1000x (~4 kcal/mol) to T315I; ponatinib was
# designed to survive it. If the machinery cannot separate them, selectivity measures nothing.
#
# Expected (selectivity = wt - mut, both scores negative, so NEGATIVE = prefers wild-type):
#   imatinib   ~ -0.35   defeated by T315I
#   ponatinib  ~ +0.45   survives it
#
# Everything below mirrors services/dual_dock.go: exhaustiveness 16, --cpu 2,
# seeds {42,1337,7}, 20 modes, median over seeds, and PrepareReceptor's `obabel -xr`.
#
# Usage:  scripts/controls/abl_t315i.sh [workdir]      (default: ./tmp/abl_control)
# Needs:  curl, obabel, vina, python3 + RDKit/PDBFixer (scripts/requirements.txt)
# Runtime: ~8 min for 12 docks on 6 cores.

set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
D="${1:-$REPO/tmp/abl_control}"
mkdir -p "$D/out"

# 1IEP: ABL kinase domain + imatinib (STI), DFG-out, 2.10 A. The conformation is
# load-bearing -- imatinib REQUIRES DFG-out. Docking it into an active-conformation or
# AlphaFold model fails for reasons that have nothing to do with T315I.
if [ ! -s "$D/1iep.pdb" ]; then
  echo "==> fetching 1IEP"
  curl -sf --max-time 60 -o "$D/1iep.pdb" https://files.rcsb.org/download/1IEP.pdb || {
    echo "failed to fetch 1IEP" >&2; exit 1; }
fi

# Ligands, by PubChem CID. Do NOT hand-type these: an earlier revision of the KRAS
# prior-art set was typed from memory and every structure was a different molecule.
#   imatinib  CID 5291      ponatinib CID 24826799
IMATINIB='CC1=C(C=C(C=C1)NC(=O)C2=CC=C(C=C2)CN3CCN(CC3)C)NC4=NC=CC(=N4)C5=CN=CC=C5'
PONATINIB='CC1=C(C=C(C=C1)C(=O)NC2=CC(=C(C=C2)CN3CCN(CC3)C)C(F)(F)F)C#CC4=CN=C5N4N=CC=C5'

echo "==> building the matched WT / T315I pair"
# Both receptors come from the SAME input through the SAME script, so they share one
# backbone frame and differ only at residue 315. The analyser re-verifies this.
python3 "$REPO/scripts/mutate.py" --input "$D/1iep.pdb" --chain A --resnum 315 \
  --to THR --out "$D/wt.pdb"  --keep-chain A --strip-het >/dev/null
python3 "$REPO/scripts/mutate.py" --input "$D/1iep.pdb" --chain A --resnum 315 \
  --to ILE --out "$D/mut.pdb" --keep-chain A --strip-het >/dev/null

echo "==> deriving the box from the crystallographic imatinib"
# Same rule as services/docking.go boxSizeFor(): ligand extent + 8 A padding, clamped.
read -r CX CY CZ SZ < <(python3 - "$D/1iep.pdb" <<'PY'
import sys, statistics as st
xs=ys=zs=None; pts=[]
for l in open(sys.argv[1]):
    if l.startswith('HETATM') and l[17:20]=='STI' and l[21]=='A' and l[76:78].strip()!='H':
        pts.append((float(l[30:38]), float(l[38:46]), float(l[46:54])))
xs,ys,zs = zip(*pts)
span = max(max(xs)-min(xs), max(ys)-min(ys), max(zs)-min(zs))
size = min(max(span+8.0, 20.0), 30.0)
print(f"{st.mean(xs):.3f} {st.mean(ys):.3f} {st.mean(zs):.3f} {size:.3f}")
PY
)
echo "    center ($CX, $CY, $CZ)  edge ${SZ} A"

echo "==> preparing receptors and ligands"
for t in wt mut; do
  obabel "$D/$t.pdb" -O "$D/${t}_rec.pdbqt" -xr >/dev/null 2>&1
  # Vina rejects anything but these records in a rigid receptor (stripNonPDBQTLines).
  grep -E '^(ATOM|HETATM|REMARK|TER|END|MODEL|ENDMDL)' "$D/${t}_rec.pdbqt" > "$D/$t.tmp"
  mv "$D/$t.tmp" "$D/${t}_rec.pdbqt"
done
python3 "$REPO/scripts/ligprep.py" --smiles "$IMATINIB"  --out "$D/imatinib.sdf"  --seed 42 >/dev/null
python3 "$REPO/scripts/ligprep.py" --smiles "$PONATINIB" --out "$D/ponatinib.sdf" --seed 42 >/dev/null
for l in imatinib ponatinib; do obabel "$D/$l.sdf" -O "$D/$l.pdbqt" >/dev/null 2>&1; done

echo "==> docking: 2 drugs x 2 receptors x 3 seeds = 12 runs"
started=$(date +%s)
for lig in imatinib ponatinib; do
  for rec in wt mut; do
    for seed in 42 1337 7; do
      out="$D/out/${lig}_${rec}_${seed}"
      [ -s "$out.log" ] && { echo "    skip ${lig}/${rec}/${seed} (cached)"; continue; }
      vina --receptor "$D/${rec}_rec.pdbqt" --ligand "$D/${lig}.pdbqt" \
        --center_x "$CX" --center_y "$CY" --center_z "$CZ" \
        --size_x "$SZ" --size_y "$SZ" --size_z "$SZ" \
        --exhaustiveness 16 --cpu 2 --seed "$seed" --num_modes 20 \
        --out "$out.pdbqt" > "$out.log" 2>&1 &
      # bounded pool of 3 -> 6 cores, matching maxParallelDocks in dual_dock.go
      while [ "$(jobs -rp | wc -l)" -ge 3 ]; do wait -n; done
    done
  done
done
wait
echo "    12 docks in $(( $(date +%s) - started ))s"

echo
python3 "$REPO/scripts/controls/abl_t315i_analyse.py" "$D"
