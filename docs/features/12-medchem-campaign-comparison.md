# The controls versus their published discovery campaigns

The molecules Stanza uses as controls are drugs whose discovery stories are published in the
*Journal of Medicinal Chemistry*: sotorasib and adagrasib on KRAS G12C, imatinib and ponatinib
on BCR-ABL T315I. Each paper documents, by hand and over years, the structure-based design loop
Stanza automates. This maps Stanza's logic onto those campaigns and, in the project's habit,
states what the comparison licenses as a claim and what it does not.

## How to read this

One caveat governs the whole section: matching a design *rationale* is not the same as recovering
an optimized *structure*. Stanza is a warhead-reach triage filter with error bars, not a
selectivity or affinity predictor (see the README and
[`10-covalent-validity-audit.md`](10-covalent-validity-audit.md)). Where a campaign and Stanza
agree, the agreement is on the *direction* of a design decision, not on a number. Read every
"Stanza recovers X" below with that bound.

## 1. Sotorasib (AMG 510): the warhead trajectory

**The campaign.** Amgen grew sotorasib from the weak fragment the Shokat lab found in the cryptic
switch-II pocket (Ostrem 2013). The load-bearing move was conformational: a quinazolinone core and
a locked atropisomer that fix the molecule's orientation so the acrylamide warhead sits at the
distance and angle needed to react with Cys12. Reversible affinity for this pocket is weak, so the
potency is carried by getting the warhead to bond, not by binding tighter (Lanman 2020).

**Stanza's counterpart.** Stanza scores exactly that geometry:
`feasibility = distance_score × angle_score`, with full distance credit at the 3.50 Å Bondi
S···C contact and the angle measured against the 105° Bürgi–Dunitz trajectory, and only Vina
modes within 2.0 kcal/mol of the best pose allowed to contribute (`scripts/covalent.py`). It is
the same distance-and-angle question Amgen spent the campaign answering by hand.

**What the comparison licenses.** Stanza can say it evaluates, per pose, the same reach-and-angle
criterion, and flags in seconds which generated scaffolds can reach Cys12 along a viable path. It
cannot say a scaffold is as good as sotorasib, nor that a docked pose is the optimized bound
conformation: Stanza free-docks, so Vina has no reason to aim the warhead, and the feasibility
proxy disagrees with the stronger tether-build check on 4 of 5 measured molecules (README,
Limitations). The honest statement is that Stanza recovers the design *question* Amgen posed, not
their answer.

## 2. Adagrasib (MRTX849): why the weight window breaks Lipinski

**The campaign.** Mirati solved the same target from a different scaffold, a
tetrahydropyridopyrimidine, and documents the tug-of-war between reversible pocket fit and covalent
reactivity: molecules with good reactivity but poor fit, and the reverse. The optimized drug lands
at 604 Da, over the Lipinski 500 Da ceiling (Fell 2020).

**Stanza's counterpart.** The KRAS G12C `SiteGuidance` widens the drug-likeness pre-filter to a
430-620 Da window and quotes real masses (sotorasib 560.6, adagrasib 604.1) so the generator
anchors on a number instead of undershooting. Under a standard 500 Da ceiling the pre-filter
deletes both marketed switch-II drugs, and adagrasib fails the QED floor besides
(`services/known_sites.go`).

**What the comparison licenses.** The campaign is direct, external evidence that the useful mass
for this pocket exceeds the Lipinski heuristic, which is precisely why Stanza's window is set to
admit the drugs it is benchmarked against rather than tuned after the fact. Stanza does not model
the affinity-versus-reactivity balance Fell describes: Vina scores non-covalently and is blind to
the kinetics, so the window is a gate on what the generator proposes, never a prediction of potency.

## 3. Ponatinib (AP24534): the ethynyl wire past the gatekeeper

**The campaign.** ARIAD designed ponatinib to survive the T315I gatekeeper that defeats imatinib.
Imatinib clashes with the bulky Ile315 side chain and loses the hydrogen bond Thr315 donated to its
anilino NH. Ponatinib threads past the mutant residue on a rigid carbon-carbon triple bond, an
ethynyl linker chosen as a narrow spacer that skirts the steric bulk without clashing (Huang 2010).

**Stanza's counterpart.** The ABL T315I control docks both drugs into the matched wild-type /
mutant pair over shared box and seeds. It reports imatinib -0.35 (defeated), ponatinib +0.45
(survives), a separation of 0.80 kcal/mol against 0.13 of seed noise, after imatinib redocks to its
1IEP crystal pose at 1.07 Å (see [`11-abl-t315i-positive-control.md`](11-abl-t315i-positive-control.md)).

**What the comparison licenses.** Stanza recovers the sign and ordering ARIAD's design implies:
ponatinib tolerates T315I and imatinib does not, on a target it was never tuned for. You can load
ponatinib's top pose in the Mol\* viewer and see qualitatively whether the ethynyl linker threads
past residue 315 as designed. It cannot claim the magnitude: the recovered 0.35 is under a tenth of
the roughly 4 kcal/mol experimental penalty, because a rigid single frame is blind to the
conformational component of T315I resistance, which destabilises the DFG-out state imatinib needs.
Stanza sees the steric bump ARIAD routed around; it does not see the conformational selection they
also exploited.

## For a presentation

The honest, strong version of the slide a reviewer proposed:

> Amgen optimized this warhead trajectory by hand over roughly two years (Lanman 2020). Stanza's
> agentic loop proposes novel scaffolds and flags in seconds which can plausibly reach Cys12 along
> the same Bürgi–Dunitz path: a triage step whose geometry and error bars are auditable, and whose
> disagreements with the stronger structural check are surfaced rather than hidden.

That claims what Stanza does (fast, auditable triage grounded in the same criteria) without
claiming what it does not (that it recovered an optimized drug's structure).

## References

- Lanman et al. 2020, *J. Med. Chem.* 63:52. Sotorasib (AMG 510) discovery; switch-II
  conformational locking to satisfy the warhead trajectory.
- Fell et al. 2020, *J. Med. Chem.* 63:6679. Adagrasib (MRTX849) discovery; the reversible-affinity
  versus covalent-reactivity balance; 604 Da beyond Lipinski.
- Huang et al. 2010, *J. Med. Chem.* 53:4701. Ponatinib (AP24534) discovery; the ethynyl linker
  designed to bypass the T315I gatekeeper.
