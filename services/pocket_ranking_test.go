package services

import (
	"testing"

	"github.com/ayush00git/stanza/models"
)

// pocket is a terse constructor for ranking tests.
func pocket(id int, drug float64, center [3]float64, chain string, residues ...int) models.Pocket {
	chains := make([]string, len(residues))
	for i := range chains {
		chains[i] = chain
	}
	return models.Pocket{
		PocketID:       id,
		Score:          drug,
		Center:         center,
		ResidueIndices: residues,
		ResidueChains:  chains,
	}
}

func TestDruggabilityWeightDoesNotAnnihilateCrypticPockets(t *testing.T) {
	// A cryptic pocket scores 0.00 druggability on an apo structure. A bare product
	// would zero its site score and make it unselectable no matter how close it sits
	// to the mutation; the floor keeps it in contention.
	if got := druggabilityWeight(0); got <= 0 {
		t.Fatalf("druggabilityWeight(0) = %v, want > 0", got)
	}
	if got := druggabilityWeight(0); got != druggabilityFloor {
		t.Errorf("druggabilityWeight(0) = %v, want floor %v", got, druggabilityFloor)
	}
	if got := druggabilityWeight(1); got != 1 {
		t.Errorf("druggabilityWeight(1) = %v, want 1", got)
	}
	// Clamped, not extrapolated.
	if got := druggabilityWeight(2.5); got != 1 {
		t.Errorf("druggabilityWeight(2.5) = %v, want 1", got)
	}
	if got := druggabilityWeight(-1); got != druggabilityFloor {
		t.Errorf("druggabilityWeight(-1) = %v, want floor", got)
	}
}

func TestSiteScorePrefersCloserPocketAtEqualDruggability(t *testing.T) {
	res := [3]float64{0, 0, 0}
	near := pocket(1, 0.5, [3]float64{3, 0, 0}, "A", 12)
	far := pocket(2, 0.5, [3]float64{12, 0, 0}, "A", 12)
	if siteScore(near, res) <= siteScore(far, res) {
		t.Fatalf("near pocket must outscore far pocket at equal druggability")
	}
}

func TestSiteScorePrefersDruggablePocketAtEqualDistance(t *testing.T) {
	res := [3]float64{0, 0, 0}
	drug := pocket(1, 0.9, [3]float64{5, 0, 0}, "A", 12)
	undrug := pocket(2, 0.0, [3]float64{0, 5, 0}, "A", 12)
	if siteScore(drug, res) <= siteScore(undrug, res) {
		t.Fatalf("druggable pocket must outscore undruggable one at equal distance")
	}
}

// Proximity must be able to overcome druggability: a pocket that truly surrounds
// the mutation beats a more druggable pocket that only grazes it from far away.
// This is the failure the old first-match-by-druggability scan produced.
func TestProximityCanOutrankRawDruggability(t *testing.T) {
	res := [3]float64{0, 0, 0}
	rim := pocket(1, 0.63, [3]float64{20, 0, 0}, "A", 12, 13, 15, 16)
	surrounding := pocket(9, 0.00, [3]float64{2, 0, 0}, "A", 12, 61, 68, 95)

	got := selectByProximity([]models.Pocket{rim, surrounding}, "A", 12, res)
	if got == nil || got.PocketID != 9 {
		t.Fatalf("selectByProximity picked %v, want pocket 9 (surrounds the mutation)", got)
	}
}

// The old behaviour: pockets arrive sorted by druggability and the first one that
// merely lists the residue wins. Ranking must not reproduce that when the leading
// pocket is far away.
func TestSelectByProximityIgnoresSliceOrder(t *testing.T) {
	res := [3]float64{0, 0, 0}
	// Deliberately place the far, druggable pocket first, as fpocket would.
	pockets := []models.Pocket{
		pocket(1, 0.63, [3]float64{18, 0, 0}, "A", 12),
		pocket(9, 0.10, [3]float64{1, 0, 0}, "A", 12),
	}
	if got := selectByProximity(pockets, "A", 12, res); got.PocketID != 9 {
		t.Fatalf("got pocket %d, want 9 — selection must not depend on slice order", got.PocketID)
	}
}

func TestSelectByProximityOnlyConsidersPocketsLiningTheResidue(t *testing.T) {
	res := [3]float64{0, 0, 0}
	// A very close, very druggable pocket that does NOT line residue 12 must lose to
	// a farther pocket that does.
	closeButUnrelated := pocket(1, 1.0, [3]float64{1, 0, 0}, "A", 40, 41)
	lining := pocket(2, 0.1, [3]float64{7, 0, 0}, "A", 12, 13)
	if got := selectByProximity([]models.Pocket{closeButUnrelated, lining}, "A", 12, res); got.PocketID != 2 {
		t.Fatalf("got pocket %d, want 2 — pockets lining the residue are preferred as a group", got.PocketID)
	}
}

// An allosteric/surface mutation lines no pocket at all; selection widens to nearby
// pockets rather than returning nil.
func TestSelectByProximityFallsBackWhenNoPocketLinesResidue(t *testing.T) {
	res := [3]float64{0, 0, 0}
	a := pocket(1, 0.2, [3]float64{6, 0, 0}, "A", 40)
	b := pocket(2, 0.2, [3]float64{30, 0, 0}, "A", 80)
	got := selectByProximity([]models.Pocket{a, b}, "A", 12, res)
	if got == nil || got.PocketID != 1 {
		t.Fatalf("got %v, want nearest pocket 1 within the search radius", got)
	}
}

// Beyond the search radius nothing is nearby, but a non-empty pocket list must
// still yield a pocket rather than nil.
func TestSelectByProximityNeverReturnsNilForNonEmptyInput(t *testing.T) {
	res := [3]float64{0, 0, 0}
	far := pocket(3, 0.4, [3]float64{100, 0, 0}, "A", 77)
	if got := selectByProximity([]models.Pocket{far}, "A", 12, res); got == nil {
		t.Fatal("selectByProximity returned nil for a non-empty pocket list")
	}
	if got := selectByProximity(nil, "A", 12, res); got != nil {
		t.Fatal("selectByProximity must return nil for an empty pocket list")
	}
}

func TestRankResistanceCandidatesIsDeterministicOnTies(t *testing.T) {
	res := [3]float64{0, 0, 0}
	// Identical score, different IDs: the lower ID must win, every time.
	a := pocket(7, 0.5, [3]float64{4, 0, 0}, "A", 12)
	b := pocket(3, 0.5, [3]float64{4, 0, 0}, "A", 12)
	for range 50 {
		ranked := rankResistanceCandidates([]*models.Pocket{&a, &b}, res)
		if ranked[0].pocket.PocketID != 3 {
			t.Fatalf("tie broke to pocket %d, want 3 (lowest ID)", ranked[0].pocket.PocketID)
		}
	}
}

func TestPocketHasResidueRespectsChain(t *testing.T) {
	p := pocket(1, 0.5, [3]float64{}, "B", 12)
	if pocketHasResidue(&p, "A", 12) {
		t.Error("residue 12 on chain B must not match chain A")
	}
	if !pocketHasResidue(&p, "B", 12) {
		t.Error("residue 12 on chain B must match chain B")
	}
}
