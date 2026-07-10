package scoring

import (
	"math"
	"sort"

	"github.com/ayush00git/stanza/models"
)

// Stage 7 — scoring & ranking. This turns each molecule's paired WT/mutant dock
// scores, its drug-likeness, and (when present) its covalent geometry into a single
// composite fitness, then ranks the run's docked molecules best-first.
//
// Sign convention (from the docking stage): Vina affinities are negative kcal/mol,
// more negative = stronger binding. So:
//   - mutant_score: want MORE negative (bind the mutant well)
//   - wt_score:     want LESS negative / nearer 0 (bind the wild type poorly)
//   - selectivity = wt_score − mutant_score: a real, NON-COVALENT WT/mutant margin.
//
// For a covalent target (this pipeline's KRAS G12C case) selectivity is expected to be
// ~0 and is NOT the payoff metric: non-covalent docking genuinely cannot separate WT
// from mutant — pan-KRAS binders hit WT, G12C, G12D and G12V at the same nanomolar Kd,
// so wt_score ≈ mutant_score by construction. The covalent evidence lives instead in
// CovalentDock.Feasibility, a dimensionless 0–1 geometry score (can the warhead reach
// the mutated cysteine's thiol along an attack trajectory the receptor actually binds).
// Feasibility is NOT an energy and must never be summed with a kcal/mol quantity; it
// enters fitness only after each term is normalised onto a common, unitless scale.

// NormMode selects how each fitness term is normalised across the ranked pool.
type NormMode string

const (
	NormZScore NormMode = "zscore" // (x−μ)/σ over the pool; default
	NormMinMax NormMode = "minmax" // (x−min)/(max−min) into [0,1]
)

// defaultSelectTop is how many top-ranked molecules are flagged Selected when the
// caller doesn't specify.
const defaultSelectTop = 20

// FitnessWeights weight the four fitness terms. They are normalised to sum to 1
// before use.
type FitnessWeights struct {
	Potency             float64 `json:"potency"`              // w_p — mutant potency (−mutant_score)
	Selectivity         float64 `json:"selectivity"`          // w_s — non-covalent WT/mutant margin (≈0 for covalent targets)
	DrugLikeness        float64 `json:"drug_likeness"`        // w_q — QED
	CovalentFeasibility float64 `json:"covalent_feasibility"` // w_c — dimensionless covalent-attack geometry (0–1)
}

