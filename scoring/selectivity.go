package scoring

import (
	"math"
	"sort"

	"github.com/ayush00git/stanza/models"
)

// Stage 7 — selectivity scoring & ranking. This turns each molecule's paired
// WT/mutant dock scores (plus its drug-likeness) into a single composite fitness,
// then ranks the run's docked molecules most-mutant-selective-first.
//
// Sign convention (from the docking stage): Vina affinities are negative kcal/mol,
// more negative = stronger binding. So:
//   - mutant_score: want MORE negative (bind the mutant well)
//   - wt_score:     want LESS negative / nearer 0 (bind the wild type poorly)
//   - selectivity = wt_score − mutant_score: want LARGE POSITIVE (mutant-selective)

// NormMode selects how each fitness term is normalised across the ranked pool.
type NormMode string

const (
	NormZScore NormMode = "zscore" // (x−μ)/σ over the pool; default
	NormMinMax NormMode = "minmax" // (x−min)/(max−min) into [0,1]
)

// defaultSelectTop is how many top-ranked molecules are flagged Selected when the
// caller doesn't specify.
const defaultSelectTop = 20

// FitnessWeights weight the three fitness terms. They are normalised to sum to 1
// before use; the defaults lean on selectivity because it is the product's point.
type FitnessWeights struct {
	Potency      float64 `json:"potency"`       // w_p — mutant potency (−mutant_score)
	Selectivity  float64 `json:"selectivity"`   // w_s — selectivity margin
	DrugLikeness float64 `json:"drug_likeness"` // w_q — QED
}

// DefaultWeights returns the default term weights (selectivity-leaning).
func DefaultWeights() FitnessWeights {
	return FitnessWeights{Potency: 0.35, Selectivity: 0.45, DrugLikeness: 0.20}
}

// Options configure a ranking pass.
type Options struct {
	Weights   FitnessWeights
	Norm      NormMode // "" defaults to zscore
	SelectTop int      // top-N flagged Selected; <=0 uses defaultSelectTop
}

// Scores is the selectivity scorecard for one docked molecule. Raw scores are kept
// beside the derived ones so a pool can be re-weighted or re-normalised without
// re-docking.
type Scores struct {
	SMILES      string   `json:"smiles"`
	MutantScore float64  `json:"mutant_score"` // kcal/mol into the MUTANT pocket
	WTScore     float64  `json:"wt_score"`     // kcal/mol into the WT pocket
	Selectivity float64  `json:"selectivity"`  // wt_score − mutant_score
	QED         *float64 `json:"qed"`          // drug-likeness (05); nil when unknown for this molecule
	Fitness     *float64 `json:"fitness"`      // composite, pool-normalised; nil when status != "scored"
	Status      string   `json:"status"`       // "scored" | "incomplete"
}

// RankedMolecule is one row of the ranked leaderboard.
type RankedMolecule struct {
	Rank     int    `json:"rank"`     // 1-based, dense
	Selected bool   `json:"selected"` // in the top-N carried-forward / highlighted pool
	SMILES   string `json:"smiles"`
	Scores   Scores `json:"scores"`
}

// Ranking is the computed, ordered view returned to callers (the loop / the UI).
// Fitness is pool-relative, so it is a ranking coordinate comparable only within
// this Ranking — not an absolute score.
type Ranking struct {
	RunID    string           `json:"run_id"`
	Weights  FitnessWeights   `json:"weights"`       // as applied (normalised to sum 1)
	Norm     NormMode         `json:"normalization"`
	Count    int              `json:"count"`         // molecules ranked
	Ranked   []RankedMolecule `json:"ranked"`        // sorted by fitness desc; rank 1 = best
	Excluded []Scores         `json:"excluded"`      // incomplete molecules, not ranked
}

