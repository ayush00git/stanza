# adagrasib covalent control

**Question.** The ABL T315I control validates the *steric* side of the pipeline, where
`selectivity` is a physically meaningful quantity. It says nothing about the covalent side,
which is the whole point of the KRAS work. The covalent feasibility gate needs its own
known-answer test: docked freely into the switch-II pocket, does the gate recognise the
warhead of a drug that provably bonds Cys12, and does the honesty machinery behave when the
free dock finds the bonding pose only some of the time?

**adagrasib (MRTX849)** is the answer key. It is an approved covalent KRAS G12C inhibitor;
its alpha-fluoroacrylamide forms a bond to Cys12 in the clinic (kinact/KI ~ 35,000 M⁻¹s⁻¹).
If the gate cannot recognise adagrasib's warhead, it is too strict to be useful. If it
recognises it on a lucky seed but reports that as a confident answer, the honesty backstop
is broken. Both must come out right.

## Reproducing it

```bash
scripts/controls/adagrasib_covalent.sh                        # ~10-15 min (adagrasib is floppy)
scripts/controls/adagrasib_covalent_analyse.py tmp/adagrasib_control   # re-analyse without re-docking
```

The script fetches 6OIM, builds the Cys12 receptor the same way the pipeline does
(`scripts/mutate.py`, `--strip-het`), derives the box from the co-crystal sotorasib, embeds
adagrasib from its PubChem SMILES, docks three seeds at exhaustiveness 16, and runs
`scripts/covalent.py` on each. Nothing is hand-entered.

## Result

adagrasib docked freely into 6OIM (Cys12), per seed:

| seed | feasibility | reach | attack angle |
|---|---|---|---|
| 42 | 0.00 | 4.77 Å | 74° |
| 1337 | 0.13 | 3.89 Å | 80° |
| 7 | **0.98** | **3.51 Å** | **107.2°** |

The pipeline aggregates per-seed feasibility exactly as `services/dual_dock.go` does: the
reported feasibility is the **median** (0.13), and because the per-seed values straddle zero
the molecule is flagged **`uncertain`** (seed-dependent, excluded from ranking).

**PASS**, and both halves matter:

- **The gate is calibrated.** On seed 7 the free dock places adagrasib's warhead at 3.51 Å
  and 107.2°, essentially textbook Bürgi–Dunitz geometry, and the gate scores it 0.98. The
  tool is not blind to a warhead it should recognise.
- **The free-dock limitation is real, even for an approved drug.** That pose shows up in one
  seed of three. adagrasib provably bonds Cys12 in the clinic, yet the free dock is bimodal
  on it, so the pipeline reports it seed-dependent and refuses to rank it rather than
  publishing the lucky 0.98.

## What this establishes, and what it does not

**Establishes** that the covalent feasibility gate recognises a real drug's warhead when the
dock finds the bonding pose, and that the `uncertain` backstop fires correctly on a molecule
whose covalent call flips with the seed, even when that molecule is a known inhibitor. It is
the covalent analogue of the T315I steric control.

**It also recalibrates how to read the board.** A feasibility above zero means the free dock
found a pose from which the warhead can attack, which is real signal. A feasibility of zero
does **not** mean the molecule cannot bond: adagrasib scores zero on seed 42 and is an
approved covalent drug. The tool underestimates, because the free dock has no reason to aim
the warhead at the thiol. Genuine covalent docking (search under the bond constraint) is the
fix, and is on the roadmap.

**Does not establish** anything from one drug and one dock. 6OIM is sotorasib's co-crystal,
so adagrasib's true binding pose differs from the one docked here, and the whole measurement
is a free dock. A real evaluation would run a panel of covalent actives and inactives and
report how cleanly the gate separates them.
