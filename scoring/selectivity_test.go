package scoring

import (
	"encoding/json"
	"math"
	"strings"
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

// covDock builds a warhead-bearing dock: same dual-track scores as dock, plus a
// covalent geometry record. feas is the measured 0–1 attack feasibility; uncertain
// marks a call that flipped with the docking seed.
func covDock(smiles string, mut, wt, feas float64, uncertain bool) models.LigandDock {
	d := dock(smiles, mut, wt)
	d.Covalent = &models.CovalentDock{
		TargetResidue: "Cys12",
		WarheadType:   "acrylamide",
		Status:        models.CovalentFeasible,
		Feasibility:   feas,
		Uncertain:     uncertain,
	}
	return d
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
	// Weights are normalised to sum 1 across all four terms.
	w := r.Weights
	if sum := w.Potency + w.Selectivity + w.DrugLikeness + w.CovalentFeasibility; math.Abs(sum-1) > 1e-9 {
		t.Errorf("weights sum = %v, want 1", sum)
	}
}

// Cranking the potency weight to 1 must promote the most potent mutant binder even
// when it is less selective — proving weights re-order deterministically.
func TestScoreAndRankWeightsReorder(t *testing.T) {
	docks := []models.LigandDock{
		dock("selective", -7.0, -2.0), // sel +5.0, potency 7.0
		dock("potent", -11.0, -8.0),   // sel +3.0, potency 11.0
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

// The weight vector spans four terms now; it must renormalise to a simplex including
// the covalent feasibility term (or covalent evidence gets silently mis-counted), and
// non-positive weights must fall back to the documented defaults rather than dividing
// by zero and poisoning every fitness with NaN.
func TestFitnessWeightsNormalizeAndFallback(t *testing.T) {
	w := normalizeWeights(FitnessWeights{Potency: 3, Selectivity: 1, DrugLikeness: 2, CovalentFeasibility: 4})
	if sum := w.Potency + w.Selectivity + w.DrugLikeness + w.CovalentFeasibility; math.Abs(sum-1) > 1e-9 {
		t.Errorf("normalised weight sum = %v, want 1", sum)
	}
	if got := normalizeWeights(FitnessWeights{}); got != DefaultWeights() {
		t.Errorf("zero weights fell back to %+v, want defaults %+v", got, DefaultWeights())
	}
	// The default split must itself already sum to 1 so a caller that passes it through
	// unchanged sees no rescaling.
	d := DefaultWeights()
	if sum := d.Potency + d.Selectivity + d.DrugLikeness + d.CovalentFeasibility; math.Abs(sum-1) > 1e-9 {
		t.Errorf("DefaultWeights sum = %v, want 1", sum)
	}
}

// A run of purely non-covalent molecules must rank identically to the pre-covalent
// scorer: with no warheads in the pool every feasibility is 0, so the covalent term
// zeroes out and contributes nothing to fitness. We assert the exact fitness equals the
// three non-covalent terms alone. This is the pan-KRAS reality — non-covalent binders
// carry no covalent evidence, and the leaderboard must not invent any.
func TestScoreAndRankNoCovalentTermInert(t *testing.T) {
	docks := []models.LigandDock{
		dock("a", -9.0, -5.0), // sel +4, potency 9
		dock("b", -7.0, -6.0), // sel +1, potency 7
	}
	qed := map[string]float64{"a": 0.8, "b": 0.4}
	r := ScoreAndRank("run", docks, qed, Options{})

	// Two-molecule zscore sends the larger of each term to +1 and the smaller to −1, so
	// molecule "a" (better on potency, selectivity and QED) has fitness w_p+w_s+w_q with
	// the covalent term contributing exactly 0.
	w := r.Weights
	wantA := w.Potency + w.Selectivity + w.DrugLikeness
	fa := r.Ranked[rankOf(r, "a")-1].Scores.Fitness
	if fa == nil || math.Abs(*fa-wantA) > 1e-9 {
		t.Errorf("a fitness = %v, want %v (covalent term must add 0)", fa, wantA)
	}
	if rankOf(r, "a") != 1 {
		t.Errorf("a rank = %d, want 1", rankOf(r, "a"))
	}
	// No covalent record → no reported feasibility.
	for _, m := range r.Ranked {
		if m.Scores.CovalentFeasibility != nil {
			t.Errorf("%s: CovalentFeasibility = %v, want nil for a non-covalent molecule", m.SMILES, *m.Scores.CovalentFeasibility)
		}
	}
}

// A warhead whose electrophile can reach the thiol (feasible) must outrank one that
// carries a warhead but cannot attack (infeasible) when the two are otherwise identical
// non-covalent binders. Feasibility is the whole point of a covalent inhibitor, so it
// has to be the deciding term when nothing else separates the pair.
func TestScoreAndRankFeasibleBeatsInfeasible(t *testing.T) {
	docks := []models.LigandDock{
		covDock("infeasible", -8.0, -8.0, 0.0, false),
		covDock("feasible", -8.0, -8.0, 0.9, false),
	}
	r := ScoreAndRank("run", docks, nil, Options{})
	if got := rankOf(r, "feasible"); got != 1 {
		t.Errorf("feasible rank = %d, want 1", got)
	}
	// Both are covalent, so both report their measured feasibility.
	for _, m := range r.Ranked {
		if m.Scores.CovalentFeasibility == nil {
			t.Errorf("%s: CovalentFeasibility = nil, want the measured value reported", m.SMILES)
		}
	}
}

// A molecule whose covalent call flips with the docking seed (Uncertain) must NOT
// outrank a stably feasible warhead that has the SAME median feasibility. Ranking a
// coin flip on its median launders noise into signal; the uncertain molecule's fitness
// contribution is zeroed even though its measured feasibility is reported for the UI.
func TestScoreAndRankUncertainDoesNotBeatStable(t *testing.T) {
	docks := []models.LigandDock{
		covDock("uncertain", -8.0, -8.0, 0.8, true), // same median feasibility…
		covDock("stable", -8.0, -8.0, 0.8, false),   // …but this one is reproducible
	}
	r := ScoreAndRank("run", docks, nil, Options{})
	if got := rankOf(r, "stable"); got != 1 {
		t.Errorf("stable rank = %d, want 1 (uncertain must not win on a median)", got)
	}
	// The uncertain molecule still reports its MEASURED feasibility so the UI can flag it.
	for _, m := range r.Ranked {
		if m.SMILES == "uncertain" {
			if m.Scores.CovalentFeasibility == nil || *m.Scores.CovalentFeasibility != 0.8 {
				t.Errorf("uncertain: reported CovalentFeasibility = %v, want 0.8", m.Scores.CovalentFeasibility)
			}
		}
	}
}

// CovalentFeasibility is reported iff a covalent record exists: nil for a bare
// non-covalent binder, and the mirrored measured value for a warhead-bearing molecule.
func TestScoreAndRankReportsFeasibilityOnlyForCovalent(t *testing.T) {
	docks := []models.LigandDock{
		dock("plain", -8.0, -5.0),
		covDock("warhead", -8.0, -5.0, 0.7, false),
	}
	r := ScoreAndRank("run", docks, nil, Options{})
	for _, m := range r.Ranked {
		switch m.SMILES {
		case "plain":
			if m.Scores.CovalentFeasibility != nil {
				t.Errorf("plain: CovalentFeasibility = %v, want nil", *m.Scores.CovalentFeasibility)
			}
		case "warhead":
			if m.Scores.CovalentFeasibility == nil || *m.Scores.CovalentFeasibility != 0.7 {
				t.Errorf("warhead: CovalentFeasibility = %v, want 0.7", m.Scores.CovalentFeasibility)
			}
		}
	}
}

// A non-covalent molecule's scorecard must omit BOTH covalent fields from its JSON — a
// molecule with no warhead has no covalent story to tell, and emitting an empty covalent
// block or a 0 feasibility would read as a (false) covalent negative downstream.
func TestScoresJSONOmitsCovalentWhenAbsent(t *testing.T) {
	docks := []models.LigandDock{dock("plain", -8.0, -5.0)}
	r := ScoreAndRank("run", docks, map[string]float64{"plain": 0.6}, Options{})
	b, err := json.Marshal(r.Ranked[0].Scores)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "covalent") {
		t.Errorf("non-covalent scorecard JSON contains a covalent field: %s", b)
	}
}