// DefaultWeights returns the default term weights, tuned for a covalent target.
//
// The non-covalent selectivity term is known a priori to carry ~0 signal here (WT and
// G12C bind the same non-covalent pose to within ~0.1 kcal/mol), so it must NOT dominate
// the ranking — hence it is down-weighted, not dropped, so genuinely non-covalent runs
// still use it. Feasibility is the only covalent evidence a docked pose yields, so it
// takes the largest share; raw mutant potency is the next-best discriminator; QED keeps
// the leaderboard drug-like. The split is 0.40 feasibility / 0.30 potency / 0.20 QED /
// 0.10 selectivity.
func DefaultWeights() FitnessWeights {
	return FitnessWeights{Potency: 0.30, Selectivity: 0.10, DrugLikeness: 0.20, CovalentFeasibility: 0.40}
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
	SMILES      string  `json:"smiles"`
	MutantScore float64 `json:"mutant_score"` // raw Vina affinity into the MUTANT pocket (kcal/mol); no covalent credit
	WTScore     float64 `json:"wt_score"`     // kcal/mol into the WT pocket
	Selectivity float64 `json:"selectivity"`  // wt_score − mutant_score (real non-covalent margin; ~0 for covalent targets)
	// WTSpread/MutantSpread are the max − min affinity over each track's docking seeds:
	// the error bars on Selectivity. A margin smaller than its own spread reports the
	// search, not the receptor. Zero when the dock predates spread recording (Replicates 0).
	WTSpread     float64  `json:"wt_spread,omitempty"`
	MutantSpread float64  `json:"mutant_spread,omitempty"`
	Replicates   int      `json:"replicates,omitempty"`
	QED          *float64 `json:"qed"` // drug-likeness (05); nil when unknown for this molecule
	// CovalentFeasibility is the MEASURED covalent-attack feasibility (0–1) reported for
	// display, mirrored from Covalent.Feasibility; nil for non-covalent molecules. Note
	// this is the measured value even when the call is Uncertain — the UI should show it —
	// whereas the FITNESS term zeroes an uncertain molecule (see ScoreAndRank).
	CovalentFeasibility *float64 `json:"covalent_feasibility,omitempty"`
	Fitness             *float64 `json:"fitness"` // composite, pool-normalised; nil when status != "scored"
	Status              string   `json:"status"`  // "scored" | "incomplete"
	// Covalent is carried through from the dock so the leaderboard can flag
	// mutant-selective covalent binders; nil for non-covalent molecules.
	Covalent *models.CovalentDock `json:"covalent,omitempty"`
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
	Weights  FitnessWeights   `json:"weights"` // as applied (normalised to sum 1)
	Norm     NormMode         `json:"normalization"`
	Count    int              `json:"count"`    // molecules ranked
	Ranked   []RankedMolecule `json:"ranked"`   // sorted by fitness desc; rank 1 = best
	Excluded []Scores         `json:"excluded"` // incomplete molecules, not ranked
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
	fVals := make([]float64, n) // covalent feasibility for FITNESS: 0 for non-covalent and Uncertain molecules
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

		// Covalent feasibility splits into a REPORTED value and a FITNESS value.
		//   - Reported (Scores.CovalentFeasibility): the measured feasibility whenever a
		//     covalent record exists, so the UI can show it verbatim — even when Uncertain.
		//   - Fitness (fVals): only stable, positive evidence counts. A molecule with no
		//     covalent record contributes 0.0 (it is simply not a covalent binder). An
		//     Uncertain molecule ALSO contributes 0.0: its covalent call flips with the
		//     docking seed, so ranking it on its median would launder a coin flip into
		//     signal and could let noise outrank a stably feasible warhead.
		var cfptr *float64
		if d.Covalent != nil {
			cf := d.Covalent.Feasibility
			cfptr = &cf
			if !d.Covalent.Uncertain {
				fVals[i] = d.Covalent.Feasibility
			}
		}

		scores[i] = Scores{
			SMILES:              d.SMILES,
			MutantScore:         d.MutantScore,
			WTScore:             d.WTScore,
			Selectivity:         d.Selectivity,
			WTSpread:            d.WTSpread,
			MutantSpread:        d.MutantSpread,
			Replicates:          d.Replicates,
			QED:                 qptr,
			CovalentFeasibility: cfptr,
			Status:              "scored",
			Covalent:            d.Covalent,
		}
	}

	pn := normalize(pVals, norm)
	sn := normalize(sVals, norm)
	qn := normalize(qVals, norm)
	// For a run with NO covalent molecules at all, every fVals[i] is 0, so under the
	// default zscore σ=0 and normalize returns all zeros: the covalent term drops out
	// automatically and the pool ranks exactly as the pre-covalent scorer did. No
	// special-casing needed. (Under minmax an all-equal pool maps to a uniform 0.5, a
	// constant offset that likewise cannot reorder anything.)
	fn := normalize(fVals, norm)

	ranked := make([]RankedMolecule, n)
	for i := range docks {
		f := weights.Potency*pn[i] + weights.Selectivity*sn[i] + weights.DrugLikeness*qn[i] + weights.CovalentFeasibility*fn[i]
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

// normalizeWeights scales all four weights to sum to 1, falling back to defaults when
// the caller's weights sum to non-positive (which would otherwise divide by zero and
// poison every fitness in the pool with NaN).
func normalizeWeights(w FitnessWeights) FitnessWeights {
	sum := w.Potency + w.Selectivity + w.DrugLikeness + w.CovalentFeasibility
	if sum <= 0 {
		return DefaultWeights()
	}
	return FitnessWeights{
		Potency:             w.Potency / sum,
		Selectivity:         w.Selectivity / sum,
		DrugLikeness:        w.DrugLikeness / sum,
		CovalentFeasibility: w.CovalentFeasibility / sum,
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
