package services

import (
	"context"
	"math"
	"os/exec"
	"slices"
	"testing"
)

// skipUnlessCovalent skips the test unless python3 and the RDKit covalent helper are
// runnable. covalentScript is resolved relative to the server's working directory
// (the repo root), so the test moves there for its duration — otherwise `go test`,
// which runs in the package directory, would never find the script and every one of
// these tests would silently skip.
func skipUnlessCovalent(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	t.Chdir("..")
	probe := exec.Command("python3", covalentScript, "detect", "--smiles", "CC")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("RDKit covalent helper not runnable (try `pip install -r scripts/requirements.txt`): %v: %s", err, out)
	}
}

// The warhead SMARTS must recognise substituted Michael acceptors, not just the
// textbook terminal acrylamide. A pattern demanding a terminal =CH2 silently misses
// afatinib-style dimethylaminocrotonamides and β-substituted cyanoacrylamides, which
// are real cysteine warheads — and a missed warhead looks exactly like a molecule
// that simply is not covalent.
func TestHasCovalentWarheadRecognisesWarheadClasses(t *testing.T) {
	skipUnlessCovalent(t)
	cases := []struct {
		name   string
		smiles string
		class  string
	}{
		{"terminal acrylamide", "C=CC(=O)N1CCN(c2ncnc3cc(F)c(N)cc23)CC1", "acrylamide"},
		{"haloacetamide", "O=C(CCl)N1CCN(c2cc(-c3cccc(O)c3)nc3[nH]ccc23)CC1", "haloacetamide"},
		{"propiolamide", "C#CC(=O)N1CCC(Oc2ncnc3[nH]cc(-c4cccnc4)c23)CC1", "propiolamide"},
		{"vinyl sulfonamide", "C=CS(=O)(=O)N1CCC(Nc2ncc(Cl)c(-c3cnn(C)c3)n2)CC1", "vinyl_sulfonamide"},
		// β-substituted: previously undetected.
		{"afatinib-style crotonamide", "CN(C)C/C=C/C(=O)N1CCC(c2nc3cc(O)ccc3c(=O)n2C)CC1", "unsaturated_amide"},
		{"β-cyano acrylamide", `N#C/C=C(\C(=O)N1CCN(c2nc3ccccc3nc2N)CC1)c1ccc(F)cc1`, "unsaturated_amide"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			has, class, err := HasCovalentWarhead(context.Background(), tc.smiles)
			if err != nil {
				t.Fatalf("HasCovalentWarhead: %v", err)
			}
			if !has {
				t.Fatalf("%s: has_warhead = false, want true", tc.smiles)
			}
			if class != tc.class {
				t.Errorf("%s: class = %q, want %q", tc.smiles, class, tc.class)
			}
		})
	}
}

// Saturated amides and esters are not electrophiles: flagging them would hand a
// covalent credit to molecules that cannot bond the thiol at all.
func TestHasCovalentWarheadRejectsNonElectrophiles(t *testing.T) {
	skipUnlessCovalent(t)
	for _, smiles := range []string{
		"CC(=O)N1CCN(c2ncnc3cc(F)c(N)cc23)CC1", // the acrylamide's saturated control
		"c1ccccc1C(=O)N1CCCC1",                 // aromatic amide
		"CCOC(=O)c1ccccc1",                     // benzoate ester
		"CCO",                                  // ethanol
	} {
		has, class, err := HasCovalentWarhead(context.Background(), smiles)
		if err != nil {
			t.Fatalf("HasCovalentWarhead(%s): %v", smiles, err)
		}
		if has {
			t.Errorf("%s: has_warhead = true (class %q), want false", smiles, class)
		}
	}
}

func TestIsCovalentTarget(t *testing.T) {
	for _, res := range []string{"CYS", "cys", " Cys ", "Cys"} {
		if !isCovalentTarget(res) {
			t.Errorf("isCovalentTarget(%q) = false, want true", res)
		}
	}
	// Only cysteine reacts with the current warhead set.
	for _, res := range []string{"GLY", "SER", "LYS", "ALA", ""} {
		if isCovalentTarget(res) {
			t.Errorf("isCovalentTarget(%q) = true, want false", res)
		}
	}
}

