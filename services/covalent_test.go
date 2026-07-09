package services

import (
	"math"
	"testing"
)

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