// ScoreAndRank scores and ranks a run's docked molecules. qedBySMILES maps a
// molecule's (canonical) SMILES to its QED from validation; a molecule absent from
// the map has its drug-likeness term substituted with the pool minimum (worst)
// rather than being dropped, so a strong-but-ugly binder still ranks, penalised.
// Every dual-track dock carries both scores, so all are "scored"; the incomplete
// path is kept for forward-compatibility.
func ScoreAndRank(runID string, docks []models.LigandDock, qedBySMILES map[string]float64, opts Options) Ranking {
	weights := normalizeWeights(opts.Weights)
	norm := opts.Norm
	if norm != NormMinMax {
		norm = NormZScore
	}
	top := opts.SelectTop
	if top <= 0 {
		top = defaultSelectTop
	}

	r := Ranking{
		RunID:    runID,
		Weights:  weights,
		Norm:     norm,
		Ranked:   []RankedMolecule{},
		Excluded: []Scores{},
	}
	if len(docks) == 0 {
		return r
	}

	n := len(docks)
	pVals := make([]float64, n) // potency:     −mutant_score
	sVals := make([]float64, n) // selectivity: wt_score − mutant_score
	qVals := make([]float64, n) // drug-likeness: QED (pool-min substituted when unknown)
	scores := make([]Scores, n)

	// Pool minimum QED, used to substitute molecules with no known QED.
	haveQ := false
	minQ := math.Inf(1)
	qKnown := make([]bool, n)
	for i, d := range docks {
		if q, ok := qedBySMILES[d.SMILES]; ok {
			qKnown[i] = true
			qVals[i] = q
			if q < minQ {
				minQ = q
			}
			haveQ = true
		}
	}
	if !haveQ {
		minQ = 0
	}

	for i, d := range docks {
		pVals[i] = -d.MutantScore
		sVals[i] = d.Selectivity
		var qptr *float64
		if qKnown[i] {
			q := qVals[i]
			qptr = &q
		} else {
			qVals[i] = minQ // worst-in-pool substitution
		}
		scores[i] = Scores{
			SMILES:      d.SMILES,
			MutantScore: d.MutantScore,
			WTScore:     d.WTScore,
			Selectivity: d.Selectivity,
			QED:         qptr,
			Status:      "scored",
		}
	}

	pn := normalize(pVals, norm)
	sn := normalize(sVals, norm)
	qn := normalize(qVals, norm)

	ranked := make([]RankedMolecule, n)
	for i := range docks {
		f := weights.Potency*pn[i] + weights.Selectivity*sn[i] + weights.DrugLikeness*qn[i]
		fv := f
		scores[i].Fitness = &fv
		ranked[i] = RankedMolecule{SMILES: scores[i].SMILES, Scores: scores[i]}
	}

	// Sort by fitness desc; ties by selectivity desc, then mutant_score asc
	// (more negative = better mutant binding).
	sort.SliceStable(ranked, func(a, b int) bool {
		fa, fb := *ranked[a].Scores.Fitness, *ranked[b].Scores.Fitness
		if fa != fb {
			return fa > fb
		}
		if ranked[a].Scores.Selectivity != ranked[b].Scores.Selectivity {
			return ranked[a].Scores.Selectivity > ranked[b].Scores.Selectivity
		}
		return ranked[a].Scores.MutantScore < ranked[b].Scores.MutantScore
	})

	for i := range ranked {
		ranked[i].Rank = i + 1
		ranked[i].Selected = i < top
	}

	r.Count = n
	r.Ranked = ranked
	return r
}

// normalizeWeights scales weights to sum to 1, falling back to defaults when the
// caller's weights are non-positive.
func normalizeWeights(w FitnessWeights) FitnessWeights {
	sum := w.Potency + w.Selectivity + w.DrugLikeness
	if sum <= 0 {
		return DefaultWeights()
	}
	return FitnessWeights{
		Potency:      w.Potency / sum,
		Selectivity:  w.Selectivity / sum,
		DrugLikeness: w.DrugLikeness / sum,
	}
}

func normalize(vals []float64, mode NormMode) []float64 {
	if mode == NormMinMax {
		return minmax(vals)
	}
	return zscore(vals)
}

// zscore returns (x−μ)/σ using the population std. A zero std (all-equal, or a
// pool of one) yields all zeros, so the term contributes nothing rather than NaN.
func zscore(vals []float64) []float64 {
	out := make([]float64, len(vals))
	n := float64(len(vals))
	if n == 0 {
		return out
	}
	var mean float64
	for _, v := range vals {
		mean += v
	}
	mean /= n
	var variance float64
	for _, v := range vals {
		d := v - mean
		variance += d * d
	}
	variance /= n
	std := math.Sqrt(variance)
	if std == 0 {
		return out
	}
	for i, v := range vals {
		out[i] = (v - mean) / std
	}
	return out
}

// minmax returns (x−min)/(max−min) in [0,1]. A zero range yields 0.5 everywhere.
func minmax(vals []float64) []float64 {
	out := make([]float64, len(vals))
	if len(vals) == 0 {
		return out
	}
	mn, mx := vals[0], vals[0]
	for _, v := range vals {
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	rng := mx - mn
	if rng == 0 {
		for i := range out {
			out[i] = 0.5
		}
		return out
	}
	for i, v := range vals {
		out[i] = (v - mn) / rng
	}
	return out
}