func TestMedianAndSpread(t *testing.T) {
	xs := []float64{4.51, 3.78, 4.09, 3.90, 4.12}
	if got := median(xs); got != 4.09 {
		t.Errorf("median = %v, want 4.09", got)
	}
	if got := spread(xs); math.Abs(got-0.73) > 1e-9 {
		t.Errorf("spread = %v, want 0.73", got)
	}
	// median must not mutate the caller's slice — reaches are reused for the spread.
	if xs[0] != 4.51 {
		t.Errorf("median reordered the input slice: %v", xs)
	}
}

// Vina is a minimiser: its affinity estimates a global minimum, so the deepest pose any
// seed found is the best available estimate of it. The seed that found it is the one whose
// pose the viewer shows and the tether is built from.
func TestBestReplicatePicksTheDeepestPose(t *testing.T) {
	reps := []replicate{
		{seed: 1, affinity: -9.5}, // deepest
		{seed: 2, affinity: -7.1},
		{seed: 3, affinity: -8.3},
		{seed: 4, affinity: -8.9},
		{seed: 5, affinity: -7.7},
	}
	got := bestReplicate(reps)
	if got.seed != 1 {
		t.Errorf("bestReplicate seed = %d, want 1 (affinity -9.5)", got.seed)
	}
	if reps[0].seed != 1 || reps[1].seed != 2 {
		t.Error("bestReplicate reordered the caller's slice")
	}
}

// A covalent call that flips with the RNG is indistinguishable from its neighbours —
// not better, not worse — so ranking it on a median would launder noise into signal.
// The audit measured exactly this: with the ligand conformer held fixed, warhead reach
// varied ±0.16–1.09 Å over five seeds, and on one molecule the call flipped between
// feasible and infeasible on the RNG alone. `services` (DockLigandDualTrack) flags such
// a molecule with the predicate asserted here — a feasibility sample that straddles zero
// across seeds — and this test pins that predicate so the flag cannot silently regress
// into ranking a coin toss.
func TestSeedStraddlingFeasibilityIsUncertain(t *testing.T) {
	// Mirror services/dual_dock.go verbatim: a call is uncertain when some seeds place
	// the warhead where it can attack (feasibility > 0) and others do not (≤ 0).
	uncertain := func(f []float64) bool {
		return slices.Min(f) <= 0 && slices.Max(f) > 0
	}
	straddling := []float64{0.0, 0.82, 0.0, 0.77, 0.80} // feasible under 3 seeds, not under 2
	stable := []float64{0.71, 0.82, 0.77, 0.80, 0.74}   // feasible under every seed
	if !uncertain(straddling) {
		t.Error("a feasibility that straddles zero across seeds must be flagged uncertain")
	}
	if uncertain(stable) {
		t.Error("a feasibility positive under every seed must not be flagged uncertain")
	}
}

// Both tracks must be replicated over the same odd, distinct seed list. Odd so the
// median is unambiguous; at least three so a single outlying seed is outvoted; shared so
// a WT/mutant affinity difference can only come from the receptor and not from the search.
//
// A single-seed track is not safe, even for an affinity. Vina's search occasionally lands
// in a bad local minimum, per (molecule, receptor, seed). Measured on
// C=C(F)C(=O)N1CCN(c2nc(-c3cccc4c(O)cccc34)nc3c2ncn3C)CC1: seed 42 scored the wild-type
// pocket at −8.75 kcal/mol where four other seeds agreed on −9.8, and the mutant's seed
// 1337 scored −7.84 against a −9.86 consensus. The mutant's median discarded its outlier;
// the wild type, then docked once, reported its own as fact — and the run published a
// +1.03 kcal/mol selectivity for a molecule whose median-of-three margin is +0.09.
func TestScreenSeedsAreOddDistinctAndSharedByBothTracks(t *testing.T) {
	if len(screenSeeds) < 3 {
		t.Fatalf("only %d replicate seeds; one bad local minimum cannot be outvoted", len(screenSeeds))
	}
	if len(screenSeeds)%2 == 0 {
		t.Errorf("%d seeds: an even count makes the median ambiguous", len(screenSeeds))
	}
	seen := map[int]bool{}
	for _, s := range screenSeeds {
		if seen[s] {
			t.Errorf("duplicate replicate seed %d", s)
		}
		seen[s] = true
	}
}

