package services

import (
	"context"
	"math"
	"os/exec"
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

func TestCovalentCreditDecaysWithReach(t *testing.T) {
	p := DefaultCovalentParams()
	// At or inside the ideal reach, full credit.
	if got := covalentCredit(p.ReachIdeal, p); got != p.MaxCredit {
		t.Errorf("credit at ideal reach = %v, want %v", got, p.MaxCredit)
	}
	if got := covalentCredit(p.ReachIdeal-1, p); got != p.MaxCredit {
		t.Errorf("credit inside ideal reach = %v, want %v", got, p.MaxCredit)
	}
	// At or beyond the max reach, no credit.
	if got := covalentCredit(p.ReachMax, p); got != 0 {
		t.Errorf("credit at max reach = %v, want 0", got)
	}
	if got := covalentCredit(p.ReachMax+5, p); got != 0 {
		t.Errorf("credit beyond max reach = %v, want 0", got)
	}
	// Monotonic non-increasing between ideal and max.
	prev := math.Inf(1)
	for d := p.ReachIdeal; d <= p.ReachMax; d += 0.1 {
		got := covalentCredit(d, p)
		if got > prev+1e-9 {
			t.Fatalf("credit not monotonic: reach %.2f gave %v after %v", d, got, prev)
		}
		if got < 0 || got > p.MaxCredit {
			t.Fatalf("credit %v out of [0, %v] at reach %.2f", got, p.MaxCredit, d)
		}
		prev = got
	}
}

func TestCovalentCreditMidpoint(t *testing.T) {
	p := CovalentParams{ReachIdeal: 3.5, ReachMax: 5.5, MaxCredit: 4.0}
	// Halfway through the window → half credit.
	if got := covalentCredit(4.5, p); math.Abs(got-2.0) > 1e-9 {
		t.Errorf("credit at window midpoint = %v, want 2.0", got)
	}
}

func TestCovalentCreditDegenerateParams(t *testing.T) {
	// A zero/blank window must yield no credit rather than dividing by zero.
	if got := covalentCredit(3.0, CovalentParams{}); got != 0 {
		t.Errorf("credit with zero params = %v, want 0", got)
	}
	// Inverted window (max ≤ ideal) yields no credit.
	if got := covalentCredit(3.0, CovalentParams{ReachIdeal: 5, ReachMax: 3, MaxCredit: 4}); got != 0 {
		t.Errorf("credit with inverted window = %v, want 0", got)
	}
	// Non-positive credit ceiling yields no credit.
	if got := covalentCredit(3.0, CovalentParams{ReachIdeal: 3, ReachMax: 5, MaxCredit: 0}); got != 0 {
		t.Errorf("credit with zero ceiling = %v, want 0", got)
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
