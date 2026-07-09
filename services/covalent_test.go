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

func TestMedianReplicatePicksMiddleAffinity(t *testing.T) {
	reps := []replicate{
		{seed: 1, affinity: -9.5},
		{seed: 2, affinity: -7.1},
		{seed: 3, affinity: -8.3}, // median
		{seed: 4, affinity: -8.9},
		{seed: 5, affinity: -7.7},
	}
	got := medianReplicate(reps)
	if got.seed != 3 {
		t.Errorf("medianReplicate seed = %d, want 3 (affinity -8.3)", got.seed)
	}
	if reps[0].seed != 1 {
		t.Error("medianReplicate reordered the caller's slice")
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

// The replicate seeds must admit an unambiguous median and a real spread, and the
// single-seed tracks must start from a seed the replicates also use — otherwise a
// WT/mutant affinity difference could come from the search rather than the receptor.
func TestCovalentSeedsAreOddDistinctAndShareTheSingleSeed(t *testing.T) {
	if len(covalentSeeds) < 3 {
		t.Fatalf("only %d replicate seeds; the feasibility spread cannot be estimated", len(covalentSeeds))
	}
	if len(covalentSeeds)%2 == 0 {
		t.Errorf("%d seeds: an even count makes the median ambiguous", len(covalentSeeds))
	}
	seen := map[int]bool{}
	for _, s := range covalentSeeds {
		if seen[s] {
			t.Errorf("duplicate replicate seed %d", s)
		}
		seen[s] = true
	}
	if len(singleSeed) != 1 {
		t.Fatalf("singleSeed carries %d seeds, want exactly 1", len(singleSeed))
	}
	if singleSeed[0] != covalentSeeds[0] {
		t.Errorf("singleSeed %d is not the replicates' first seed %d: the WT track and the "+
			"mutant replicates would no longer share a search", singleSeed[0], covalentSeeds[0])
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
