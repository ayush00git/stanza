package scoring

import (
	"math"
	"testing"

	"github.com/ayush00git/stanza/models"
)

// dock builds a dual-track dock result; Selectivity follows the wt−mut convention.
func dock(smiles string, mut, wt float64) models.LigandDock {
	return models.LigandDock{
		SMILES:      smiles,
		MutantScore: mut,
		WTScore:     wt,
		Selectivity: wt - mut,
	}
}

func rankOf(r Ranking, smiles string) int {
	for _, m := range r.Ranked {
		if m.SMILES == smiles {
			return m.Rank
		}
	}
	return -1
}

// The selective molecule (binds mutant hard, WT weakly) must outrank the
// non-selective one (binds both, WT slightly better) under default weights.
func TestScoreAndRankSelectivityWins(t *testing.T) {
	docks := []models.LigandDock{
		dock("selective", -9.2, -5.1),    // selectivity +4.1
		dock("nonselective", -8.0, -8.4), // selectivity −0.4
	}
	qed := map[string]float64{"selective": 0.74, "nonselective": 0.55}

	r := ScoreAndRank("run", docks, qed, Options{})

	if r.Count != 2 || len(r.Ranked) != 2 {
		t.Fatalf("expected 2 ranked, got count=%d len=%d", r.Count, len(r.Ranked))
	}
	if got := rankOf(r, "selective"); got != 1 {
		t.Errorf("selective molecule rank = %d, want 1", got)
	}
	// Selectivity passes through exactly as wt − mut.
	if s := r.Ranked[0].Scores.Selectivity; math.Abs(s-4.1) > 1e-9 {
		t.Errorf("selectivity = %v, want 4.1", s)
	}
	// Weights are normalised to sum 1.
	w := r.Weights
	if sum := w.Potency + w.Selectivity + w.DrugLikeness; math.Abs(sum-1) > 1e-9 {
		t.Errorf("weights sum = %v, want 1", sum)
	}
}

// Cranking the potency weight to 1 must promote the most potent mutant binder even
// when it is less selective — proving weights re-order deterministically.
func TestScoreAndRankWeightsReorder(t *testing.T) {
	docks := []models.LigandDock{
		dock("selective", -7.0, -2.0),  // sel +5.0, potency 7.0
		dock("potent", -11.0, -8.0),    // sel +3.0, potency 11.0
	}

	sel := ScoreAndRank("run", docks, nil, Options{Weights: FitnessWeights{Selectivity: 1}})
	if got := rankOf(sel, "selective"); got != 1 {
		t.Errorf("selectivity-weighted: selective rank = %d, want 1", got)
	}

	pot := ScoreAndRank("run", docks, nil, Options{Weights: FitnessWeights{Potency: 1}})
	if got := rankOf(pot, "potent"); got != 1 {
		t.Errorf("potency-weighted: potent rank = %d, want 1", got)
	}
}

// A pool of one must yield a finite fitness (no NaN/Inf from σ=0), rank 1.
func TestScoreAndRankPoolOfOne(t *testing.T) {
	r := ScoreAndRank("run", []models.LigandDock{dock("solo", -8.0, -4.0)}, map[string]float64{"solo": 0.6}, Options{})
	if len(r.Ranked) != 1 {
		t.Fatalf("expected 1 ranked, got %d", len(r.Ranked))
	}
	f := r.Ranked[0].Scores.Fitness
	if f == nil || math.IsNaN(*f) || math.IsInf(*f, 0) {
		t.Errorf("pool-of-one fitness = %v, want a finite number", f)
	}
	if r.Ranked[0].Rank != 1 {
		t.Errorf("rank = %d, want 1", r.Ranked[0].Rank)
	}
}

// zscore and minmax must agree on ordering when a single term drives the ranking
// (both are monotonic transforms of the same values).
func TestScoreAndRankNormModesSameOrdering(t *testing.T) {
	docks := []models.LigandDock{
		dock("a", -8.0, -6.0), // sel +2
		dock("b", -8.0, -4.0), // sel +4
		dock("c", -8.0, -7.0), // sel +1
	}
	onlySel := Options{Weights: FitnessWeights{Selectivity: 1}}

	z := onlySel
	z.Norm = NormZScore
	m := onlySel
	m.Norm = NormMinMax

	rz := ScoreAndRank("run", docks, nil, z)
	rm := ScoreAndRank("run", docks, nil, m)

	for _, smi := range []string{"a", "b", "c"} {
		if rankOf(rz, smi) != rankOf(rm, smi) {
			t.Errorf("norm modes disagree on %q: zscore=%d minmax=%d", smi, rankOf(rz, smi), rankOf(rm, smi))
		}
	}
	// b has the largest margin → rank 1 under both.
	if rankOf(rz, "b") != 1 {
		t.Errorf("b rank = %d, want 1", rankOf(rz, "b"))
	}
}

// A molecule with no known QED is kept (not dropped), its QED substituted with the
// pool minimum, and its scorecard reports a nil QED pointer.
func TestScoreAndRankMissingQED(t *testing.T) {
	docks := []models.LigandDock{
		dock("known", -9.0, -5.0),
		dock("noqed", -9.0, -5.0),
	}
	r := ScoreAndRank("run", docks, map[string]float64{"known": 0.8}, Options{})

	if len(r.Ranked) != 2 {
		t.Fatalf("expected both molecules ranked, got %d", len(r.Ranked))
	}
	for _, m := range r.Ranked {
		if m.SMILES == "noqed" && m.Scores.QED != nil {
			t.Errorf("noqed: QED = %v, want nil", *m.Scores.QED)
		}
		if m.SMILES == "known" && (m.Scores.QED == nil || *m.Scores.QED != 0.8) {
			t.Errorf("known: QED = %v, want 0.8", m.Scores.QED)
		}
	}
}

func TestScoreAndRankEmpty(t *testing.T) {
	r := ScoreAndRank("run", nil, nil, Options{})
	if len(r.Ranked) != 0 || r.Count != 0 {
		t.Errorf("empty: got count=%d len=%d, want 0/0", r.Count, len(r.Ranked))
	}
	if r.Ranked == nil || r.Excluded == nil {
		t.Errorf("empty: ranked/excluded should be non-nil slices for a clean JSON []")
	}
}

func TestZScoreAndMinMaxGuards(t *testing.T) {
	// All-equal → zscore all zeros, minmax all 0.5 (no NaN).
	eq := []float64{3, 3, 3}
	for i, v := range zscore(eq) {
		if v != 0 {
			t.Errorf("zscore all-equal[%d] = %v, want 0", i, v)
		}
	}
	for i, v := range minmax(eq) {
		if v != 0.5 {
			t.Errorf("minmax all-equal[%d] = %v, want 0.5", i, v)
		}
	}
	// minmax maps min→0, max→1.
	mm := minmax([]float64{2, 4, 6})
	if mm[0] != 0 || mm[2] != 1 {
		t.Errorf("minmax range = %v, want [0, .5, 1]", mm)
	}
}