// A SHALLOW outlying seed must never be reported. This is the exact wild-type sample that
// produced the phantom +1.03 selectivity: two seeds agreeing near −9.8, and seed 42 a full
// kcal/mol shallower. A minimum discards it for free — a shallow pose is never the deepest
// one — which is why best-of-seeds fixes this case as well as the median ever did.
func TestBestOfSeedsRejectsAShallowOutlier(t *testing.T) {
	wt := []replicate{
		{seed: 42, affinity: -8.75}, // the outlier a single-seed track would have reported
		{seed: 1337, affinity: -9.80},
		{seed: 7, affinity: -9.77},
	}
	got := bestReplicate(wt).affinity
	if got != -9.80 {
		t.Errorf("best affinity = %v, want -9.80 (the shallow outlier -8.75 must not be reported)", got)
	}
	// Selectivity against the mutant's own best (-9.86) is +0.06, not +1.03.
	if sel := round2(got - (-9.86)); sel != 0.06 {
		t.Errorf("selectivity = %v, want 0.06", sel)
	}
}

// The case the median could not survive, in the numbers that exposed it.
//
// One molecule reported selectivity +2.39 against Gly12→Cys12 — a mutation that cannot
// change reversible binding. Docking it with seven seeds per track showed BOTH tracks were
// bimodal: the mutant found its deep basin (≈ −9.35) in five seeds of seven, the wild type
// found its own deep basin (−9.23) in one of seven. The pockets bind this ligand to within
// 0.19 kcal/mol. The median reported whichever basin the search happened to prefer, and
// manufactured 2.2 kcal/mol of selectivity out of that asymmetry.
func TestBestOfSeedsResolvesABimodalWildTypeTrack(t *testing.T) {
	// Measured, not invented: exhaustiveness 16, cpu 2, seeds as labelled.
	wt := []replicate{
		{seed: 42, affinity: -6.93}, {seed: 1337, affinity: -7.46}, {seed: 7, affinity: -7.47},
		{seed: 2024, affinity: -7.45}, {seed: 101, affinity: -6.93}, {seed: 555, affinity: -6.96},
		{seed: 909, affinity: -9.23}, // the only seed that found the deep wild-type pose
	}
	mut := []replicate{
		{seed: 42, affinity: -7.05}, {seed: 1337, affinity: -9.38}, {seed: 7, affinity: -9.33},
		{seed: 2024, affinity: -9.42}, {seed: 101, affinity: -7.14}, {seed: 555, affinity: -9.30},
		{seed: 909, affinity: -9.37},
	}

	sel := round2(bestReplicate(wt).affinity - bestReplicate(mut).affinity)
	if sel != 0.19 {
		t.Errorf("best-of-seeds selectivity = %+.2f, want +0.19 (≈0, as a covalent target requires)", sel)
	}

	// The spread is what tells a reader this molecule was searched badly. Without it the
	// margin above is indistinguishable from a margin measured on a unimodal ligand.
	if s := round2(spread(affinities(wt))); s != 2.30 {
		t.Errorf("wild-type spread = %.2f, want 2.30", s)
	}

	// And the median, on the very seeds the pipeline shipped, disagrees with physics.
	medSel := round2(median(affinities(wt[:3])) - median(affinities(mut[:3])))
	if medSel < 1.5 {
		t.Errorf("median-of-3 selectivity = %+.2f; the regression this test guards produced ~+1.9", medSel)
	}
}

// Replicates only ever stabilise the covalent geometry, so a molecule that cannot bond
// the target must not pay for them. A detection failure, though, must fall back to
// assessing (and therefore replicating): a silent "no warhead" is how a broken
// measurement passes for a molecule that simply is not covalent.
func TestLigandHasWarheadFailsOpen(t *testing.T) {
	skipUnlessCovalent(t)
	if !ligandHasWarhead(context.Background(), "C=CC(=O)N1CCNCC1") {
		t.Error("acrylamide reported as having no warhead")
	}
	if ligandHasWarhead(context.Background(), "CC(=O)N1CCNCC1") {
		t.Error("saturated amide reported as having a warhead")
	}
	// An unparseable SMILES makes the helper error; it must fail open, not silently
	// declare the molecule non-covalent.
	if !ligandHasWarhead(context.Background(), "this is not a smiles") {
		t.Error("a failed warhead detection must fail open, not report 'no warhead'")
	}
}
